# Backlog — galera-doctor

Single source of truth for what is planned. Items keep a stable `GD-n` id so
commits, the CHANGELOG and issues can reference them. New ideas go here rather
than into scattered TODO comments.

[ROADMAP.md](ROADMAP.md) is a **generated** view of this file, grouped by
milestone. Do not edit it by hand — run `scripts/backlog.sh roadmap` after
touching this file, or CI will fail.

## How to write an item

```
## M3 — Title of the milestone <!-- ms: target=v0.2.0 phase=now -->

- [ ] **GD-15 — Short name**: what it is, why it earns its place, what it
  needs to touch. <!-- gd: prio=high size=L labels=check,collect -->
```

- The **id never changes**. Adding an item means taking the next free number,
  never reusing a retired one.
- `- [ ]` is open, `- [x]` is shipped, and a shipped item carries the release it
  went out in: `ver=0.1.0`.
- Metadata lives in a trailing `<!-- gd: ... -->` comment. Keys: `prio`
  (`high|med|low`), `size` (`S|M|L|XL`), `labels`, `ver` (shipped items only).
- Milestone metadata is a trailing `<!-- ms: ... -->` on the heading: `target`
  and `phase` (`shipped|now|next|later|ongoing`).
- Labels: `collect`, `check`, `output`, `cli`, `proxysql`, `delivery`,
  `integration`, `tests`, `docs`, `release`, `project`.

## M1 — See what the metrics cannot <!-- ms: target=v0.1.0 phase=shipped -->

- [x] **GD-1 — Read-only by construction**: every query goes through one
  function that refuses anything but `SHOW` and `SELECT`, with a test that
  tries `UPDATE`, `DELETE`, `SET GLOBAL`, `FLUSH` and `DROP`, and a CI grep over
  the source. A tool pointed at a cluster in trouble has to be provably unable
  to make it worse. <!-- gd: prio=high size=M labels=collect ver=0.1.0 -->
- [x] **GD-2 — System table drift**: fingerprint the column definitions of every
  table in the `mysql` schema per node and compare. Galera does not replicate
  this, so the drift is invisible to every `wsrep_*` counter — it is the check
  the tool exists for. <!-- gd: prio=high size=L labels=check ver=0.1.0 -->
- [x] **GD-3 — Cluster identity**: state UUID and configuration id compared
  across nodes, so one name covering two clusters is a finding rather than
  something you notice from row counts.
  <!-- gd: prio=high size=M labels=check ver=0.1.0 -->
- [x] **GD-4 — Counters graded as rates**: `--state` remembers the totals
  between runs; flow control, certification failures and the gcache window are
  graded over the interval. Without a baseline they report the lifetime figure
  and say they were not graded — a threshold over a monotonic counter goes red
  once and stays red. <!-- gd: prio=high size=L labels=check ver=0.1.0 -->
- [x] **GD-5 — A restart invalidates the baseline**: a counter that went
  backwards, or an uptime that shrank, means no baseline rather than a negative
  rate or a wraparound-sized incident.
  <!-- gd: prio=high size=S labels=check ver=0.1.0 -->
- [x] **GD-6 — Node states**: ready, connected, wsrep_on, desync, read_only and
  the local state comment, each with what it means operationally rather than
  the raw value. <!-- gd: prio=high size=M labels=check ver=0.1.0 -->
- [x] **GD-7 — A standalone server is one finding, not five**: `wsrep_provider`
  unset means "not a cluster member", and the node is excluded from every
  comparison instead of firing size, quorum, ready and wsrep_on at once.
  <!-- gd: prio=high size=S labels=check ver=0.1.0 -->
- [x] **GD-8 — ProxySQL against the cluster**: nodes missing from every serving
  hostgroup, ONLINE-but-not-Synced, shunned-but-Synced, and a hostgroup with
  nothing online. The **offline hostgroup is monitor-managed and never graded**
  — flagging it is a permanent false positive and "cleaning it up" fights the
  monitor. <!-- gd: prio=high size=L labels=proxysql ver=0.1.0 -->
- [x] **GD-9 — Missing primary keys**: the union across nodes, with the reason
  Galera calls them unsupported. <!-- gd: prio=med size=S labels=check ver=0.1.0 -->
- [x] **GD-10 — Config, DSNs and secrets**: JSON config with `${ENV_VAR}`
  expansion (an unset variable is an error), `--node name=DSN` for a
  file-less run, and a redactor so a driver error never carries a password into
  a ticket. <!-- gd: prio=high size=M labels=cli ver=0.1.0 -->
- [x] **GD-11 — Three renderers and exit 0**: text worst-first, `--json`,
  `--findings` (empty array, never `null`); exit 0 whenever the audit ran,
  `--exit-on` to opt into exit 1. <!-- gd: prio=high size=M labels=output ver=0.1.0 -->
- [x] **GD-12 — The SQL, against a real server**: an integration test behind
  `GD_TEST_DSN`, because an information_schema join that is subtly wrong looks
  perfect from a fixture. <!-- gd: prio=high size=S labels=tests ver=0.1.0 -->

## M2 — Deeper into the cluster <!-- ms: target=v0.2.0 phase=later -->

- [x] **GD-13 — Application schema drift**: the same fingerprint comparison for
  the application schemas. Galera *does* replicate that DDL, so a difference
  means a failed or half-applied schema change — a different diagnosis from
  GD-2 and worth its own check.
  <!-- gd: prio=high size=M labels=check ver=0.2.0 -->
- [ ] **GD-14 — Backup freshness**: a dump on disk is not a backup that left the
  building. Check the age of the local dump *and* whether it reached its
  off-site destination, because the first without the second is the failure
  everybody discovers at the worst moment. **Parked**: this needs the
  filesystem and the off-site destination, and this tool only speaks read-only
  SQL to nodes — as written it is a generic backup check and belongs in
  [checkfleet](https://github.com/Allan-Nava/checkfleet). It earns its place
  here only in a form that reads backup metadata out of a table named in the
  config. <!-- gd: prio=high size=M labels=check -->
- [ ] **GD-15 — SST/IST history**: read the recent state transfers from the
  error log or the status counters, so a cluster that quietly full-syncs a node
  every week is visible. **Parked**: MariaDB exposes no state-transfer
  counters, so this needs the error log — outside the SQL channel. GD-52 (a
  node that restarted between runs) is the part of it that *is* reachable.
  <!-- gd: prio=med size=L labels=check -->
- [x] **GD-16 — Node clock skew**: compare each node's clock with the auditing
  host. Certification and log correlation both suffer, and it is one query.
  <!-- gd: prio=med size=S labels=check ver=0.5.0 -->
- [x] **GD-17 — Cross-DC latency from the cluster's own numbers**: segment
  configuration versus the observed apply and send queues, to say whether a
  node is slow or simply far away. <!-- gd: prio=med size=L labels=check ver=0.8.0 -->
- [ ] **GD-18 — Watch mode**: re-audit on an interval and print only the
  transitions, for the window in which a cluster is being repaired.
  <!-- gd: prio=low size=M labels=cli -->

## M3 — Fit the toolchain <!-- ms: target=v0.3.0 phase=later -->

- [ ] **GD-19 — checkfleet module**: emit the same findings under a `galera`
  module in [checkfleet](https://github.com/Allan-Nava/checkfleet), so a fleet
  already described there gains the check without a second inventory. **Parked
  here**: the code lives in that repository, and what this one owes it is the
  stable `--findings` array it already emits.
  <!-- gd: prio=high size=M labels=integration -->
- [x] **GD-20 — Release pipeline**: tag-driven archives for six platforms with
  `SHA256SUMS`, an attestation, the `ghcr.io` image and notes lifted from the
  CHANGELOG. <!-- gd: prio=high size=M labels=release ver=0.3.0 -->
- [x] **GD-37 — Homebrew, from the release's own checksums**: `Formula/` in this
  repository is the tap, and `scripts/brew.sh` renders the formula from the
  `SHA256SUMS` the release workflow computed over the bytes it uploaded — a
  checksum somebody retyped is a formula that fails on the one machine that
  matters. <!-- gd: prio=med size=S labels=delivery ver=0.3.0 -->
- [x] **GD-54 — Nothing ever installed the formula**: the release generated,
  committed and published a Homebrew formula that no job had ever run
  `brew install` against. A macOS job now does — `brew style`, `brew install`,
  `brew test`, and the installed binary has to report the released version —
  rendering the formula from the published release rather than from the
  committed file, because that is what a user gets. Plus a scheduled workflow
  that installs from the tap the way the docs say to, since a release asset can
  be deleted long after the run that made it went green.
  <!-- gd: prio=high size=S labels=release,tests ver=0.9.1 -->
- [x] **GD-55 — goreleaser and a cask, like the sibling tools**: the
  hand-rolled `scripts/release.sh build` and `scripts/brew.sh` are replaced by
  `.goreleaser.yaml` — archives, `SHA256SUMS`, SBOMs, keyless cosign
  signatures, the multi-arch `ghcr.io` image, and a Homebrew **cask** pushed to
  `Allan-Nava/homebrew-tap` with the quarantine hook every unsigned binary
  needs. One release shape across the tools, and `brew install --cask
  Allan-Nava/tap/galera-doctor` alongside its siblings.
  <!-- gd: prio=high size=M labels=release,delivery ver=0.10.0 -->
- [x] **GD-21 — Docs site**: `docs/` published with the same POSIX-sh generator
  the sibling tools use, with a dead-link gate in CI.
  <!-- gd: prio=med size=M labels=docs ver=0.9.0 -->
- [ ] **GD-22 — Percona XtraDB Cluster and MySQL Group Replication**: the first
  is nearly free, the second is a different model and needs its own checks
  rather than a rename. <!-- gd: prio=low size=XL labels=check -->
- [x] **GD-23 — Identity and landing page**: a logo in `assets/`, `INTENT.md`
  as the charter (why the tool exists, what it will never become) and a
  dependency-free GitHub Pages landing page in `site/`, deployed by its own
  workflow. <!-- gd: prio=med size=S labels=docs,project ver=0.1.1 -->
- [x] **GD-24 — The project's own metadata is generated too**: the GitHub
  description, website and topics live in `.github/repo.env` and are written by
  `scripts/repo.sh`; `scripts/links.sh` checks every local link in the docs and
  the site; `scripts/site.sh` keeps the page's logo a copy of `assets/`. Each
  has a CI gate, so drift fails the build rather than sitting on the repository
  front page. <!-- gd: prio=med size=S labels=project,docs ver=0.1.1 -->

## M4 — What the next restart costs <!-- ms: target=v0.2.0 phase=shipped -->

Every item here is a state that is free today and expensive at the next
restart, failover or partition: the cluster is Synced, every counter is green,
and the configuration or the schema has already decided what will happen when
something moves. That is the same shape as GD-2 — invisible until it is not —
which is why these belong together rather than one per release.

- [x] **GD-36 — One label vocabulary**: the linter's label list and the list
  `issues --apply` creates on GitHub were two copies that had already drifted —
  the second still carried `parser` from a sibling tool and knew neither
  `collect` nor `proxysql`. One list, two consumers, `scripts/backlog.sh
  labels` to inspect or create it, and a test that walks it in both directions.
  <!-- gd: prio=med size=S labels=project,tests ver=0.2.1 -->
- [x] **GD-44 — The page has to be findable**: canonical URL, Open Graph and
  Twitter card tags, a 1200x630 preview card rendered from
  `assets/og-image.html`, schema.org JSON-LD, `robots.txt` and a generated
  `sitemap.xml` — with `scripts/seo_test.sh` as the gate, because every one of
  these fails silently. Somebody searching for the symptom rather than for this
  tool has to land somewhere.
  <!-- gd: prio=med size=S labels=docs,tests ver=0.5.1 -->
- [x] **GD-45 — The release that published no image**: every tag from v0.3.0 to
  v0.5.1 shipped its archives and failed to push a container image —
  `github.repository_owner` is spelled `Allan-Nava` and a registry refuses a
  capital letter — while the Homebrew formula was written and never committed,
  because `git diff` does not see a file that is not tracked yet. Both are now
  gated, and publishing is repeatable so a half-finished tag can be re-run.
  <!-- gd: prio=high size=S labels=release,tests ver=0.5.2 -->
- [x] **GD-25 — SST readiness**: `wsrep_sst_method` compared across nodes, and
  `wsrep_sst_donor` checked against the names the cluster actually has. A node
  whose method differs from its peers', or whose donor list names a server that
  was decommissioned in March, is a node that cannot rejoin — and nothing says
  so until it tries. <!-- gd: prio=high size=M labels=check ver=0.2.0 -->
- [x] **GD-26 — A split brain that is already configured**: `pc.ignore_sb`,
  `pc.bootstrap` and `pc.weight` read out of `wsrep_provider_options` per node.
  A node left with `pc.ignore_sb=true` after somebody recovered a cluster by
  hand will keep serving writes on the wrong side of the next partition, and
  the weights decide which side that is. Reported as the arithmetic it implies,
  not as a variable dump. <!-- gd: prio=high size=M labels=check ver=0.2.0 -->
- [x] **GD-27 — Causal reads that are not**: `wsrep_sync_wait` compared across
  nodes. When one node has it and another does not, the same query returns
  fresh or stale data depending on which node the proxy picked — a bug that
  arrives as "sometimes the row is not there yet" and appears in no metric on
  either node. <!-- gd: prio=high size=S labels=check ver=0.2.0 -->
- [x] **GD-28 — Auto-increment collision on failover**: `auto_increment_offset`,
  `auto_increment_increment` and `wsrep_auto_increment_control` per node. Two
  nodes sharing an offset generate the same ids the moment writes reach both,
  and the damage is duplicate keys in application data rather than anything
  replication reports. <!-- gd: prio=high size=S labels=check ver=0.2.0 -->
- [x] **GD-29 — Tables Galera does not replicate at all**: application tables on
  a storage engine outside InnoDB — MyISAM and Aria unless
  `wsrep_replicate_myisam` says otherwise. The writes succeed, the counters stay
  green and the rows exist on one node. Sibling of `schema/no-pk` and a
  different diagnosis. <!-- gd: prio=high size=M labels=check ver=0.2.0 -->

## M5 — The cluster you cannot see from one node <!-- ms: target=v0.3.0 phase=shipped -->

M4 shipped the states that cost you at the next restart. This one continues the
same line into the settings that are *per node* while everybody talks about
them as if they were properties of the cluster: durability, DDL method,
write-set limits, the segment map. Each of them is uniform in every diagram and
different on one server, and the difference only surfaces when the cluster is
asked to behave as one thing.

- [x] **GD-30 — Write-set limits that disagree**: `wsrep_max_ws_size` and
  `wsrep_max_ws_rows` across nodes. A transaction that certifies on the node
  that accepted it and is refused by an applier with a smaller limit takes that
  applier out of the cluster, which reads as a node failure rather than as the
  configuration difference it is. <!-- gd: prio=med size=S labels=check ver=0.5.0 -->
- [x] **GD-31 — The segment map against the topology**: `gmcast.segment` per
  node next to the addresses. Two nodes in the same datacentre in different
  segments, or three datacentres all in segment 0, is a WAN bill and a slow
  cluster that replicates perfectly. Complements GD-17, which measures the
  latency rather than the intent. <!-- gd: prio=med size=M labels=check ver=0.5.0 -->
- [x] **GD-32 — What changed since the last run**: the state file already holds
  the previous run's findings; print the transitions — appeared, cleared, got
  worse — for the person who ran the audit twenty minutes ago and needs to know
  whether the thing they did helped. Not a history, and not a daemon: one diff
  against one file. <!-- gd: prio=med size=M labels=output ver=0.7.0 -->
- [x] **GD-46 — The baseline that was never found**: the state file namespaces
  nodes by cluster (`compress/sg-01`) and the audit asks about bare node names,
  so every lookup missed and every counter check reported *not graded: no
  baseline* forever — indistinguishable from a cluster with nothing to grade.
  `State.Scope` and `State.Merge` are now the two sides of that boundary, with
  tests on both. <!-- gd: prio=high size=S labels=collect,tests ver=0.7.0 -->
- [x] **GD-33 — A restart that throws the gcache away**: `gcache.recover` per
  node. With it off, a clean restart loses the write-set cache and the node
  needs a full SST for a two-minute maintenance window — the gcache window
  check (`gcache/window`) measures a buffer that this setting quietly discards.
  <!-- gd: prio=high size=S labels=check ver=0.4.0 -->
- [x] **GD-34 — The DDL method that explains GD-13**: `wsrep_OSU_method` per
  node. A node left on RSU applies schema changes locally and does not
  replicate them, which is precisely how the application schema drift that
  `schema/drift` reports comes to exist. Reporting the cause next to the
  symptom is the difference between a finding and a diagnosis.
  <!-- gd: prio=high size=S labels=check ver=0.4.0 -->
- [x] **GD-35 — Durability that is not the cluster's**:
  `innodb_flush_log_at_trx_commit` and `sync_binlog` across nodes. A cluster's
  durability is the weakest node's, not the average: one node set to flush
  once a second turns "committed on three nodes" into "committed on two and
  probably a third" the moment the power goes. Nothing reports it because each
  node is doing exactly what it was told.
  <!-- gd: prio=med size=S labels=check ver=0.5.0 -->

## M6 — Configured, and not running <!-- ms: target=v0.6.0 phase=shipped -->

Everything shipped so far compares nodes with each other. This milestone
compares what a node is *configured to believe* with what is actually there:
the peer list against the membership, the applier settings against the queues,
the proxy's monitor against the proxy's own tables. A configuration that
describes a cluster which no longer exists is the quietest failure in the set —
it costs nothing until the process restarts and looks for the cluster it was
told about.

- [x] **GD-38 — The peer list against the membership**: `wsrep_cluster_address`
  per node, resolved against the nodes actually in the component. A node whose
  list names two servers that were decommissioned, or that does not name the
  node that is currently the only other member, starts fine today and cannot
  find the cluster after a restart. <!-- gd: prio=high size=M labels=check ver=0.6.0 -->
- [x] **GD-39 — Flow control that one node decides for everybody**:
  `gcs.fc_limit`, `gcs.fc_factor` and `gcs.fc_master_slave` per node. The
  cluster throttles when the slowest queue hits its own limit, so a node
  configured with a smaller one paces every writer in the cluster — and
  `flow/paused` reports the symptom without the reason.
  <!-- gd: prio=high size=S labels=check ver=0.6.0 -->
- [x] **GD-40 — Appliers that are not the same size**: `wsrep_slave_threads`
  per node, reported next to that node's receive queue. A node with a quarter
  of its peers' apply threads is slower by configuration rather than by load,
  which is a different fix from "look at its disk".
  <!-- gd: prio=med size=S labels=check ver=0.6.0 -->
- [x] **GD-41 — What a rejoin will actually copy**: the dataset size from
  `information_schema.TABLES` next to the gcache window and the SST method, so
  "this node needs a full SST" comes with the number of gigabytes that implies
  and the donor it will take out of service. <!-- gd: prio=med size=M labels=check ver=0.6.0 -->
- [x] **GD-42 — A proxy whose monitor stopped**: ProxySQL's Galera monitor
  writes the hostgroups this tool already compares. When the monitor is
  disabled or its checks are failing, those hostgroups are a photograph: every
  proxysql/* finding agrees with the cluster and none of it is live.
  <!-- gd: prio=high size=M labels=proxysql ver=0.6.0 -->
- [x] **GD-43 — What this run could not audit**: one finding summarising the
  checks that did not run and why — a missing grant, a metric this build does
  not have, a node that could not be read. A cron job needs one line to know
  whether "no findings" meant "nothing is wrong".
  <!-- gd: prio=med size=S labels=output ver=0.6.0 -->

## M7 — Every write path, drawn or not <!-- ms: target=v1.0.0 phase=next -->

Everything shipped so far treats the cluster as the only thing writing to
itself. Real deployments are rarely that tidy: a node is also an async replica,
another one feeds a reporting replica downstream, a trigger runs on one node
and not on its peers, and somebody believes writes only go to one member. None
of it appears in a cluster diagram and none of it appears in `wsrep_*` — the
cluster cannot see a write path it is not part of.

- [ ] **GD-47 — Async replication attached to the cluster**: `SHOW REPLICA
  STATUS` per node, and whether any node is a source for something outside.
  A member that is also an async replica is a second write path into the
  cluster; a member that feeds a downstream replica is a dependency nobody
  else in the cluster knows about, and the next SST rebuilds its binlogs out
  from under it. <!-- gd: prio=high size=M labels=collect,check -->
- [ ] **GD-48 — GTID domains that do not agree**: `gtid_domain_id`,
  `server_id` and `gtid_strict_mode` across nodes. When anything replicates out
  of a Galera cluster, the nodes have to agree about the domain or a failover
  silently rewrites history for every downstream replica — and nothing in the
  cluster is affected, which is why nothing reports it.
  <!-- gd: prio=high size=S labels=check -->
- [ ] **GD-50 — Triggers that run on one node only**:
  `wsrep_slave_run_triggers` per node. A trigger that fires on the writer and
  not on the appliers writes rows on one node and not the others — divergence
  produced by design, certified by nothing, invisible to every counter.
  <!-- gd: prio=high size=S labels=check -->
- [ ] **GD-49 — Who is actually writing**: `wsrep_replicated` per node over the
  interval (needs `--state`). "We only write to one node" is a belief, and the
  cluster has the numbers to confirm or refute it — a second writer nobody
  meant to have is the cause behind half the certification failures this tool
  already reports. <!-- gd: prio=med size=M labels=check -->
- [ ] **GD-51 — The binary log, per node**: `log_bin`, `binlog_format` and
  `log_slave_updates` compared. Galera does not need the binlog and everything
  around a cluster does: a node with it off is a node no backup and no
  downstream replica can be taken from, and finding that out during a failover
  is finding it out too late. <!-- gd: prio=med size=S labels=check -->
- [ ] **GD-52 — A node that restarted between runs**: `wsrep_gcomm_uuid`
  compared with the previous run (needs `--state`). A restart resets every
  counter, which is exactly why the rate checks fall back to *no baseline* —
  and this is the check that says the restart happened rather than leaving it
  as an unexplained gap. <!-- gd: prio=med size=S labels=check -->
- [ ] **GD-53 — The membership as the cluster reports it**:
  `information_schema.WSREP_MEMBERSHIP` where the `wsrep_info` plugin is
  installed, compared with what each node claims about itself. Two independent
  views of the same membership, and a node that believes it is a member while
  the group has not listed it is a state no single node can report.
  <!-- gd: prio=low size=M labels=collect,check -->
