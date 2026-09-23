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

## M2 — Deeper into the cluster <!-- ms: target=v0.2.0 phase=shipped -->

- [x] **GD-13 — Application schema drift**: the same fingerprint comparison for
  the application schemas. Galera *does* replicate that DDL, so a difference
  means a failed or half-applied schema change — a different diagnosis from
  GD-2 and worth its own check.
  <!-- gd: prio=high size=M labels=check ver=0.2.0 -->
- [x] **GD-16 — Node clock skew**: compare each node's clock with the auditing
  host. Certification and log correlation both suffer, and it is one query.
  <!-- gd: prio=med size=S labels=check ver=0.5.0 -->
- [x] **GD-17 — Cross-DC latency from the cluster's own numbers**: segment
  configuration versus the observed apply and send queues, to say whether a
  node is slow or simply far away. <!-- gd: prio=med size=L labels=check ver=0.8.0 -->
- [x] **GD-18 — Watch mode**: re-audit on an interval and print only the
  transitions, for the window in which a cluster is being repaired.
  <!-- gd: prio=low size=M labels=cli ver=0.12.0 -->

## M3 — Fit the toolchain <!-- ms: target=v0.3.0 phase=shipped -->

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
- [x] **GD-56 — The release notes made the tree dirty**: goreleaser refuses to
  release from a dirty working tree, and the notes file was written into the
  repository root — so v0.10.0 failed after 0s, before creating anything, with
  an error that reads like a goreleaser problem. The notes go to the runner
  temp directory now, with the premise itself asserted in the test.
  <!-- gd: prio=high size=S labels=release,tests ver=0.10.1 -->
- [x] **GD-21 — Docs site**: `docs/` published with the same POSIX-sh generator
  the sibling tools use, with a dead-link gate in CI.
  <!-- gd: prio=med size=M labels=docs ver=0.9.0 -->
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

## M7 — Every write path, drawn or not <!-- ms: target=v1.0.0 phase=shipped -->

Everything shipped so far treats the cluster as the only thing writing to
itself. Real deployments are rarely that tidy: a node is also an async replica,
another one feeds a reporting replica downstream, a trigger runs on one node
and not on its peers, and somebody believes writes only go to one member. None
of it appears in a cluster diagram and none of it appears in `wsrep_*` — the
cluster cannot see a write path it is not part of.

- [x] **GD-47 — Async replication attached to the cluster**: `SHOW REPLICA
  STATUS` per node, and whether any node is a source for something outside.
  A member that is also an async replica is a second write path into the
  cluster; a member that feeds a downstream replica is a dependency nobody
  else in the cluster knows about, and the next SST rebuilds its binlogs out
  from under it. <!-- gd: prio=high size=M labels=collect,check ver=0.11.0 -->
- [x] **GD-48 — GTID domains that do not agree**: `gtid_domain_id`,
  `server_id` and `gtid_strict_mode` across nodes. When anything replicates out
  of a Galera cluster, the nodes have to agree about the domain or a failover
  silently rewrites history for every downstream replica — and nothing in the
  cluster is affected, which is why nothing reports it.
  <!-- gd: prio=high size=S labels=check ver=0.11.0 -->
- [x] **GD-50 — Triggers that run on one node only**:
  `wsrep_slave_run_triggers` per node. A trigger that fires on the writer and
  not on the appliers writes rows on one node and not the others — divergence
  produced by design, certified by nothing, invisible to every counter.
  <!-- gd: prio=high size=S labels=check ver=0.11.0 -->
- [x] **GD-49 — Who is actually writing**: `wsrep_replicated` per node over the
  interval (needs `--state`). "We only write to one node" is a belief, and the
  cluster has the numbers to confirm or refute it — a second writer nobody
  meant to have is the cause behind half the certification failures this tool
  already reports. <!-- gd: prio=med size=M labels=check ver=1.0.0 -->
- [x] **GD-51 — The binary log, per node**: `log_bin`, `binlog_format` and
  `log_slave_updates` compared. Galera does not need the binlog and everything
  around a cluster does: a node with it off is a node no backup and no
  downstream replica can be taken from, and finding that out during a failover
  is finding it out too late. <!-- gd: prio=med size=S labels=check ver=1.0.0 -->
- [x] **GD-52 — A node that restarted between runs**: `wsrep_gcomm_uuid`
  compared with the previous run (needs `--state`). A restart resets every
  counter, which is exactly why the rate checks fall back to *no baseline* —
  and this is the check that says the restart happened rather than leaving it
  as an unexplained gap. <!-- gd: prio=med size=S labels=check ver=1.0.0 -->
- [x] **GD-53 — The membership as the cluster reports it**:
  `information_schema.WSREP_MEMBERSHIP` where the `wsrep_info` plugin is
  installed, compared with what each node claims about itself. Two independent
  views of the same membership, and a node that believes it is a member while
  the group has not listed it is a state no single node can report.
  <!-- gd: prio=low size=M labels=collect,check ver=1.0.0 -->

## M8 — Parked, and why <!-- ms: target=v2.0.0 phase=later -->

Not a queue. These are items that were written down before it was clear they
cannot be done through the channel this tool has — read-only SQL to the nodes
and to a ProxySQL admin interface — and they are kept here, with the reason,
because an item that quietly disappears is an item somebody proposes again in
six months. Each one names the form in which it *would* earn its place.

- [x] **GD-14 — Backup freshness, declared not guessed**: the config carries a
  `backup` block — a `SELECT` the operator writes against the table their
  backups already record into, plus two thresholds — and `backup/freshness`
  grades its age against the answering server's clock. Both halves of the
  original item fit in the `WHERE`: "finished" and "reached the off-site
  destination" are the same query. The schema stays theirs, and the read-only
  gate is enforced at load as well as at run time.
  <!-- gd: prio=high size=M labels=check,cli ver=1.2.0 -->
- [ ] **GD-15 — SST/IST history**: read the recent state transfers from the
  error log or the status counters, so a cluster that quietly full-syncs a node
  every week is visible. **Parked**: MariaDB exposes no state-transfer
  counters, so this needs the error log — outside the SQL channel. GD-52 (a
  node that restarted between runs) is the part of it that *is* reachable.
  <!-- gd: prio=med size=L labels=check -->
- [ ] **GD-19 — checkfleet module**: emit the same findings under a `galera`
  module in [checkfleet](https://github.com/Allan-Nava/checkfleet), so a fleet
  already described there gains the check without a second inventory. **Parked
  here**: the code lives in that repository, and what this one owes it is the
  stable `--findings` array it already emits.
  <!-- gd: prio=high size=M labels=integration -->
- [x] **GD-58 — The findings contract, frozen**: the half of GD-19 that lives
  in this repository. `--findings` is what another tool consumes, so the field
  names are pinned by a test that compares the whole document, and
  [docs/findings.md](docs/findings.md) states the promises — array never
  `null`, worst first, numbers in `value` rather than in prose, no DSN in any
  field — with the rule that breaking them is a major release.
  <!-- gd: prio=high size=S labels=output,docs ver=1.1.1 -->

## M9 — Beyond Galera <!-- ms: target=v2.0.0 phase=later -->

Not a leftover from M3, which was about this repository's own toolchain: this
is a different replication model, and pointing the existing checks at it would
be a rename rather than support. It is written down as the milestone it would
have to be.

- [x] **GD-22 — Percona XtraDB Cluster**: the same provider under another name,
  so every check applies as it stands — verified against a real PXC 8.0 node —
  plus `pxc/strict-mode`, the guard rail it adds and the one thing that needed a
  check of its own. <!-- gd: prio=low size=S labels=check ver=1.1.0 -->
- [ ] **GD-57 — MySQL Group Replication**: a different replication model, not a
  rename. Its own membership, its own certification, its own failure modes:
  pointing these checks at it would produce confident findings about variables
  that do not mean the same thing. It needs its own set, which is what this
  milestone is for. <!-- gd: prio=low size=XL labels=check -->

## M10 — Divergence that has not happened yet <!-- ms: target=v1.3.0 phase=shipped -->

Everything shipped so far reports a state the cluster is already in. These are
the ones where it is not yet: a setting that will produce different rows the
next time somebody runs a DDL, a transaction the certifier is going to abort, a
node the group is already suspicious of. Each of them is a divergence with a
date in the future, and none of them appears in a counter — because from
replication's point of view nothing has gone wrong at all.

- [x] **GD-59 — `sql_mode` per node**: the setting that decides what a write
  *means*. A DDL run on a node with `NO_ZERO_DATE` off produces a different
  table from the same statement run on its peers, and an application talking to
  the wrong node gets a truncation where it expected an error. Galera
  replicates the result, not the interpretation, so nothing here is ever a
  conflict. <!-- gd: prio=high size=S labels=check ver=1.3.0 -->
- [x] **GD-60 — Character set and collation per node**:
  `character_set_server` and `collation_server` differing means a `CREATE TABLE`
  without an explicit charset is a different table depending on where it ran —
  which is how `schema/drift` findings come to exist, so this is the cause next
  to the symptom, the way `repl/osu-method` is for the other kind.
  <!-- gd: prio=high size=S labels=check ver=1.3.0 -->
- [x] **GD-61 — Time zone, per node**: `time_zone` differing, and whether the
  zone tables are loaded at all. A `NOW()` in a trigger or a default, a
  `CONVERT_TZ` in a view: same statement, different stored value, no conflict
  and no counter. <!-- gd: prio=med size=S labels=check ver=1.3.0 -->
- [x] **GD-62 — Cascading foreign keys**: parent-child cascades are certified
  on the parent rows only, which is the documented weak spot of Galera's
  certification. Report the cascading constraints in the application schemas —
  the ones that turn one certified write into rows nobody certified.
  <!-- gd: prio=med size=M labels=check,collect ver=1.3.0 -->
- [x] **GD-63 — The transaction that is going to lose**:
  `information_schema.INNODB_TRX` for transactions older than a threshold. On a
  standalone server that is a slow query; in a cluster it is the next
  brute-force abort, and it is what makes a rolling schema change hang, because
  TOI waits for it cluster-wide. Different diagnosis, same row.
  <!-- gd: prio=high size=M labels=check,collect ver=1.3.0 -->
- [x] **GD-64 — The node the group already distrusts**: `wsrep_evs_delayed`
  and the eviction list. The group communication layer keeps its own opinion
  about which members are flaky, and with `evs.auto_evict` set it acts on it —
  so a node named there is one the cluster is about to remove while every
  membership check still reads Primary and Synced.
  <!-- gd: prio=high size=S labels=check ver=1.3.0 -->
- [x] **GD-65 — A state transfer in flight**: `wsrep_ist_receive_status` and
  the donor's side of it. `node/state` says Donor/Desynced and Joined; this
  says how far along it is and therefore whether waiting is the right thing to
  do. <!-- gd: prio=low size=S labels=check ver=1.3.0 -->
- [x] **GD-66 — The read-only gate fired on prose**: the CI grep looked for a
  writing verb anywhere inside a string, so `"ON UPDATE "+onUpdate` and a hint
  quoting `CREATE TABLE` — both added in v1.3.0 — turned the build red. A
  statement *begins* with its verb, which is what `scripts/readonly.sh` matches
  now, and it has `scripts/readonly_test.sh` behind it in both directions
  because a gate that fires on prose is a gate somebody loosens in a hurry.
  <!-- gd: prio=high size=S labels=tests,project ver=1.3.1 -->

## M11 — What the next restart erases <!-- ms: target=v1.4.0 phase=next -->

M10 was about a divergence with a date in the future. This one is about a
cluster that is correct right now and will not be after the next `systemctl
restart` — because what makes it correct was typed at a prompt and never
written to a file. A `SET GLOBAL` that fixed an incident, a plugin installed by
hand, a `wsrep_cluster_address` that still lists the node that was
decommissioned in March. Nothing here is a wrong value, so no check that grades
values can see it: the finding is the *provenance* of the value, and provenance
is the one thing the running server knows and nobody writes down.

- [ ] **GD-67 — The setting that only exists in memory**:
  `performance_schema.variables_info` says where each variable's current value
  came from — `COMMAND_LINE`, a config file, or `DYNAMIC`, meaning somebody set
  it at run time. A `DYNAMIC` source on a variable that matters to replication
  is a fix that dies at the next restart. Available on MySQL and PXC 8.0 and
  absent on MariaDB, so on MariaDB the check reports *not available* rather
  than *clean* — a build without the table is not a server without the
  problem. <!-- gd: prio=high size=M labels=collect,check -->
- [ ] **GD-68 — `wsrep_cluster_address` against the group**: the list a node
  reads on start-up, compared with the members it is actually talking to. A
  node missing from the address list rejoins nothing after a full cluster stop,
  and an address that resolves to a decommissioned host is a start-up delay
  nobody expects. The running cluster is the evidence that the list is stale,
  and the running cluster is what the check has.
  <!-- gd: prio=high size=M labels=check -->
- [ ] **GD-69 — The provider option one node carries alone**:
  `wsrep_provider_options` compared across nodes, minus the keys already graded
  by `flow/settings` and `cluster/segments`. The provider takes some of these at
  run time and reads all of them at start-up, so a node with `evs.` or `gcs.`
  tuning its peers do not have is behaving differently now *and* differently
  again once it comes back. <!-- gd: prio=med size=M labels=check -->
- [ ] **GD-70 — The plugin nobody put in the config**:
  `information_schema.PLUGINS` with `LOAD_OPTION`, so a plugin that was
  `INSTALL PLUGIN`-ed live — `wsrep_info`, an audit plugin, a password
  validation plugin — is separated from one the config loads. The audit's own
  `cluster/membership-view` depends on `wsrep_info` being there, which makes
  this the check that says why a previously working check went quiet.
  <!-- gd: prio=med size=S labels=collect,check -->

## M12 — The account that exists on one node <!-- ms: target=v1.5.0 phase=next -->

`systables/drift` compares the *definitions* of the `mysql` tables. Their
**rows** are the other half, and the privilege tables are the half that locks
people out: `CREATE USER` and `GRANT` replicate, but a statement run with
`wsrep_on=OFF`, a `mysql_upgrade` on one node, a password rotated by a script
that connected through the proxy and got whichever node was up — none of them
do. The cluster stays Primary and Synced, every counter stays flat, and an
application fails to authenticate on one node in three, which is exactly the
shape of a bug that gets blamed on the proxy for a week. Reading these tables
needs a grant the audit may not have, so a node without it is reported as *not
audited*, never as agreeing.

- [ ] **GD-71 — Accounts, per node**: an order-independent fingerprint of
  `mysql.user`'s rows — account, host, privileges, plugin, TLS and resource
  limits, never the credential itself — compared across nodes, with the
  accounts present here and absent there named individually. The password hash
  is part of what must match and no part of what is printed.
  <!-- gd: prio=high size=L labels=collect,check -->
- [ ] **GD-72 — Grants below the global level**: `mysql.db`, `tables_priv`,
  `columns_priv` and `procs_priv`, the same way. An account that exists
  everywhere and can read the schema on two nodes out of three is the version
  of this that survives a login test.
  <!-- gd: prio=high size=M labels=collect,check -->
- [ ] **GD-73 — Authentication that differs**: the plugin, the expiry and the
  lock state of the same account on different nodes. `unix_socket` on one node
  and `mysql_native_password` on its peers, or an expired password on the one
  node a failover has not sent traffic to yet, is a divergence that only shows
  itself under the load that follows a switch.
  <!-- gd: prio=med size=M labels=check -->
- [ ] **GD-74 — The proxy's users against the cluster's**: the accounts in
  ProxySQL's `mysql_users` must exist, and be usable, on every node the proxy
  can route to. The monitor user is the one that matters most: the proxy
  demoting a node it cannot authenticate against looks exactly like a node that
  is down. Same matching rules as `proxysql/*` already uses, and still no
  finding about the offline hostgroup.
  <!-- gd: prio=high size=M labels=proxysql,check -->

## M13 — The tests that were never red <!-- ms: target=v1.3.2 phase=shipped -->

Every gate this repository has is green, which is the problem: the audit that
found these items ran the whole suite and learned nothing, because the missing
tests are missing rather than failing. The shape repeats — a check whose
healthy fixture walks the code and never plants the condition, a pure function
that only the integration test reaches, a hand-written list with no diff
against what it describes. High statement coverage hid all three, and one of
them had already drifted.

- [x] **GD-75 — The `checks` list is a claim, not a gate**: `cmdChecks` is
  fifty-odd check ids typed by hand, 0% covered, with nothing comparing it to
  the ids `audit.Run` and `proxysql.Audit` actually emit — and it has already
  drifted: `cluster/membership`, `cluster/provider-version` and
  `node/not-galera` fire and are not listed. A test that walks the two sets in
  both directions, the way `backlog.sh labels` does for the label vocabulary,
  so the next check cannot ship undocumented.
  <!-- gd: prio=high size=S labels=cli,tests ver=1.3.2 -->
- [x] **GD-76 — The pure helpers nobody tested**: `cluster.HostOnly` is the
  documented ProxySQL matching trap — port and CIDR suffix — and has no direct
  test; `collect.parseWhen` guesses between five layouts and a fractional epoch
  for `backup/freshness` and is reachable only behind `GD_TEST_DSN`;
  `state.Delta.PerSecond`, `state.Key`, `cluster.ReplLatency`, `Segment`,
  `ColumnRow`, `Names`, `collect.placeholders`, `keyValue`, `errString` and
  `finding.Num` are all 0%. None of them needs a server. `redact` has exactly
  one case for a rule that says a DSN never reaches the output, which is one
  fewer than that rule deserves. <!-- gd: prio=high size=M labels=tests ver=1.3.2 -->
- [x] **GD-77 — Nine checks with only the quiet half**: the "two tests minimum"
  rule is satisfied halfway for `cluster/conf-id`, `cluster/provider-version`,
  `node/connected`, `node/desync`, `node/read-only`, `node/timezone`,
  `queue/recv`, `queue/send` and `repl/cert-failures` — the healthy fixture
  proves they stay silent, and nothing proves they fire, or that they name the
  right node when they do. `nodeState` at 75% and `stateComment` at 67% are
  where that shows. One planted condition each, asserted by check id and by
  target. <!-- gd: prio=high size=M labels=check,tests ver=1.3.2 -->
- [x] **GD-78 — The one script without a test**: `scripts/og.sh` is the only
  `scripts/x.sh` with no `scripts/x_test.sh` and no CI step, so the rule that
  covers the tooling has a hole in exactly the place where a regenerated PNG is
  hardest to eyeball. It needs headless Chrome, which the test does not: a fake
  `chrome` on `PATH` and an `ASSETS_DIR` override cover the argument handling
  and the 1200x630 assertion, the way the `gh` fake covers `backlog.sh issues`.
  <!-- gd: prio=med size=S labels=tests,docs ver=1.3.2 -->

## M14 — The seqno nobody compares <!-- ms: target=v1.6.0 phase=next -->

Every node reports where it is in the replication stream, how far back its
cache still reaches, how much of the workload it manages to apply at once, and
how many local transactions it killed to let a remote one in. Alone each of
these is a number with no scale — nobody knows whether a certification index of
90,000 is large, and no dashboard has a threshold for it. Compared against the
other nodes, or against the same node ten minutes ago, each one answers a
question an operator actually asks: how far behind is that node really, can it
rejoin without a full copy, and why is the application seeing deadlocks nobody
can reproduce.

`SHOW GLOBAL STATUS` is already read whole, so every variable here is in the
snapshot and unused; `wsrep_local_bf_aborts` is already carried between runs by
`state.Counters` with nothing grading it. This milestone is mostly arithmetic
over data the tool already has.

- [ ] **GD-79 — Apply lag, in transactions rather than in queue depth**:
  `wsrep_last_committed` compared across nodes. `queue/recv` answers "how much
  is waiting here", which is empty on a node that is behind because it stopped
  *receiving* — the two failures look opposite and read the same from one node.
  The nodes are collected concurrently, so a small spread is collection skew
  and not lag: grade only what is larger than that, and where `--state` gives a
  write rate, say the spread in seconds as well as in seqnos, because "40,000
  behind" means nothing without the rate that produced it.
  <!-- gd: prio=high size=M labels=check -->
- [ ] **GD-80 — IST or SST, as a fact instead of a forecast**:
  `wsrep_local_cached_downto` is the oldest seqno a node still holds. Against
  each other node's `wsrep_last_committed` it answers the question
  `gcache/window` can only estimate: if this node restarts now, which of its
  peers can still feed it an incremental transfer, and which would have to copy
  the whole dataset. A cluster where no node can donate an IST to any other is
  one restart away from an hour of `sst/size`, and today that is invisible
  until it happens. Absent on a build that does not report the variable, which
  is *not available* rather than clean. <!-- gd: prio=high size=M labels=check -->
- [ ] **GD-81 — The transactions the cluster killed** (`repl/bf-aborts`):
  `wsrep_local_bf_aborts` graded over the interval, with `wsrep_local_replays`
  beside it. A brute-force abort is a local transaction rolled back so a
  writeset from another node could commit: the application sees a deadlock
  error at a point in its code where no deadlock is possible, and nobody
  attributes it to replication. Distinct from `repl/cert-failures`, which is
  this node's own writesets losing certification elsewhere — same cause,
  opposite direction, different fix. The baseline is already recorded;
  `wsrep_local_replays` has to join `state.Counters`, and both need the
  ungraded fallback when there is none. <!-- gd: prio=high size=M labels=check -->
- [ ] **GD-82 — Applier threads that are not applying in parallel**:
  `wsrep_apply_window` is how many writesets the node actually had in flight at
  once, on average. Against `wsrep_slave_threads` it says whether the
  parallelism that was configured is the parallelism that happened — a node
  with sixteen threads and a window of 1.2 is applying serially, and the queue
  that `repl/appliers` reports will not be fixed by adding more.
  <!-- gd: prio=med size=S labels=check -->
- [ ] **GD-83 — The ceiling GD-82 is measured against**:
  `wsrep_cert_deps_distance` is the average gap between writesets that depend
  on each other, and therefore the hard upper bound on useful applier threads.
  Threads above it do nothing; threads far below it leave the workload's own
  parallelism unused. GD-82 says what is happening and this says what was ever
  possible, so the pair produces a number to set rather than a direction to
  move in. <!-- gd: prio=med size=S labels=check -->
- [ ] **GD-84 — The certification index that grew**: `wsrep_cert_index_size`
  is memory outside the buffer pool, and it grows with the certification
  interval — a long transaction, or an applier that fell behind, keeps a longer
  history certifiable. There is no absolute threshold worth shipping, so this
  is graded the way drift is: against the node's peers. One node an order of
  magnitude above the others is certifying against a history the others have
  already discarded, which is the cause of `txn/long-running` seen from the
  other end. <!-- gd: prio=med size=S labels=check -->

## M15 — The number nobody was watching <!-- ms: target=v1.3.4 phase=shipped -->

M13 closed four gates that could not fail. This is the fifth, found while
writing them: CI has printed the total coverage on every run since the first
release and compared it with nothing. A figure that is printed and not
asserted is decoration — the 70.5% that the M13 audit found had been falling
for releases without anybody noticing, and nothing would have failed if it had
gone on falling.

- [x] **GD-85 — A coverage floor per package, not one global number**: a
  single threshold is the wrong shape here. `internal/cluster` is at 23.7%
  *on purpose* — its SQL is the integration test's job, behind `GD_TEST_DSN` —
  while `internal/audit` at 96.6% is where a drop actually means a check went
  untested, and one global figure lets the second rot while the first holds it
  up. `scripts/coverage.sh` carries a floor per package and fails the build
  under it, with `scripts/coverage_test.sh` behind it driving the script off a
  fixture instead of this repository's own numbers. A package with no floor
  declared is a failure and so is a floor for a package that no longer exists,
  because the way this gate would quietly stop covering things is a new
  package nobody added a line for. Floors sit a couple of points under today's
  measurement — close enough to bite, far enough not to flap — and the script
  says when one has been left far behind the real number. Shipped in 1.3.3 and
  fixed in 1.3.4: the first version used `next` in an awk `BEGIN` action,
  which the awk on macOS accepts and the gawk in CI refuses, so the gate
  passed locally and died on the runner. `AWK` now selects the implementation
  and the test runs under every one on the machine.
  <!-- gd: prio=high size=M labels=tests,project ver=1.3.4 -->
