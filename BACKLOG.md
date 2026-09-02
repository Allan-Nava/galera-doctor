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

## M2 — Deeper into the cluster <!-- ms: target=v0.2.0 phase=now -->

- [x] **GD-13 — Application schema drift**: the same fingerprint comparison for
  the application schemas. Galera *does* replicate that DDL, so a difference
  means a failed or half-applied schema change — a different diagnosis from
  GD-2 and worth its own check.
  <!-- gd: prio=high size=M labels=check ver=0.2.0 -->
- [ ] **GD-14 — Backup freshness**: a dump on disk is not a backup that left the
  building. Check the age of the local dump *and* whether it reached its
  off-site destination, because the first without the second is the failure
  everybody discovers at the worst moment.
  <!-- gd: prio=high size=M labels=check -->
- [ ] **GD-15 — SST/IST history**: read the recent state transfers from the
  error log or the status counters, so a cluster that quietly full-syncs a node
  every week is visible. <!-- gd: prio=med size=L labels=check -->
- [ ] **GD-16 — Node clock skew**: compare each node's clock with the auditing
  host. Certification and log correlation both suffer, and it is one query.
  <!-- gd: prio=med size=S labels=check -->
- [ ] **GD-17 — Cross-DC latency from the cluster's own numbers**: segment
  configuration versus the observed apply and send queues, to say whether a
  node is slow or simply far away. <!-- gd: prio=med size=L labels=check -->
- [ ] **GD-18 — Watch mode**: re-audit on an interval and print only the
  transitions, for the window in which a cluster is being repaired.
  <!-- gd: prio=low size=M labels=cli -->

## M3 — Fit the toolchain <!-- ms: target=v0.3.0 phase=next -->

- [ ] **GD-19 — checkfleet module**: emit the same findings under a `galera`
  module in [checkfleet](https://github.com/Allan-Nava/checkfleet), so a fleet
  already described there gains the check without a second inventory.
  <!-- gd: prio=high size=M labels=integration -->
- [x] **GD-20 — Release pipeline**: tag-driven archives for six platforms with
  `SHA256SUMS`, an attestation, the `ghcr.io` image and notes lifted from the
  CHANGELOG. <!-- gd: prio=high size=M labels=release ver=0.3.0 -->
- [x] **GD-37 — Homebrew, from the release's own checksums**: `Formula/` in this
  repository is the tap, and `scripts/brew.sh` renders the formula from the
  `SHA256SUMS` the release workflow computed over the bytes it uploaded — a
  checksum somebody retyped is a formula that fails on the one machine that
  matters. <!-- gd: prio=med size=S labels=delivery ver=0.3.0 -->
- [ ] **GD-21 — Docs site**: `docs/` published with the same POSIX-sh generator
  the sibling tools use, with a dead-link gate in CI.
  <!-- gd: prio=med size=M labels=docs -->
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

## M5 — The cluster you cannot see from one node <!-- ms: target=v0.3.0 phase=next -->

M4 shipped the states that cost you at the next restart. This one continues the
same line into the settings that are *per node* while everybody talks about
them as if they were properties of the cluster: durability, DDL method,
write-set limits, the segment map. Each of them is uniform in every diagram and
different on one server, and the difference only surfaces when the cluster is
asked to behave as one thing.

- [ ] **GD-30 — Write-set limits that disagree**: `wsrep_max_ws_size` and
  `wsrep_max_ws_rows` across nodes. A transaction that certifies on the node
  that accepted it and is refused by an applier with a smaller limit takes that
  applier out of the cluster, which reads as a node failure rather than as the
  configuration difference it is. <!-- gd: prio=med size=S labels=check -->
- [ ] **GD-31 — The segment map against the topology**: `gmcast.segment` per
  node next to the addresses. Two nodes in the same datacentre in different
  segments, or three datacentres all in segment 0, is a WAN bill and a slow
  cluster that replicates perfectly. Complements GD-17, which measures the
  latency rather than the intent. <!-- gd: prio=med size=M labels=check -->
- [ ] **GD-32 — What changed since the last run**: the state file already holds
  the previous run's findings; print the transitions — appeared, cleared, got
  worse — for the person who ran the audit twenty minutes ago and needs to know
  whether the thing they did helped. Not a history, and not a daemon: one diff
  against one file. <!-- gd: prio=med size=M labels=output -->
- [ ] **GD-33 — A restart that throws the gcache away**: `gcache.recover` per
  node. With it off, a clean restart loses the write-set cache and the node
  needs a full SST for a two-minute maintenance window — the gcache window
  check (`gcache/window`) measures a buffer that this setting quietly discards.
  <!-- gd: prio=high size=S labels=check -->
- [ ] **GD-34 — The DDL method that explains GD-13**: `wsrep_OSU_method` per
  node. A node left on RSU applies schema changes locally and does not
  replicate them, which is precisely how the application schema drift that
  `schema/drift` reports comes to exist. Reporting the cause next to the
  symptom is the difference between a finding and a diagnosis.
  <!-- gd: prio=high size=S labels=check -->
- [ ] **GD-35 — Durability that is not the cluster's**:
  `innodb_flush_log_at_trx_commit` and `sync_binlog` across nodes. A cluster's
  durability is the weakest node's, not the average: one node set to flush
  once a second turns "committed on three nodes" into "committed on two and
  probably a third" the moment the power goes. Nothing reports it because each
  node is doing exactly what it was told.
  <!-- gd: prio=med size=S labels=check -->
