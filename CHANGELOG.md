# Changelog

All notable changes to galera-doctor are recorded here. The format is
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the versioning is
[Semantic Versioning](https://semver.org/). Every release is a tagged `vX.Y.Z`
with its own section; `minor` for new checks or flags, `patch` for fixes. Items
reference their `GD-n` id in [BACKLOG.md](BACKLOG.md).

## [0.12.0] - 2026-09-04

M2 is finished: everything in it that can be done through this tool's channel
is done, and the two items that cannot are in M8 with the reason.

### Added

- **`--watch D`** (GD-18) — re-audit on an interval and print only the
  transitions, for the window in which somebody is repairing a cluster. The
  first report in full, so they know where they are starting from, and after
  that only what moved, with the status it moved from: `[BAD → OK]`. A tick
  that changed nothing prints nothing — reprinting twenty `OK` lines every ten
  seconds buries the one that matters.

  A finding that **disappeared** is a transition too, and during a repair it is
  usually the line somebody is waiting for; an `OK` that stopped being reported
  is not, because the check simply did not run this time. Running it against a
  real server is what found that gap: killing the server printed the new
  `node/read` error and said nothing about the finding that had gone.

  It refuses an interval under a second, which is a busy loop against a cluster
  that is already having a bad day, and refuses `--json` and `--findings`,
  which emit one document per run — a stream of documents is not a document.
  `--watch 0` is a usage error rather than "watching, disabled": `fs.Visit` is
  what tells a flag that was typed apart from one that was left out.

  Still not a daemon and not a monitoring system, which
  [INTENT.md](INTENT.md) rules out: it runs in the foreground, holds its
  baseline in memory, and stops when the person watching it stops. The loop is
  driven by a channel rather than a ticker so it can be tested — an untested
  loop is how "it printed nothing" becomes "it printed nothing because it never
  ran".

### Changed

- **M2 is shipped, and M8 says why the rest is not** — `GD-14` (backup
  freshness) and `GD-15` (SST/IST history) moved to **M8 — Parked, and why**.
  Neither can be done through read-only SQL to the nodes: one needs the
  filesystem and an off-site destination, the other needs the error log because
  MariaDB exposes no state-transfer counters. M8 is not a queue — it is where
  an item goes with the reason it cannot be built and the form in which it
  would earn its place, because an item that quietly disappears is one somebody
  proposes again in six months.

## [0.11.0] - 2026-09-04

M7's high-priority half: the write paths a cluster diagram does not show.

### Added

- **`repl/async-in`, `repl/async-out`** (GD-47) — `SHOW REPLICA STATUS` and
  `SHOW REPLICAS` per node, under their older names on older builds and read by
  column name, because the columns were renamed with the statements. A member
  that is also an async replica is a second write path *into* the cluster
  (`WARN`, naming the source); a configured link that is not running is `BAD`
  and carries the server's own error, because somebody believes those writes
  are arriving; a member feeding a downstream replica is a dependency the rest
  of the cluster knows nothing about, and the next SST rebuilds its binary logs
  out from under it.

  Verified against two real MariaDB servers replicating to each other, running
  and stopped: the replica reports its source with both threads up and zero
  seconds behind, the source reports its downstream replica by host and port,
  and a stopped link reports both threads down with no `Seconds_Behind_Source`
  at all — which is why that field is a pointer, since a zero there would read
  as "perfectly caught up".

- **`repl/server-id`, `repl/gtid-domain`, `repl/gtid-strict`** (GD-48) — two
  nodes sharing a `server_id` is `BAD` with or without a binary log: a replica
  downstream cannot tell their events apart and a replication loop becomes
  possible. The two GTID checks are graded only when some node has a binary
  log, because with nothing able to replicate out, a domain id that cannot
  reach anybody is not a finding.

- **`repl/triggers`** (GD-50) — `wsrep_slave_run_triggers` per node. The
  writer's trigger has already put its rows in the writeset, so an applier that
  runs it again applies them twice. Nodes disagreeing is `BAD` — the same
  statement ends up with different rows per node, and certification compares
  writesets rather than their consequences. Uniformly on is `WARN`.

- **`audit/coverage`** now reports a replication status that could not be read,
  alongside the other gaps.

## [0.10.1] - 2026-09-04

### Fixed

- **The release notes made the tree dirty** (GD-56) — goreleaser refuses to
  release from a dirty working tree, and the step that lifts the notes out of
  `CHANGELOG.md` wrote them to `notes.md` in the repository root. The first
  goreleaser release, v0.10.0, failed after 0s with `git is in a dirty state`
  — before creating a release, so nothing had to be cleaned up, but the error
  reads like a goreleaser problem rather than "your own pipeline wrote a file".

  The notes go to the runner's temp directory now and are handed over by path.
  `scripts/release_test.sh` asserts both the fix and the premise behind it: a
  file written into the tree shows up in `git status --porcelain`, and one
  written outside does not.

## [0.10.0] - 2026-09-04

### Changed

- **goreleaser owns the release, and Homebrew gets a cask** (GD-55) — the same
  shape as the sibling tools, so one person can read both repositories.
  `.goreleaser.yaml` builds the six platforms, writes the archives (still
  carrying the licence, the readme, the changelog and the example config) and a
  checksum file still called `SHA256SUMS`, so every published download and
  verify command keeps working. It also adds what the hand-rolled scripts never
  had: an SBOM per archive, keyless cosign signatures over the checksums and
  the images, and the `ghcr.io` image as a real multi-arch manifest.

  `brew install --cask Allan-Nava/tap/galera-doctor` — the cask is generated
  from the checksums of the archives the tag just uploaded and pushed to
  [Allan-Nava/homebrew-tap](https://github.com/Allan-Nava/homebrew-tap), next
  to the sibling tools. It carries the postflight hook that strips
  `com.apple.quarantine`: macOS quarantines an unsigned binary, and a
  quarantined binary installs cleanly and then refuses to run. A prerelease
  publishes no cask (`skip_upload: "auto"`), so a `-rc` tag cannot hand every
  user a release candidate.

  `scripts/brew.sh`, `scripts/brew_test.sh` and the in-repo `Formula/` are
  **removed** — two things generating a brew install is how they drift — and
  `scripts/release.sh` keeps exactly one job, the release notes, because a
  changelog written for people beats a list of commit subjects. Building
  locally is `goreleaser release --snapshot --clean`.

  `.github/workflows/brew.yml` now installs the published cask on Apple Silicon
  **and** Intel after every release, weekly, and on demand — and asserts that
  the tap serves the *latest* release, which is the only check that catches a
  cask that was never pushed. Two lessons from the sibling tool are asserted by
  `scripts/release_test.sh` rather than rediscovered: the Intel leg must be
  `macos-15-intel`, since a retired label queues forever instead of failing,
  and installing *something* proves nothing on its own.

  CI validates the configuration and builds a snapshot on every push, because a
  tag is permanent and a configuration error must fail before it.

### Note

- The release now needs a `HOMEBREW_TAP_GITHUB_TOKEN` secret (a PAT with repo
  scope) to push the cask to the shared tap. Without it the release fails early
  with a message saying exactly that, rather than deep inside goreleaser with a
  template error about an empty variable.

## [0.9.1] - 2026-09-04

### Added

- **The release installs the formula it publishes** (GD-54) — a macOS job runs
  `brew style`, `brew install --formula`, `brew test` and asserts that
  `galera-doctor version` reports the tag that was just released. It renders
  the formula from the published release's own `SHA256SUMS` rather than from
  the committed file, because that is what a user gets and it does not depend
  on the formula commit having landed first. `brew audit` runs alongside and is
  advisory: it has rules for the core tap that a single-binary formula in its
  own tap cannot satisfy, and failing a release on those would teach everybody
  to ignore the job.

  Until now the formula was generated, committed and published without anything
  ever installing it. A correct-looking `sha256` on a wrong URL, a platform
  block that never matches, a `test do` that fails — all of it would have sat
  on `main` and broken on somebody's laptop.

- **The published tap is checked on a schedule** (GD-54) —
  `.github/workflows/brew.yml` taps by URL and installs by name, monthly and on
  demand, the way [docs/install.md](docs/install.md) says to. The release job
  proves the formula was right when it was written; this proves the tap still
  works, because a release asset can be deleted long after the run that made it
  went green.

## [0.9.0] - 2026-09-04

### Added

- **The reference pages are published** (GD-21) — `scripts/docs.sh` renders
  `docs/*.md` into `site/docs/`, so the landing page links its own
  documentation instead of sending everybody to GitHub's Markdown view. One
  source per document, one stylesheet generated with the pages, a `check`
  subcommand that fails when a page has fallen behind its source, and the
  sitemap now lists every published page with the commit date of the document
  behind it.

  The renderer is awk, and the Markdown subset is deliberately the one these
  docs use — headings, paragraphs, fenced blocks, tables, lists, blockquotes,
  bold, emphasis, inline code, links. Anything else is escaped and passed
  through as text rather than guessed at: a renderer that guesses produces a
  page that still looks plausible, which is worse than one that leaves a line
  alone.

  `scripts/docs_test.sh` (42 checks) is what makes that safe, and it earned its
  place immediately — five real bugs, every one of which produced a page that
  looked finished:

  - `**`code`**` — bold wrapping a code span, which these docs are full of —
    left both markers stranded, because code spans were taken out first and
    `bold()` only ever saw the fragments.
  - a `**bold**` span that a Markdown author wrapped across two lines never
    closed, since the inline pass ran line by line. Paragraphs and list items
    are buffered to the end of the block now.
  - `*emphasis*` was not implemented at all and reached the browser as
    asterisks.
  - restoring a code span with `sub()` reads an `&` in the replacement as the
    whole match, so `&lt;` came back as `lt;` — the escaping undone by the
    unescaping. It is done with `index()` and `substr()` instead.
  - an apostrophe in an awk comment closed the single-quoted shell string
    around the whole program.

## [0.8.0] - 2026-09-03

### Added

- **`cluster/latency`** (GD-17) — slow, or simply far away. `queue/send`
  reports a deep queue and cannot say why: a node across a WAN link is doing
  exactly what physics allows, and a node with a failing disk in the same rack
  looks identical from there. Two things the cluster already knows make the
  distinction — `wsrep_evs_repl_latency`, its own measurement of the round trip,
  and `gmcast.segment`, which says which pairs are *supposed* to be far apart.

  So the comparison happens inside a segment, where distance is not an
  explanation: a node four times slower than the fastest of its own segment is
  a `WARN` naming the link, carrying that node's send queue in the hint. Across
  segments the latency is reported and never graded. Two guards keep it from
  becoming noise: `--latency-floor` (2ms), below which a ratio between
  microseconds is not a finding, and zero samples — a provider that has
  measured nothing reports `0/0/0/0/0`, which is *not graded* rather than the
  fastest cluster in the fleet.

## [0.7.0] - 2026-09-03

### Added

- **`audit/changes`** (GD-32) — what moved since the previous run: appeared,
  cleared, got worse, improved. The state file now carries the previous run's
  verdicts alongside its counters, so a second run says what changed instead of
  repeating itself. Only the statuses are compared, never the messages — a
  message carries a measurement, and comparing prose would report a change
  every time a percentage moved by 0.1. The summary is always `OK`: every
  finding it mentions is in the same report with its own severity, and counting
  them twice makes one incident look like two. A cluster that could not be read
  at all still carries its findings forward, so "the node came back" is a
  transition rather than a silence.

  The on-disk format is **2**. A format 1 file is ignored rather than migrated,
  as before: read as "that run found nothing", it would report every current
  finding as newly appeared.

### Fixed

- **The baseline that was never found** (GD-46) — the state file namespaces
  nodes by cluster, because one `--state` file may cover a whole `--config`, and
  the audit asks `Since()` about a bare node name. Nothing sat in between: the
  CLI wrote `compress/sg-01`, the audit looked up `sg-01`, the lookup missed on
  every run, and every counter check reported *not graded: no baseline* forever.
  It was invisible precisely because the fallback is honest — "no baseline" is a
  legitimate state, and a check that never grades looks exactly like a cluster
  with nothing to grade.

  `State.Scope` and `State.Merge` are now the two sides of that boundary, each
  with tests: what lands on disk is namespaced, what the audit gets is one
  cluster's view keyed the way it asks, and a cluster that is not in the file
  yet gets an empty baseline rather than another cluster's.

## [0.6.0] - 2026-09-03

M6 — configured, and not running. Everything before this compares the nodes
with each other; these six compare what a node is *configured to believe* with
what is actually there.

### Added

- **`cluster/peers`** (GD-38) — `wsrep_cluster_address` resolved against the
  nodes actually in the component, on every spelling each answers to. It is
  only read at startup, which is why nothing reports it: a list naming servers
  this cluster does not have is `WARN`, a list naming **no** current member is
  `BAD` (that node cannot rejoin), and an empty `gcomm://` is `BAD` — the
  bootstrap form left in a running node's configuration means the next restart
  forms a second Primary component instead of rejoining this one.

- **`flow/settings`** (GD-39) — `gcs.fc_limit`, `gcs.fc_factor` and the
  master-slave flags compared across nodes. The cluster pauses when the slowest
  queue reaches *its own* limit, so the node with the smallest one paces every
  writer; `flow/paused` reports that pausing without the reason. The finding
  names the node that throttles first.

- **`repl/appliers`** (GD-40) — `wsrep_slave_threads` (or its 10.6 name
  `wsrep_applier_threads`) differing between nodes, reported next to that
  node's receive queue, because the queue is why it matters. A node with a
  quarter of its peers' apply threads is behind by configuration rather than by
  load — a different fix from looking at its disk.

- **`sst/size`** (GD-41) — what a rejoin actually copies: the data and index
  length of every application table, from the largest node, with the SST method
  beside it. "This node needs a full SST" is not actionable without the
  gigabytes it implies and the donor it takes out of service for the duration.
  `OK`, because a size is a number rather than a fault. The collector reads it
  as a pointer: `nil` is "not read", since a zero would make an SST look free —
  and `SUM()` over no rows is `NULL`, which is a real answer about an empty
  cluster and not a failure.

- **`proxysql/monitor`** (GD-42) — the proxy's Galera monitor is what keeps
  every other `proxysql/*` comparison live. With `mysql-monitor_enabled=false`,
  or a hostgroup set with `active=0`, the hostgroups still look exactly like a
  healthy cluster's — they just stopped following the one that is running, so
  the agreement is a photograph. `BAD`. A proxy with no Galera hostgroup table
  is a deployment choice rather than a stopped monitor, and an unread variable
  is not a variable set to false: both stay quiet.

- **`audit/coverage`** (GD-43) — one line saying what this run could not audit:
  a node that could not be read, a missing `information_schema` grant, a clock
  that did not answer, a missing baseline. A cron job sees an exit code and a
  worst status, and neither distinguishes "nothing is wrong" from "the check
  that would have found it never ran". Access gaps are `WARN` because a
  statement was not made; a missing baseline is named in the same line and does
  not escalate, since running without `--state` is a choice and warning about a
  choice on every run is how a check stops being read.

## [0.5.2] - 2026-09-02

### Fixed

- **The release published no image, and no formula** (GD-45) — every tag from
  v0.3.0 to v0.5.1 published its archives, its checksums and its attestation,
  and then failed twice over in silence:

  - `ghcr.io/${{ github.repository_owner }}/…` renders as
    `ghcr.io/Allan-Nava/galera-doctor`, and a registry refuses a capital
    letter: `invalid tag … repository name must be lowercase`. The image name
    is now lowercased once, in one step, and used for both the tags and the
    attestation subject.
  - the formula step decided whether to commit with `git diff --quiet --
    Formula/`, and `git diff` does not see a file that is not tracked yet — so
    the first release wrote `Formula/galera-doctor.rb`, reported "the formula is
    already current", and published a tap with no formula in it. The decision
    moved into `scripts/brew.sh commit`, which uses `git status`, and it is
    tested against a real temporary repository for all three cases: untracked,
    unchanged, changed.

  Two more that fell out of writing the tests:

  - `brew.sh write` used `render > formula.rb`, which truncates the formula
    before `render` runs: a partial release would have left the tap holding an
    empty file. It renders to a temporary file and moves it.
  - `checksum` reported a missing platform with `exit`, from inside a command
    substitution, where an exit only leaves the subshell — and `set -e` is
    switched off inside an `if !` condition, so a formula with an **empty**
    `sha256` would have been written and committed. It returns instead, and the
    caller propagates.

- **Publishing is repeatable** (GD-45) — a tag whose first attempt created the
  release and failed afterwards can be re-run: the workflow updates an existing
  release (`gh release edit`, `gh release upload --clobber`) instead of stopping
  at "already exists".

## [0.5.1] - 2026-09-02

### Added

- **The landing page says what it is** (GD-44) — a canonical URL, `robots`,
  Open Graph and Twitter card tags with an absolute `og:image`, schema.org
  JSON-LD describing the tool, `robots.txt`, and a `sitemap.xml` whose
  `lastmod` is generated by `scripts/site.sh sync` rather than typed. The
  preview card that renders in Slack and in a search result is
  `site/og-image.png`, a screenshot of `assets/og-image.html` at exactly
  1200x630 taken by `scripts/og.sh` — a page rather than a hand-drawn image, so
  it stays in step with what the site says.

  The title and the description now lead with what somebody searches for — a
  read-only Galera cluster audit for MariaDB/MySQL — instead of only with the
  line the project likes about itself.

  All of it is a set of tags nothing renders, which is why
  `scripts/seo_test.sh` is in CI: it checks the canonical, the card tags, that
  the `og:image` is an absolute URL to a PNG that exists and is 1200x630, that
  the JSON-LD parses and carries a type, that the title survives truncation and
  the description fits a result, that there is exactly one `h1`, that every
  `img` has alt text, and that `robots.txt` and the sitemap agree with each
  other.

## [0.5.0] - 2026-09-02

### Added

- **`node/clock`** (GD-16) — the spread between the nodes' own clocks, read as
  `UNIX_TIMESTAMP(NOW(6))` in the same round trip as everything else, and
  stamped with this host's time immediately before that round trip so the
  server's response time is not folded into the number. The nodes are compared
  with each other rather than with the auditing host, because this host's NTP
  is not a reference either. Replication is unaffected — Galera orders writes
  by sequence number — and everything a human does during an incident is:
  `--clock-warn` (2s) and `--clock-bad` (30s). A clock that could not be read
  is not a clock that agrees.

- **`repl/ws-limits`** (GD-30) — `wsrep_max_ws_size` and `wsrep_max_ws_rows`
  differing between nodes. The write-set is certified on the node that accepted
  it and refused by the applier with the smaller limit, which then leaves the
  cluster: it arrives as a node failure and it is a configuration difference.
  The finding names the node holding the cluster's real limit, the smallest
  non-zero one.

- **`node/durability`** (GD-35) — `innodb_flush_log_at_trx_commit` and
  `sync_binlog` differing between nodes. A cluster's durability is its weakest
  node's, not its average, so "committed on every node" can survive a process
  crash and not a power cut. A uniform relaxed setting is the cluster's
  decision and stays quiet; the nodes disagreeing is the finding.

- **`cluster/segments`** (GD-31) — the `gmcast.segment` map, reported as the
  map it is, because the intent behind one lives in somebody's head rather than
  in the server. The single shape that cannot be deliberate is graded: every
  node in a segment of its own, which turns off the one thing segments buy —
  one copy of a write-set per segment instead of one per node.

## [0.4.0] - 2026-09-02

### Added

- **`gcache/recover`** (GD-33) — `gcache.recover` off, one finding per node
  because each node's restart is its own. `gcache/window` measures how much
  time the write-set cache buys before a restarting node needs a full state
  transfer, and this setting discards that cache along with the process: the
  window is a buffer being thrown away, and a two-minute maintenance restart
  costs a full SST — which takes a donor out of service with it. A provider
  that does not report the option is not a provider with it off.

- **`repl/osu-method`** (GD-34) — a node whose `wsrep_OSU_method` is RSU. TOI
  replicates a schema change to every node and NBO does the same without the
  cluster-wide lock; RSU applies it where it was run and leaves the others
  alone, which is precisely how the drift `schema/drift` reports comes to
  exist. The hint says so: the cause reported next to the symptom is the
  difference between a finding and a diagnosis, and a test asserts both survive
  into the same run.

## [0.3.0] - 2026-09-02

### Added

- **Release pipeline** (GD-20) — a tag builds it all.
  `.github/workflows/release.yml` runs the race-detector tests first (a tag is
  permanent), then `scripts/release.sh build`: one static binary per platform
  for six platforms, a `tar.gz` each carrying the licence and the readme, and
  one `SHA256SUMS` written from the bytes actually on disk. The archives get a
  provenance attestation, so a download can be checked against the run that
  produced it with `gh attestation verify`, and the release notes are lifted
  from this file rather than retyped — a version with no CHANGELOG section
  stops the release instead of publishing empty notes.

- **`ghcr.io/allan-nava/galera-doctor`** (GD-20) — the image on every tag,
  `linux/amd64` and `linux/arm64`, tagged with the version and `latest`, with
  the same attestation pushed to the registry. The Dockerfile now takes
  buildx's `TARGETOS`/`TARGETARCH` and cross-compiles with the Go toolchain
  instead of building under emulation: the arm64 image costs seconds rather
  than minutes, and it is still `scratch` plus a static binary, no shell.

- **Homebrew** (GD-37) — this repository is the tap:

  ```sh
  brew tap Allan-Nava/galera-doctor https://github.com/Allan-Nava/galera-doctor
  brew install galera-doctor
  ```

  `Formula/galera-doctor.rb` is generated by `scripts/brew.sh` from the
  release's own `SHA256SUMS` and committed by the release workflow, so the
  checksum Homebrew verifies is the one computed over the uploaded bytes. A
  platform missing from the checksum file is a hard error: rendering a blank
  `sha256` would ship a download nobody can verify. The formula installs the
  released binary — no compiler on the user's machine — and its `test do` block
  runs it.

  Both scripts are tested (`scripts/release_test.sh`, `scripts/brew_test.sh`,
  in CI): the matrix, the archive naming, that `SHA256SUMS` verifies against
  the archives it lists, that the version is compiled into the binary, that the
  notes stop at the previous release, and that the rendered formula parses as
  Ruby.

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
