# Changelog

All notable changes to galera-doctor are recorded here. The format is
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the versioning is
[Semantic Versioning](https://semver.org/). Every release is a tagged `vX.Y.Z`
with its own section; `minor` for new checks or flags, `patch` for fixes. Items
reference their `GD-n` id in [BACKLOG.md](BACKLOG.md).

## [0.2.1] - 2026-09-02

### Fixed

- **One label vocabulary** (GD-36) — the label list the backlog linter enforces
  and the list `scripts/backlog.sh issues --apply` creates on GitHub were two
  copies of the same data, and they had drifted: the creating one still carried
  `parser` from a sibling tool and had never heard of `collect` or `proxysql`.
  Since `gh issue create` treats an unknown label as a hard error, the first
  apply of an item labelled `collect` would have failed halfway through
  creating issues. There is now one list with two consumers,
  `scripts/backlog.sh labels` prints it (`--apply` creates what is missing),
  and `backlog_issues_test.sh` walks it in both directions — every creatable
  label must lint, and a label outside the list must still be rejected.

## [0.2.0] - 2026-09-02

### Added

- **`schema/drift`** (GD-13) — the application base tables are fingerprinted
  per node, keyed `schema.table`, and compared. Galera *does* replicate this
  DDL, which makes it a different diagnosis from `systables/drift` rather than
  the same check on another schema: a difference is a change that did not
  finish — a failed `ALTER`, one applied on a single node by hand, one that
  landed while a node was desynced — and the fix is to re-apply it, not to run
  `mysql_upgrade`. The counters stay green throughout, because replication
  carried what it was given.

  A node whose grants do not reach `information_schema` is reported as **not
  audited**, never dropped from the comparison; a cluster with no application
  tables at all is quiet, because "there are none" and "they could not be read"
  are different findings (nil versus empty map in the snapshot). Past five
  drifted tables the finding becomes one line with the count — a node that
  missed a whole schema change drifts on every table at once, and four hundred
  findings would bury the report. Views are excluded: their columns are derived
  from tables already being compared. `--no-schema` skips this read together
  with the primary key one.

- **`schema/engine`** (GD-29) — application base tables on an engine Galera
  does not replicate. The write succeeds, nothing certifies it, no counter
  records that it went nowhere, and the rows exist on the node that took them.
  MariaDB can be told to replicate MyISAM and Aria (`wsrep_mode`,
  `wsrep_replicate_myisam`) and that setting is per node: when the nodes
  disagree about it the finding is `BAD` rather than `WARN`, because then the
  same write lands on some of them and not others.

- **`sst/method`, `sst/donor`, `sst/auth`** (GD-25) — whether the next node to
  restart can rejoin. Nodes disagreeing about `wsrep_sst_method` (the joiner
  asks, the donor serves); a `wsrep_sst_donor` naming a server this cluster
  does not have under any spelling it answers to — `BAD` when the list has no
  trailing comma, because then it is the only donor allowed and the node
  refuses to start, `WARN` when it falls back; and an empty `wsrep_sst_auth`
  with a method that logs in to the donor, reported as a warning that names
  the `[sst]` config section it may be hiding in rather than as a verdict.

- **`quorum/ignore-sb`, `quorum/bootstrap`, `quorum/weight`** (GD-26) — a split
  brain that is already configured. `pc.ignore_sb` left on after a manual
  recovery keeps a node writable in a non-Primary component; `pc.bootstrap`
  left in the configuration makes it form its own component next time it starts
  alone; and `pc.weight` is reported as arithmetic — the sum, and whether one
  node alone holds a majority — because unequal weights are legal and
  sometimes deliberate. Weight 0 is `BAD`: that node never counts towards
  quorum, so the cluster has one fewer vote than it has nodes.

- **`repl/sync-wait`** (GD-27) — nodes disagreeing about `wsrep_sync_wait`. The
  same query is causal or not depending on which node the proxy picked, which
  reaches the application as "sometimes the row is not there yet" and reaches
  no dashboard at all. Agreement is never a finding, whatever the value: what
  the cluster wants is not this tool's business.

- **`repl/auto-increment`** (GD-28) — ids that collide. With
  `wsrep_auto_increment_control` off the step and offsets are whatever somebody
  typed: two nodes sharing an `auto_increment_offset` is `BAD`, a step smaller
  than the number of nodes is `WARN`, and Galera managing them itself is `OK`.

## [0.1.1] - 2026-09-02

### Added

- **Identity and landing page** (GD-23) — a logo in `assets/` (three nodes, one
  of which does not agree with the other two), [INTENT.md](INTENT.md) as the
  charter for what the tool is for and what it will never become, and a
  dependency-free landing page in `site/` published to GitHub Pages by its own
  workflow.
- **The project's own metadata is generated and gated** (GD-24) — the GitHub
  description, website and topics are in
  [.github/repo.env](.github/repo.env) and written by `scripts/repo.sh apply`;
  `scripts/links.sh` fails on a local link the docs or the site point at
  something that is not in the tree; `scripts/site.sh` keeps the page's logo a
  copy of `assets/`. All three run in CI, so a change to how the project
  describes itself arrives in a diff.
- **Tests for the tooling** (GD-24) — `scripts/links_test.sh`,
  `scripts/site_test.sh` and `scripts/repo_test.sh` (against a fake `gh`, so
  neither GitHub nor the network is touched) join the backlog's own tests in
  CI. They found three real bugs in the link checker: a Markdown title —
  `[x](file.md "Title")` — was read as a second, broken link; a `<picture>`
  element's `srcset` was not checked at all, which is where the README's dark
  logo lives; and a link inside a code span or a fenced block was read as a
  link, so documenting the first bug in this file broke the build.

## [0.1.0] - 2026-09-02

First release: the Galera states that no `wsrep_*` counter can show you, from a
read-only audit.

### Added

- **Read-only by construction** (GD-1) — every query goes through one function
  that refuses anything but `SHOW` and `SELECT`; a test tries `UPDATE`,
  `DELETE`, `SET GLOBAL`, `FLUSH` and `DROP` and requires all of them to be
  rejected, and CI greps the source for a writing statement or an `Exec`.
- **System table drift** (GD-2) — the column definitions of every table in the
  `mysql` schema are fingerprinted per node and compared. Galera does not
  replicate maintenance on its own system tables, so a difference here is
  invisible to every replication metric. Reports which table and which nodes.
- **Cluster identity** (GD-3) — `wsrep_cluster_state_uuid` and
  `wsrep_cluster_conf_id` compared across nodes, so one cluster name covering
  two clusters is a finding instead of something noticed from row counts.
  Nodes disagreeing about the membership size beat the size itself.
- **Counters graded as rates** (GD-4) — `--state FILE` remembers the totals
  between runs; flow control, certification failures and the gcache window are
  graded over the interval. Without a baseline they report the lifetime figure
  and say they were not graded.
- **A restart invalidates the baseline** (GD-5) — a counter that went backwards,
  or an uptime that shrank, means *no baseline*: never a negative rate and never
  a wraparound-sized incident.
- **Node states** (GD-6) — `wsrep_ready`, `wsrep_connected`, `wsrep_on`,
  `wsrep_desync`, `read_only` and the local state comment, each reported with
  what it means operationally: a Donor is a warning, `wsrep_on` OFF is not.
- **A standalone server is one finding, not five** (GD-7) — a server without
  `wsrep_provider` is excluded from every cluster comparison, instead of firing
  size, quorum, ready and wsrep-on at once and describing an outage that is not
  happening.
- **ProxySQL against the cluster** (GD-8) — nodes missing from every serving
  hostgroup, ONLINE-but-not-Synced, shunned-but-Synced, and hostgroups with
  nothing online. The offline hostgroup is recognised as monitor-managed and
  never graded. Node matching tries every spelling the node answers to.
- **Missing primary keys** (GD-9) — the union across nodes, with the reason
  Galera calls them unsupported.
- **Config, DSNs and secrets** (GD-10) — JSON config with `${ENV_VAR}`
  expansion where an unset variable is an error, `--node name=DSN` for a
  file-less run, and a redactor so a driver error cannot carry a password into
  a terminal or a ticket.
- **Three renderers and exit 0** (GD-11) — text worst-first, `--json`,
  `--findings` (an empty array, never `null`); exit 0 whenever the audit ran,
  with `--exit-on S` to opt into exit 1.
- **The SQL, against a real server** (GD-12) — an integration test behind
  `GD_TEST_DSN`, run in CI against a MariaDB service container, because an
  `information_schema` join that is subtly wrong looks perfect from a fixture.

[0.1.0]: https://github.com/Allan-Nava/galera-doctor/releases/tag/v0.1.0
