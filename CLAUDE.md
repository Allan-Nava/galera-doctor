# CLAUDE.md — galera-doctor

`galera-doctor` (`github.com/Allan-Nava/galera-doctor`) audits a MariaDB/MySQL
Galera cluster read-only and reports the states the cluster's own metrics cannot
show. One static Go binary, one dependency (the MySQL driver):
`internal/cluster` is the data model and the only place that talks to a server,
`internal/audit` turns snapshots into findings, `internal/state` remembers
counters between runs so rates can be graded, `internal/proxysql` compares the
proxy's view with the cluster's, `internal/config` reads the definitions,
`internal/output` renders, `cmd/galera-doctor` is the CLI.

This file is the Claude Code copy of the operating brief. [AGENTS.md](AGENTS.md)
is the original — when the two disagree, AGENTS.md wins and this file gets
fixed.

## Working rules (ALWAYS)

- **Every feature earns its place against one sentence**: *say what is wrong
  with this cluster that its own metrics do not say*. A generic MySQL health
  check (connections, buffer pool, slow queries, replica lag) belongs in
  [checkfleet](https://github.com/Allan-Nava/checkfleet); a repair operation
  belongs in a human's hands.
- **Read-only, mechanically.** Every query goes through `cluster.Query`, which
  refuses anything but `SHOW` and `SELECT`; CI greps the source for a writing
  statement and for `Exec`. This tool is pointed at clusters that are already
  in trouble — "it does not write" has to be a property, not a claim in a
  README.
- **A total is never a verdict.** The `wsrep_*` counters only go up and reset on
  restart. Grade them over the interval between runs (`--state`) or do not grade
  them at all, and say which. A check that goes red once and stays red is a
  check people stop reading.
- **No baseline is a state, not a zero.** A missing state file, a counter that
  went backwards, an uptime that shrank, a state file from another format
  version: all of them mean *no baseline*. Never a negative rate.
- **A missing value is not zero.** `Snapshot.Float` returns `(0, false)` for a
  missing *or* unparseable value, and callers must consult the bool: a build
  without a metric is not a healthy one.
- **Say what was not audited.** A node that could not be read is an `ERROR`,
  because every cluster-wide statement below it was made without that node. A
  node without the grants for `information_schema` is reported as *not audited*
  rather than dropped from the comparison.
- **Do not grade what the tool does not own.** ProxySQL's offline hostgroup is
  moved by ProxySQL's own Galera monitor: its contents are never a finding, and
  a "cleanup" of it fights the monitor. The same rule applies to any
  auto-managed state discovered later.
- **A standalone server is one finding.** `wsrep_provider` unset means "not a
  cluster member" — exclude it from every comparison instead of reporting five
  simultaneous catastrophes.
- **A DSN never reaches the output.** Nodes are identified by name; driver
  errors go through `redact` first. Error messages end up in tickets.
- **Exit 0 whenever the audit ran.** Findings are output. Only `--exit-on`
  produces exit 1; a usage error or an unusable config exits 2.
- **Test first, always**, against fixtures — the interesting states (a split
  brain, a drifted system table, a proxy that disagrees) cannot be conjured on
  demand against a real server. **This covers the tooling**: every
  `scripts/x.sh` has a `scripts/x_test.sh` written before it changes, wired
  into CI in the same commit, with the script taking env overrides
  (`BACKLOG_FILE`, `REPO_ENV`, `ASSETS_DIR`, a fake `gh` on `PATH`) so a test
  never touches this repository or the network. The SQL itself is covered by the integration
  test behind `GD_TEST_DSN`, which is where a wrong `information_schema` join
  gets caught.
- **Backlog first**: work exists in `BACKLOG.md` with a `GD-n` id, and
  `ROADMAP.md` is generated — run `scripts/backlog.sh roadmap` after editing the
  backlog or CI fails. Commits and CHANGELOG entries reference the id.
- **Releases**: every release is a tagged `vX.Y.Z` with a new `CHANGELOG.md`
  section. `minor` for new checks or flags, `patch` for fixes. **Never
  `git push`**, tags included. No `Co-Authored-By` trailers. Building,
  archiving, signing and publishing are **goreleaser's** (`.goreleaser.yaml`),
  in the same shape as the sibling tools; the Homebrew **cask** goes to
  `Allan-Nava/homebrew-tap` and needs the `HOMEBREW_TAP_GITHUB_TOKEN` secret.
  The one part that is not goreleaser's is the release notes: `scripts/release.sh
  notes` lifts them from `CHANGELOG.md`, because a changelog written for people
  beats a list of commit subjects.

## Pattern for adding a check

1. **Backlog first**: a `GD-n` with a milestone, `prio`, `size` and `labels`.
   Regenerate the roadmap.
2. **Red first**: extend the fixture cluster in `audit_test.go` with the
   property planted on one node, and watch the assertion fail for the right
   reason.
3. **Decide what the check is grading.** A gauge (queue depth, a state string)
   can be graded as it stands. A counter needs `state.Since` and an ungraded
   fallback. There is no third option.
4. **The finding says what to do.** `Message` carries the measurement,
   `Value`/`Unit` carry the number, and `Hint` says what it means for whoever
   is on call — the raw variable name tells them nothing at 03:00.
5. **Two tests minimum**: one that plants the condition and asserts it is found
   *and correctly attributed*, and one that asserts a healthy cluster stays
   quiet.
6. `go test -race ./...`, `gofmt`, `go vet`, and the read-only grep.
7. **Close the loop**: CHANGELOG referencing the `GD-n`, tick the backlog with
   `ver=X.Y.Z`, regenerate the roadmap, tag. No push.

## Known traps / technical rules

- **Galera does not replicate the `mysql.*` tables the way it replicates
  application DDL.** That is the whole premise of `systables/drift`, and it is
  why the fix is per node (`mysql_upgrade`, or aligning the definition by hand)
  rather than "let it replicate".
- **`wsrep_provider` is the signal for "is this a cluster member", not
  `wsrep_on`.** `wsrep_on` is OFF on a real member that somebody took out of
  replication — a serious finding that must not be mistaken for "not a
  cluster".
- **`wsrep_flow_control_paused` is a fraction since the last status reset**, not
  a current value. `wsrep_flow_control_paused_ns` is the nanosecond counter to
  diff between runs, which is what makes an honest percentage possible.
- **The gcache size is not the useful number.** 512 MB is forty minutes or
  ninety seconds depending on the write rate, so the window is only reported
  when there is a rate to compute it from.
- **A fingerprint must be order-independent.** The server returns
  `information_schema` rows in whatever order it likes; sorting before hashing
  is what keeps a healthy cluster from reporting drift on every run.
- **A proxy's server list is written by a human.** Match a node on every
  spelling it answers to — configured name, `wsrep_node_address` (with the port
  and any CIDR suffix stripped), `wsrep_node_name` — or a node that is right
  there gets reported as missing.
- **`DisallowUnknownFields` on the config is deliberate.** A typo in
  `"noeds"` would otherwise produce a cluster with no nodes and a confusing
  error much later.
- **A DSN contains `=` in its parameters**, so `--node name=DSN` splits on the
  *first* one only.
- **Everything around the code has a CI gate, and the gate is the point.**
  `ROADMAP.md` is generated from `BACKLOG.md`, the About box is generated from
  `.github/repo.env` (`scripts/repo.sh`), `site/`'s logo is a copy of `assets/`
  (`scripts/site.sh`) and every local link is checked (`scripts/links.sh`). Add
  a generated artefact only together with the check that it is current —
  otherwise it is a file that is wrong and nobody notices.
- **The About box is a diff, not a browser.** Description, website and topics
  are in `.github/repo.env`; `scripts/repo.sh apply` needs admin and is run by
  hand, `scripts/repo.sh check` runs in CI. GitHub's own `GITHUB_TOKEN` cannot
  write repository settings, which is why the apply is not a workflow.
- **The landing page has no build step.** `site/index.html` is hand-written and
  self-contained — no generator, no theme, no dependency to keep current — and
  `.github/workflows/pages.yml` uploads the directory as it stands.
- **The page's SEO is tags nothing renders.** A missing canonical, a relative
  `og:image`, JSON-LD with a typo: none of them fails a browser or a build, so
  `scripts/seo_test.sh` is the only thing standing between them and a broken
  preview card. `site/og-image.png` is a screenshot of
  `assets/og-image.html` at exactly 1200x630 — regenerate it with
  `scripts/og.sh` (needs a headless Chrome) after changing the tagline or the
  logo, and never hand-edit the PNG.
