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

- [ ] **GD-13 — Application schema drift**: the same fingerprint comparison for
  the application schemas. Galera *does* replicate that DDL, so a difference
  means a failed or half-applied schema change — a different diagnosis from
  GD-2 and worth its own check.
  <!-- gd: prio=high size=M labels=check -->
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
- [ ] **GD-20 — Release pipeline**: tag-driven archives for six platforms with
  `SHA256SUMS`, an attestation, the `ghcr.io` image and notes lifted from the
  CHANGELOG. <!-- gd: prio=high size=M labels=release -->
- [ ] **GD-21 — Docs site**: `docs/` published with the same POSIX-sh generator
  the sibling tools use, with a dead-link gate in CI.
  <!-- gd: prio=med size=M labels=docs -->
- [ ] **GD-22 — Percona XtraDB Cluster and MySQL Group Replication**: the first
  is nearly free, the second is a different model and needs its own checks
  rather than a rename. <!-- gd: prio=low size=XL labels=check -->
