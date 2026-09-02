#!/bin/sh
# site_test.sh — tests for scripts/site.sh.
#
# site/ carries a copy of the logo so the landing page needs no build step, and
# a copy is a thing that drifts. The gate that says so is worth as much as its
# ability to fail: a `check` that passes on a stale copy publishes the wrong
# logo, and a `sync` that only copies the file it happens to be asked about
# leaves one of the three behind.
#
# The paths come from ASSETS_DIR and SITE_DIR so the test can point the script
# at a fixture instead of at this repository's own logo.
#
# POSIX sh only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/site.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-site-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}

svgs="mark.svg logo.svg logo-dark.svg"

# A fixture pair: three logos in assets/, and a site/ that is in sync.
fixture() {
	rm -rf "$tmp/assets" "$tmp/site"
	mkdir -p "$tmp/assets" "$tmp/site"
	for f in $svgs; do
		printf '<svg id="%s"/>\n' "$f" >"$tmp/assets/$f"
		cp "$tmp/assets/$f" "$tmp/site/$f"
	done
}

run() { ASSETS_DIR="$tmp/assets" SITE_DIR="$tmp/site" sh "$script" "$@"; }

# assert_pass <name> <command>...
assert_pass() {
	name=$1
	shift
	checks=$((checks + 1))
	if run "$@" >"$tmp/out.txt" 2>&1; then
		echo "ok   $name"
	else
		fail "$name — the script failed"
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
}

# assert_fail <name> <needle> <command>...
assert_fail() {
	name=$1
	needle=$2
	shift 2
	checks=$((checks + 1))
	if run "$@" >"$tmp/out.txt" 2>&1; then
		fail "$name — the script passed"
		sed 's/^/       /' "$tmp/out.txt" >&2
		return
	fi
	if grep -qF "$needle" "$tmp/out.txt"; then
		echo "ok   $name"
	else
		fail "$name — failed for the wrong reason, wanted: $needle"
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
}

# ---------------------------------------------------------------------------
# In sync: check passes.
# ---------------------------------------------------------------------------
fixture
assert_pass "a synced site passes the check" check

# ---------------------------------------------------------------------------
# Each of the three copies, one at a time: a check that only looks at the first
# one publishes the other two stale.
# ---------------------------------------------------------------------------
for drifted in $svgs; do
	fixture
	printf '<svg id="stale"/>\n' >"$tmp/site/$drifted"
	assert_fail "drift in $drifted is caught" "site/$drifted" check
done

# A copy that is missing entirely is drift, not a crash.
fixture
rm "$tmp/site/logo.svg"
assert_fail "a missing copy is caught" "logo.svg" check

# The message has to say how to fix it, or the gate is a riddle.
checks=$((checks + 1))
fixture
printf '<svg id="stale"/>\n' >"$tmp/site/mark.svg"
run check >"$tmp/out.txt" 2>&1 || true
if grep -qF "scripts/site.sh sync" "$tmp/out.txt"; then
	echo "ok   the failure names the command that fixes it"
else
	fail "the failure did not name scripts/site.sh sync"
	sed 's/^/       /' "$tmp/out.txt" >&2
fi

# ---------------------------------------------------------------------------
# sync repairs every copy, including one that was deleted, and is idempotent.
# ---------------------------------------------------------------------------
fixture
printf '<svg id="stale"/>\n' >"$tmp/site/mark.svg"
printf '<svg id="stale"/>\n' >"$tmp/site/logo-dark.svg"
rm "$tmp/site/logo.svg"
assert_pass "sync repairs a drifted and a missing copy" sync
assert_pass "the check passes after a sync" check
assert_pass "sync is idempotent" sync
assert_pass "the check still passes" check

# ---------------------------------------------------------------------------
# An unknown subcommand is a usage error, not a silent success: a typo in CI
# would otherwise be a gate that never runs.
# ---------------------------------------------------------------------------
fixture
assert_fail "an unknown subcommand is a usage error" "usage:" publish

checks=$((checks + 1))
if run nonsense >/dev/null 2>&1; then
	fail "an unknown subcommand exited 0"
else
	status=$?
	if [ "$status" -eq 2 ]; then
		echo "ok   a usage error exits 2"
	else
		fail "a usage error exited $status, expected 2"
	fi
fi

# ---------------------------------------------------------------------------
# sync also writes the sitemap, because its lastmod is the one thing on the
# page that has to be generated: a date somebody typed is a date that is wrong
# a week later.
# ---------------------------------------------------------------------------
fixture
assert_pass "sync writes the sitemap" sync

checks=$((checks + 1))
if [ -f "$tmp/site/sitemap.xml" ]; then
	echo "ok   the sitemap is written"
else
	fail "no sitemap.xml after a sync"
fi

checks=$((checks + 1))
if grep -qF "<loc>https://allan-nava.github.io/galera-doctor/</loc>" "$tmp/site/sitemap.xml"; then
	echo "ok   the sitemap lists the canonical URL"
else
	fail "the sitemap does not list the canonical URL"
	sed 's/^/       /' "$tmp/site/sitemap.xml" >&2
fi

checks=$((checks + 1))
if grep -qE '<lastmod>[0-9]{4}-[0-9]{2}-[0-9]{2}</lastmod>' "$tmp/site/sitemap.xml"; then
	echo "ok   the lastmod is a date"
else
	fail "the lastmod is not a YYYY-MM-DD date"
	sed 's/^/       /' "$tmp/site/sitemap.xml" >&2
fi

checks=$((checks + 1))
if command -v python3 >/dev/null 2>&1; then
	if python3 -c 'import sys,xml.dom.minidom as m; m.parse(sys.argv[1])' "$tmp/site/sitemap.xml" >/dev/null 2>&1; then
		echo "ok   the sitemap is well-formed XML"
	else
		fail "the sitemap is not well-formed XML"
	fi
else
	echo "ok   the sitemap is well-formed XML (skipped: no python3)"
fi

# The check is about the logo, not the sitemap: a regenerated lastmod must not
# fail the drift gate on every commit.
assert_pass "the check still passes with a sitemap present" check

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
echo "all $checks checks passed"
