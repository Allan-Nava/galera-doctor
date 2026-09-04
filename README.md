<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="galera-doctor" src="assets/logo.svg" width="560">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/Allan-Nava/galera-doctor/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Allan-Nava/galera-doctor/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-10b981"></a>
  <img alt="Go" src="https://img.shields.io/badge/go-1.25%2B-00ADD8">
  <img alt="Read only" src="https://img.shields.io/badge/writes-none-10b981">
  <a href="https://allan-nava.github.io/galera-doctor/"><img alt="Docs" src="https://img.shields.io/badge/docs-galera--doctor-0f766e"></a>
</p>

---

**galera-doctor audits a MariaDB/MySQL Galera cluster read-only and reports the
states the cluster's own metrics cannot show you.** One static Go binary, one
dependency, only `SHOW` and `SELECT` — enforced in code, not promised in a
README.

`wsrep_cluster_size` being right is not the same as the cluster being right.
These are the states this tool exists for:

| What is wrong | Why no `wsrep_*` metric shows it |
|---|---|
| **A system table's definition differs between nodes** | Galera does not replicate maintenance on the server's own `mysql.*` tables. Two nodes can disagree about `mysql.column_stats` for months while every replication counter stays green — until one node's query plans, or its error log, go strange. |
| **A schema change that did not finish** | Galera replicates application DDL, so the counters stay green: they carried what they were given. One node with a failed `ALTER`, or one that was desynced while the change went through, holds a different definition of an application table — and the application finds out. |
| **One name, two clusters** | Each half reports a consistent size and a Primary status. The divergence is only visible by comparing the `wsrep_cluster_state_uuid` of every node *to each other*. |
| **The proxy and the cluster disagree** | ProxySQL says ONLINE, the node says Joined. Each dashboard is fine. Traffic is going to a node that is not synced. |
| **The gcache is too small for the write rate** | 512 MB is either forty minutes or ninety seconds. Nobody finds out until a node restarts and needs a full SST, taking a donor out of service with it. |
| **The next restart cannot rejoin** | `wsrep_sst_donor` naming a server that was decommissioned in March, or an SST method the donors cannot serve. Everything is Synced and green until the node restarts, and then it either refuses to start or takes an unexpected donor out of service. |
| **A split brain that is already configured** | `pc.ignore_sb` left on after somebody recovered a cluster by hand, or quorum weights that are not one vote per node. Nothing is exercised until the network moves — and then both sides stay writable. |
| **Tables Galera does not replicate at all** | A MyISAM or Aria table in an application schema. The write succeeds, nothing certifies it, no counter records it, and the rows exist on exactly one node. |
| **Durability that is not the cluster's** | A cluster's durability is its weakest node's, not its average. One node acknowledging a commit before its log reaches the disk turns "committed on three nodes" into something else — and each node is doing exactly what it was told, so nothing reports it. |
| **A write path nobody drew** | A cluster is drawn as three nodes replicating to each other. It does not show the member that is also an async replica of a legacy server, or the one feeding a reporting replica downstream — and the cluster cannot see a write path it is not part of. |
| **Slow, or simply far away** | A deep send queue says nothing about the cause: a node across a WAN link is doing what physics allows, and a node with a failing disk in the same rack looks identical from there. `wsrep_evs_repl_latency` and the segment map together say which one it is. |
| **A node that cannot find its cluster** | `wsrep_cluster_address` is only read at startup. A list naming two decommissioned servers — or an empty `gcomm://` left behind after a bootstrap — belongs to a node that is Synced and green today and forms its own cluster tomorrow. |
| **Flow control that already happened** | `wsrep_flow_control_paused` covers the time since the last status reset, so an incident from March reads the same today. Graded as a lifetime total it goes red once and stays red. |

## What a run looks like

```console
$ galera-doctor audit --config clusters.json --cluster compress --state /var/lib/galera-doctor/compress.json
BAD   compress  3 node(s)
  BAD   cluster/uuid             compress       nodes report different cluster state UUIDs: 5b1e2a8c-111… (cl-02, sg-01) vs 9999aaaa-222… (ov-03)
        ↳ this is a partition, not a lag: the groups have diverged and one side has to be reinitialised from the other
  BAD   systables/drift          mysql.column_stats  definition differs across nodes: aaaaaaaaaaaa… (ov-03, sg-01) vs cccccccccccc… (cl-02)
        ↳ Galera does not replicate this: fix it per node (mysql_upgrade, or align the definition by hand) — no wsrep_* metric will ever show it
  WARN  flow/paused              sg-01          flow-controlled 2.10% of the last 10m0s
        ↳ this node is intermittently the slowest in the cluster; look at its disk and its replication threads
  WARN  schema/no-pk             schema         1 table(s) without a primary key: app.events
  OK    cluster/size             compress       3 member(s), expected 3
```

*(The cluster above is the fixture the test suite audits — the failure shapes are
real, the hostnames are not.)*

Point it at something that is not a cluster and it says so once, instead of
reporting an outage that is not happening:

```console
$ galera-doctor audit --node "local=root:***@tcp(127.0.0.1:13306)/"
ERROR cluster  1 node(s)
  ERROR cluster/membership       cluster        no node in this list is running Galera
  ERROR node/not-galera          local          not a Galera node: wsrep_provider is not configured (server 11.4.13-MariaDB-ubu2404)
        ↳ excluded from every cluster comparison — grading it would report an outage that is not happening
```

## Checks

```console
$ galera-doctor checks
node/read                                  the node answered at all — every cluster finding is conditional on this
cluster/uuid                               nodes reporting different state UUIDs: one name, two clusters
cluster/conf-id                            nodes disagreeing about the membership generation
cluster/primary                            a node that is not in the Primary component
cluster/size                               membership size, and nodes disagreeing about it
node/ready, node/connected, node/wsrep-on  the node is replicating at all
node/state                                 Synced, Donor/Desynced, Joined, or something worse
node/desync, node/read-only                deliberate exclusions that were never undone
flow/paused                                share of the interval spent flow-controlling (needs --state)
repl/cert-failures                         write conflicts as a share of writesets (needs --state)
queue/recv, queue/send                     instantaneous queue depths
systables/drift                            definitions of the mysql.* tables differing between nodes
schema/drift                               application table definitions differing between nodes
schema/no-pk                               tables Galera cannot certify reliably
schema/engine                              application tables on an engine Galera does not replicate
sst/method, sst/donor, sst/auth            whether the next node to restart can rejoin
quorum/ignore-sb, quorum/bootstrap         settings that decide the next partition
quorum/weight                              the quorum arithmetic, when it is not one vote per node
repl/sync-wait                             nodes disagreeing about causal reads
repl/auto-increment                        ids that collide once a second node takes writes
cluster/versions                           mixed server or wsrep provider versions
gcache/window                              how much time the gcache buys before a restart needs a full SST (needs --state)
gcache/recover                             a clean restart that discards the write-set cache anyway
repl/osu-method                            a node on RSU: DDL applied here and not replicated
repl/ws-limits                             appliers that will refuse a write-set the cluster certified
node/clock                                 the spread between the nodes' own clocks
node/durability                            a cluster whose durability is one node's, not its average
cluster/segments                           the segment map, and the one shape that cannot be deliberate
cluster/latency                            slow, or simply far away: the cluster's own round-trip measurement
cluster/peers                              a peer list that describes a cluster which no longer exists
flow/settings                              one node's flow-control limit pacing every writer
repl/appliers                              a node that applies with fewer threads than its peers
sst/size                                   what a rejoin copies, and how long a donor is out of service
audit/coverage                             what this run could not audit, in one line
repl/async-in, repl/async-out              async replication into or out of a cluster member
repl/server-id                             nodes sharing a server_id
repl/gtid-domain, repl/gtid-strict         what a failover does to a downstream replica
repl/triggers                              triggers that run on the appliers of one node only
audit/changes                              what appeared, cleared or got worse since the last run (needs --state)
proxysql/*                                 the proxy's view against the cluster's (needs --proxysql)
proxysql/monitor                           a proxy whose Galera monitor stopped: the hostgroups are a photograph
```

## A total is not a rate

The wsrep counters only go up, and they reset on restart. A threshold over
`wsrep_flow_control_paused` goes red once and stays red, and a check that stays
red is a check people stop reading.

So `--state FILE` remembers the counters between runs and the checks grade the
**interval**:

```console
  WARN  flow/paused   sg-01   flow-controlled 2.10% of the last 10m0s
```

Without a baseline the same check reports the lifetime figure and refuses to
judge it:

```console
  OK    flow/paused   sg-01   0.9% of the time since the last status reset (not graded: no baseline)
        ↳ run again with --state to grade the interval between runs instead of the lifetime total
```

A counter that went backwards, a node whose uptime shrank, a state file from
another format version: all of them mean *no baseline*, never a negative rate
and never a spectacular one.

The same file carries the previous run's verdicts, so a second run says what
moved rather than repeating itself:

```console
  OK    audit/changes   compress   since the previous run (17m0s ago) — cleared: systables/drift@mysql.column_stats; appeared: flow/paused@sg-01 (WARN)
```

## While you are repairing it

```console
$ galera-doctor audit --config clusters.json --watch 10s
BAD   compress  3 node(s)
  BAD   cluster/uuid    compress  nodes report different cluster state UUIDs: …
  …
16:34:48  OK    cluster/uuid   compress/compress  all nodes report one cluster  [BAD → OK]
16:35:18  WARN  node/state     compress/ov-03     local state is Donor/Desynced  [OK → WARN]
```

The first report in full, so you know where you are starting from, and after
that **only what moved** — including a finding that went away, which during a
repair is usually the line you are waiting for. A tick that changed nothing
prints nothing: reprinting twenty OK lines every ten seconds buries the one
that matters.

Still not a daemon and still not a monitoring system: it runs in the
foreground, holds its baseline in memory, and stops when you do.

## Configuration

```json
{
  "clusters": {
    "compress": {
      "nodes": [
        {"name": "sg-01", "dsn": "audit:${GALERA_DOCTOR_PASS}@tcp(10.11.1.5:3306)/"},
        {"name": "cl-02", "dsn": "audit:${GALERA_DOCTOR_PASS}@tcp(10.21.1.5:3306)/"},
        {"name": "ov-03", "dsn": "audit:${GALERA_DOCTOR_PASS}@tcp(10.35.1.5:3306)/"}
      ],
      "proxysql_dsn": "admin:${PROXYSQL_ADMIN_PASS}@tcp(10.11.1.9:6032)/",
      "expect_nodes": 3
    }
  }
}
```

`${ENV_VAR}` is expanded in every DSN, and an **unset** variable is an error
rather than an empty string — "access denied" is a terrible way to learn that a
variable is missing. A DSN is never printed, and a driver error that quotes one
is redacted before it reaches a terminal.

Or skip the file entirely: `--node name=DSN`, repeatable.

The audit user needs very little:

```sql
CREATE USER 'audit'@'%' IDENTIFIED BY '…';
GRANT USAGE, PROCESS, SELECT ON *.* TO 'audit'@'%';   -- SELECT is for information_schema
```

Without `SELECT` on `information_schema` the drift comparison reports the node
as *not audited* instead of quietly leaving it out of the comparison.

## Read-only, mechanically

Every query goes through one function that refuses anything which is not a
`SHOW` or a `SELECT`, CI greps the source for a writing statement, and there is
a test that tries `UPDATE`, `DELETE`, `SET GLOBAL`, `FLUSH` and `DROP` and
requires all of them to be rejected. This tool gets pointed at clusters that are
already having a bad day; "it doesn't write anything" has to be a property, not
a claim.

## Install

```sh
# Homebrew — the cask lives in the shared tap
brew install --cask Allan-Nava/tap/galera-doctor

# a container that holds nothing but the binary
docker run --rm ghcr.io/allan-nava/galera-doctor:latest checks

# or the Go toolchain
go install github.com/Allan-Nava/galera-doctor/cmd/galera-doctor@latest
```

Or a release archive: six platforms with a `SHA256SUMS` and a provenance
attestation (`gh attestation verify`), built by the tag itself. See
[install](docs/install.md).

## Output and exit status

| Flag | Output |
|---|---|
| *(none)* | text, worst cluster first, hint on its own line |
| `--json` | everything |
| `--findings` | the flat findings array the sibling tools speak — empty array, never `null` |
| `--min-severity S` | hide findings below `S`; the cluster header stays |
| `--watch D` | the first report in full, then only what changed, every `D` |

| Exit | Meaning |
|---|---|
| `0` | the audit ran — findings are output, not an error |
| `1` | `--exit-on S` was given and something reached `S` |
| `2` | usage error, or no node could be resolved |

## What it is not

- **Not a monitoring system.** It has no daemon, no scraper and no history
  beyond the one state file it uses for rates. It is what you run from cron, a
  CI job or an incident.
- **Not a generic MySQL health check.** Connections, buffer pool, slow queries
  and replica lag belong in [checkfleet](https://github.com/Allan-Nava/checkfleet),
  which has a `mysql` module. This one only knows about Galera.
- **Not a repair tool.** It reports; a human decides. Bootstrapping a cluster,
  dropping a peer or resyncing a node are operations with consequences, and
  they are not one flag away here.

## Development

```sh
go test ./...
go test -race ./...

# the SQL, against a throwaway server
docker run -d --rm --name gd-test -e MARIADB_ROOT_PASSWORD=testpw -p 13306:3306 mariadb:11.4
GD_TEST_DSN='root:testpw@tcp(127.0.0.1:13306)/' go test ./internal/cluster/ -run Real -v
```

`BACKLOG.md` is the single source of truth for planned work and
[ROADMAP.md](ROADMAP.md) is generated from it. Why the tool exists and what it
will never become: [INTENT.md](INTENT.md). Contributor brief:
[AGENTS.md](AGENTS.md).

The reference pages at
[allan-nava.github.io/galera-doctor](https://allan-nava.github.io/galera-doctor/)
are generated from the same `docs/*.md` that render on GitHub — one source per
document, and a CI gate that fails when a page is stale.

Everything around the code is a POSIX-sh script with a CI gate behind it, so a
stale generated file fails the build instead of rotting:

```sh
scripts/backlog.sh roadmap   # regenerate ROADMAP.md from BACKLOG.md
goreleaser release --snapshot --clean     # the release artefacts, locally
scripts/release.sh notes v0.9.0           # the notes a release page would get
scripts/links.sh             # every local link in the docs and the site resolves
scripts/site.sh serve        # the landing page in site/, on localhost:8000
scripts/docs.sh build        # render docs/*.md into site/docs/
scripts/og.sh                # re-render the preview card at 1200x630
scripts/repo.sh apply        # write .github/repo.env to the GitHub About box
```

Each of them has a `scripts/<name>_test.sh` next to it — POSIX sh, fixtures,
a fake `gh` where GitHub is involved — and CI runs all of them: a gate that
quietly stopped firing looks exactly like a repository with no problems.

The repository's description, website and topics live in
[.github/repo.env](.github/repo.env) rather than in a text field somebody typed
once: `scripts/repo.sh apply` writes them and CI fails when the two drift.

## License

MIT — see [LICENSE](LICENSE).
