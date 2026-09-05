# Checks

Each finding carries `check`, `target`, `status`, `message`, a `value`/`unit`
where there is a number, and a `hint` that says what it means operationally.
Worst first, always.

`ERROR` sorts above `BAD`: a node that could not be read invalidates every
cluster-wide statement made without it, and that has to be seen first.

## The cluster as a whole

| Check | Grades |
|---|---|
| `node/read` | the node answered at all. ERROR — every cluster finding below is conditional on it |
| `node/not-galera` | `wsrep_provider` unset: a standalone server, excluded from every comparison |
| `cluster/uuid` | nodes reporting different state UUIDs — one name, two clusters |
| `cluster/conf-id` | nodes disagreeing about the membership generation (usually a change caught mid-flight) |
| `cluster/primary` | a node outside the Primary component: it refuses writes and serves stale reads |
| `cluster/size` | membership size against `--expect-nodes`; nodes *disagreeing* about it beats the size itself |
| `cluster/versions`, `cluster/provider-version` | mixed builds — expected during a rolling upgrade, a liability afterwards |

## Per node

| Check | Grades |
|---|---|
| `node/ready` | `wsrep_ready` OFF: queries touching replicated tables are refused |
| `node/connected` | `wsrep_connected` OFF: not in the group communication at all |
| `node/wsrep-on` | `wsrep_on` OFF: writes here are not replicated and will conflict later |
| `node/state` | the local state comment. Synced is OK, Donor/Desynced and Joined are warnings, anything else is BAD |
| `node/desync` | `wsrep_desync` left ON after a backup or a schema change |
| `node/read-only` | `read_only` ON — a failover that never finished, unless it is deliberate |
| `queue/recv`, `queue/send` | instantaneous queue depths: the node about to flow-control |

## The ones no metric shows

### `systables/drift`

The column definitions of every table in the `mysql` schema, fingerprinted per
node and compared.

Galera replicates the application's DDL. It does not replicate the maintenance
that happens to the server's own tables, so two nodes can hold different
definitions of `mysql.column_stats` — or of whatever `mysql_upgrade` touched on
one node and not the others — indefinitely, while every `wsrep_*` counter reads
perfectly healthy. The symptom arrives much later as a query plan that differs
per node, or one node's error log filling up.

A node without `SELECT` on `information_schema` is reported as **not audited**
rather than silently left out of the comparison.

### `schema/drift`

The same fingerprint comparison over the **application** base tables, keyed
`schema.table`.

Galera *does* replicate this DDL, and that is what makes it a different
diagnosis rather than the same check pointed at another schema: a difference
here is a schema change that did not finish — a failed `ALTER`, one applied on
a single node by hand, or one that landed while a node was desynced. The
counters stay green because replication is not broken; it carried what it was
given.

The fix is per node too, but it is *re-apply the change*, not `mysql_upgrade`.

A node that missed a whole schema change drifts on every table at once, so past
five tables the finding becomes one line with the count — four hundred findings
would bury the rest of the report. Views are excluded: their columns are
derived from tables that are already being compared. A node without `SELECT` on
`information_schema` is **not audited** rather than dropped from the
comparison, and a cluster with no application tables at all is quiet — "there
are none" and "they could not be read" are different findings.

`--no-schema` skips this read and the primary key one with it: on an instance
with tens of thousands of tables they are the only part of the audit that costs
more than microseconds.

### `schema/no-pk`

Application tables without a primary key, as a union across nodes. Galera's
row-based certification applies these with a full-row scan on every node, and
`DELETE`s can apply in a different order — the documentation calls it
unsupported.

### `schema/engine`

Application base tables on an engine Galera does not replicate — MyISAM, Aria,
anything that is not InnoDB.

The write succeeds. Nothing certifies it, nothing replicates it, and no counter
records that it went nowhere: the rows exist on the node that took them. This
is the sibling of `schema/no-pk` and a worse diagnosis — that one is a table
Galera certifies badly, this one is a table it does not certify at all.

MariaDB can be told to replicate MyISAM and Aria (`wsrep_mode`, or the older
`wsrep_replicate_myisam`). That is experimental, and it is **per node**: when
the nodes disagree about it the finding is `BAD` rather than `WARN`, because
then the same write lands on some of them and not others.

### `pxc/strict-mode`

Percona XtraDB Cluster is Galera under another name — the same provider, the
same `wsrep_*` variables — so every check on this page already applies to it.
The one thing it adds is `pxc_strict_mode`, which decides whether a node
**refuses** the operations that break replication silently: a table with no
primary key, a write to a MyISAM table, an unsupported DDL.

It is per node like everything else here, so the nodes **disagreeing** is `BAD`
and not the obvious case: the permissive node accepts a statement its peers
would have rejected, and the cluster then has to replicate something it was
configured to refuse. Uniformly off is `WARN`, and the finding names
[`schema/no-pk`](#schemano-pk) and [`schema/engine`](#schemaengine) — with the
guard rail down, those are the consequences rather than warnings about a
possibility.

MariaDB has no such variable, and a verdict about a setting that does not exist
is not a verdict.

## The write paths nobody drew

A cluster is drawn as three nodes replicating to each other. Real deployments
are rarely that tidy, and **the cluster cannot see a write path it is not part
of** — so none of this appears in any `wsrep_*` counter.

### `repl/async-in`, `repl/async-out`

`SHOW REPLICA STATUS` and `SHOW REPLICAS` per node — under their older names on
older builds, and read by column name because those were renamed with the
statements.

- A member that is **also an async replica** is a second write path into the
  cluster: `WARN`, naming the source. Those writes certify like any other and
  the cluster has no opinion about where they came from.
- A link that is **configured and not running** is `BAD`, carrying the server's
  own error. Somebody believes those writes are arriving — and a link that
  resumes after a long stop replays everything it missed into a cluster that has
  moved on.
- A member **feeding a downstream replica** is `WARN`: a dependency the rest of
  the cluster knows nothing about, and the next SST rebuilds this node's binary
  logs out from under it.

`nil` versus empty is load-bearing here: a server with no replication answers
with zero rows, and that is a different statement from a read that was refused.
The second is reported by [`audit/coverage`](#auditcoverage).

### `repl/writers`

`wsrep_replicated` per node — the writesets each node *originated* — over the
interval. Needs `--state`.

"We only write to one node" is a belief, and this is the number that confirms
or refutes it. No per-node dashboard shows a second writer, because each node
looks busy in its own right; and writing to several nodes is where certification
conflicts come from, so this is the **cause** behind a
[`repl/cert-failures`](#replcert-failures) finding rather than another symptom.

A share under 2% is a heartbeat, a schema tool or a stray connection rather than
a writer. An idle interval says so instead of inventing one. Without a baseline
it reports who has written since each node last restarted — a different question
— and says it is not graded.

### `node/binlog`, `node/binlog-format`, `node/binlog-updates`

Galera does not need the binary log: it replicates by writeset. Everything
*around* a cluster needs it.

- **`node/binlog`** — `log_bin` off on some nodes and on elsewhere: no backup,
  no downstream replica and no point-in-time recovery can be taken from those,
  which is discovered during a failover, when they are the ones left.
- **`node/binlog-format`** — the nodes disagreeing, and also the nodes agreeing
  on something that is not `ROW`, which Galera's own documentation requires.
- **`node/binlog-updates`** — `log_slave_updates` differing, graded only when
  some node has a binary log at all. A node that does not log the writesets it
  applies cannot be the source of a downstream replica: failing over to it
  breaks that replica silently.

### `node/restarted`

`wsrep_gcomm_uuid` — a new value on every boot — compared with the previous run,
or an uptime shorter than it was on a build that does not report the uuid. Needs
`--state`.

A restart resets every counter, which is why the rate checks fall back to *no
baseline*; on its own that fallback looks like the tool being coy. This says
what happened. And if nobody planned the restart, **that** is the finding rather
than the ungraded rates.

An absent uuid on either side means nothing to compare, so a state file written
before this check existed reports nothing — it did not need the format version
to move, because an absent uuid cannot be mistaken for a different one.

### `cluster/membership-view`

`information_schema.WSREP_MEMBERSHIP`, where the `wsrep_info` plugin is
installed: the **group's own** list of its members, against the nodes this run
audited. Members are matched on every spelling a node answers to, the same way
the proxy's server list is.

Two independent views of one membership, and the findings live only in the
comparison:

- a member the group lists that this run never read is `WARN` — every
  cluster-wide statement in the report was made without it, and a member nobody
  knows about is a member nobody is watching;
- a node that reported itself as a member while the group has not listed it is
  `BAD`, which no single node can report about itself.

The plugin is optional, so its absence is silence rather than a gap: unlike a
missing grant, nothing was denied, and [`audit/coverage`](#auditcoverage) does
not count it.

### `repl/server-id`, `repl/gtid-domain`, `repl/gtid-strict`

- **`repl/server-id`** — two nodes sharing a `server_id` is `BAD`, with or
  without a binary log: a replica downstream cannot tell their events apart, and
  a replication loop becomes possible.
- **`repl/gtid-domain`** — every node in the cluster should share
  `gtid_domain_id`. With different ones, a failover *inside* the cluster
  rewrites history for every replica reading from it, and nothing inside the
  cluster notices, because replication here is by writeset.
- **`repl/gtid-strict`** — `gtid_strict_mode` differing means whether a
  downstream replica refuses a bad sequence depends on which node it happened to
  be reading from.

The two GTID checks are graded **only when some node has a binary log**: with
nothing able to replicate out, a domain id that cannot reach anybody is not a
finding. This tool does not have opinions about settings nothing reads.

### `repl/triggers`

`wsrep_slave_run_triggers` per node. The writer's trigger has already put its
rows into the writeset, so an applier that runs the trigger again applies them
twice, and one that does not is doing the right thing.

The nodes **disagreeing** is therefore `BAD` — the same statement ends up with
different rows per node, and certification compares writesets rather than their
consequences. Uniformly on is `WARN`: the nodes stay consistent with each other,
but every row the trigger writes is applied twice unless the trigger expects
that.

## What the next restart costs

Everything in this section is free today. It is a state the cluster has already
decided, and the bill arrives at the next restart, failover or partition — which
is why no `wsrep_*` counter has an opinion about any of it.

### `sst/method`, `sst/donor`, `sst/auth`

- **`sst/method`** — the nodes disagreeing about `wsrep_sst_method`. The joiner
  asks and the donor serves, so a donor without the joiner's method installed
  cannot answer.
- **`sst/donor`** — `wsrep_sst_donor` naming a server this cluster does not
  have, under any spelling it answers to. Whether that is fatal is decided by a
  trailing comma: `node1,` falls back to any donor (`WARN`), `node1` is the only
  donor allowed and the node **refuses to start** without it (`BAD`).
- **`sst/auth`** — an empty `wsrep_sst_auth` with a method that logs in to the
  donor. The credentials can legitimately live in the `[sst]` section of the
  config where the server cannot see them, so this is a `WARN` that says so
  rather than a verdict.

### `quorum/ignore-sb`, `quorum/bootstrap`, `quorum/weight`

- **`quorum/ignore-sb`** — `pc.ignore_sb` on. The node keeps accepting writes in
  a non-Primary component, so the next partition leaves both sides writable and
  diverging. Usually left behind after a cluster was recovered by hand.
- **`quorum/bootstrap`** — `pc.bootstrap` still set. A one-shot trigger left in
  the configuration makes the node form its own Primary component next time it
  starts alone.
- **`quorum/weight`** — `pc.weight`, reported as arithmetic rather than as an
  opinion: the sum, and whether one node alone holds a majority. Weight 0 is
  `BAD` — that node never counts towards quorum, so the cluster has one fewer
  vote than it has nodes.

### `gcache/recover`

`gcache.recover` off. [`gcache/window`](#gcachewindow) measures how much time
the write-set cache buys before a restarting node needs a full state transfer —
and with `gcache.recover` off, a clean restart discards that cache along with
the process. The window is a buffer the setting throws away: even a two-minute
maintenance restart then costs a full SST, which takes a donor out of service
with it.

One finding per node, because each node's restart is its own. A provider that
does not report the option is not a provider with it off — it arrived in Galera
3.19.

### `repl/osu-method`

`wsrep_OSU_method` set to RSU. TOI replicates a schema change to every node and
NBO does the same without holding the cluster-wide lock; **RSU does not
replicate it at all** — it applies the change where it was run and leaves the
others alone.

That is precisely how the drift [`schema/drift`](#schemadrift) reports comes to
exist, which is why this check is here: the cause reported next to the symptom
is the difference between a finding and a diagnosis. RSU is something to turn on
for one operation and off again, not a default.

### `cluster/peers`

`wsrep_cluster_address` — the list of peers a node contacts when it starts —
resolved against the nodes actually in the component, on every spelling each one
answers to.

It is only exercised at a restart, which is why nothing reports it. Three
outcomes: a list naming servers this cluster does not have is `WARN` (the
remaining peers still answer, but the list is one decommission away from naming
nobody); a list naming **no** current member is `BAD` (this node cannot rejoin);
and an empty `gcomm://` is `BAD` — that is the bootstrap form, and left in a
running node's configuration it means the next restart forms a second Primary
component instead of rejoining this one.

### `flow/settings`

`gcs.fc_limit`, `gcs.fc_factor` and the master-slave flags, compared across
nodes. The cluster pauses when the slowest queue reaches **its own** limit, so
the node configured with the smallest one paces every writer in the cluster.
[`flow/paused`](#flowpaused) reports that pausing; this reports the reason, and
names the node that throttles first.

### `repl/appliers`

`wsrep_slave_threads` (or `wsrep_applier_threads`, its 10.6 name) differing
between nodes, reported next to that node's receive queue. A node with a quarter
of its peers' apply threads is behind by **configuration** rather than by load —
which is a different fix from looking at its disk.

### `sst/size`

What a rejoin actually copies: the data and index length of every application
table, from the largest node, with the SST method beside it. "This node needs a
full SST" is not actionable without the gigabytes it implies and the donor it
takes out of service for the duration. A size is a number rather than a fault,
so this is `OK`; [`gcache/window`](#gcachewindow) is the check that grades
whether an SST is likely at all.

### `audit/changes`

What moved since the previous run: appeared, cleared, got worse, improved.
Needs `--state`, because the answer lives in the state file — which carries the
previous run's verdicts alongside its counters.

The person reading this ran the audit twenty minutes ago and did something in
between; what they need is not the same list again. Only the **statuses** are
compared, never the messages: a message carries a measurement, and comparing
prose would report a change every time a percentage moved by 0.1. The summary
is always `OK` — every finding it mentions is in the same report with its own
severity, and counting them twice makes one incident look like two.

A cluster that could not be read at all still carries its findings forward, so
"the node came back" is a transition rather than a silence.

### `audit/coverage`

One line saying what this run could **not** audit: a node that could not be
read, a missing `information_schema` grant, a clock that did not answer, a
missing baseline.

A cron job sees an exit code and a worst status, and neither distinguishes
"nothing is wrong" from "the check that would have found it never ran". Access
gaps are `WARN`, because a statement was not made. A missing baseline is named
in the same line and does not escalate: running without `--state` is a choice,
and warning about a choice on every run is how a check stops being read.

### `repl/sync-wait`

The nodes disagreeing about `wsrep_sync_wait`. With it on, a read waits for the
writes committed before it; with it off, the same query can return a row that is
not there yet. When the nodes disagree the answer depends on which node the
proxy picked, and every node is behaving exactly as configured. What the cluster
*wants* is not this tool's business; the nodes not agreeing is.

### `repl/auto-increment`

`wsrep_auto_increment_control` keeps the auto-increment step and offsets in line
with the membership. With it off, the values are whatever somebody typed: two
nodes sharing an `auto_increment_offset` is `BAD` (the ids collide as soon as
both take writes, and the damage lands in application data), and a step smaller
than the number of nodes is `WARN`.

### `repl/ws-limits`

`wsrep_max_ws_size` and `wsrep_max_ws_rows` differing between nodes. A write-set
is certified on the node that accepted it and then applied everywhere: an
applier whose limit is smaller refuses it and leaves the cluster. That arrives
as a node failure, and it is a configuration difference. The finding names the
node holding the cluster's real limit — the smallest non-zero one, since 0 is
"unlimited".

### `node/clock`

The spread between the nodes' own clocks, read as
`UNIX_TIMESTAMP(NOW(6))` in the same round trip as everything else — an epoch,
so a server's `time_zone` cannot change the answer.

The nodes are compared **with each other**, not with the machine running the
audit, because this host's NTP is not a reference either. Replication is
unaffected: Galera orders writes by sequence number, not by time. Everything a
human does during an incident is affected — reading two error logs side by side,
correlating a spike with a backup, believing a timestamp. Thresholds:
`--clock-warn` (2s) and `--clock-bad` (30s). A clock that could not be read is
reported as not graded rather than as a clock that agrees.

### `node/durability`

`innodb_flush_log_at_trx_commit` and `sync_binlog` differing between nodes. A
cluster's durability is its **weakest** node's, not its average: on a node that
acknowledges a commit before the log reaches the disk, "committed on every node"
survives a process crash and not a power cut.

A uniform relaxed setting is not a finding — that is the cluster's decision, and
this tool does not have an opinion about it. The nodes disagreeing is the
finding.

### `cluster/latency`

`wsrep_evs_repl_latency` — the cluster's **own** measurement of the round trip
between a node and the group, printed as `min/avg/max/stddev/samples` in
seconds — read next to the segment map.

[`queue/send`](#queuerecv-queuesend) reports a deep queue and cannot say why: a
node across a WAN link is doing exactly what physics allows, and a node with a
failing disk in the same rack looks identical from there. So the comparison
happens **inside a segment**, where distance is not an explanation: a node four
times slower than the fastest of its own segment is a `WARN` that names the
link and carries that node's send queue. Across segments the latency is
reported and never graded — that is what the segment was configured for.

Two guards keep it honest. `--latency-floor` (2ms) is the level below which a
ratio is noise: 4x between 90µs and 350µs is not a finding, and grading it is
how a check gets switched off. And zero samples is not zero latency — a
provider that has measured nothing reports `0/0/0/0/0`, and that is *not
graded* rather than the fastest cluster in the fleet.

### `cluster/segments`

The `gmcast.segment` map, reported as the map it is: which nodes are in which
segment. A write-set crosses a link once per segment rather than once per node,
and the intent behind a particular map lives in somebody's head rather than in
the server — so this is `OK` with the map in the message.

The one shape that cannot be deliberate is graded: every node in a segment of
its own, which turns the optimisation off entirely and makes every node pay the
WAN transfer separately.

### `gcache/window`

Not the gcache size: the **time** it buys at the current write rate. 512 MB is
forty minutes or ninety seconds depending on the workload, and the number
decides whether a restarting node catches up incrementally or needs a full SST
— which also takes a donor out of service. Needs `--state`; see
[rates, not totals](rates.md).

## Against the proxy

With `--proxysql`, the proxy's view is compared with the cluster's — the
interesting state being the two disagreeing, which neither dashboard shows.

| Check | Grades |
|---|---|
| `proxysql/read` | the admin interface answered. ERROR; the cluster findings still stand |
| `proxysql/missing` | a cluster node in no serving hostgroup: capacity nothing reaches, and a failover target that cannot take over |
| `proxysql/disagreement` | ONLINE while the node reports Joined (BAD), or shunned while the node reports Synced (WARN) |
| `proxysql/hostgroup` | a hostgroup with servers configured and none ONLINE: every query routed there fails |
| `proxysql/mapping` | the writer/reader/offline mapping, reported and **not graded** |
| `proxysql/monitor` | the Galera monitor is not driving the hostgroups: `mysql-monitor_enabled=false`, or a hostgroup set with `active=0`. BAD — every finding above it is a photograph |

The offline hostgroup — 999 in most deployments — is where ProxySQL's own Galera
monitor parks nodes. Its contents are never a finding: flagging them is a
permanent false positive, and "cleaning it up" fights the monitor.

The same monitor is what keeps every one of those comparisons *live*, which is
why `proxysql/monitor` exists: with the monitor off, the hostgroups still look
exactly like a healthy cluster's — they just stopped following the one that is
running. A proxy with no Galera hostgroup table at all is a deployment choice
rather than a stopped monitor, and an unread variable is not a variable set to
false: both stay quiet.
