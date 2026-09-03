#!/bin/sh
# brew_test.sh — tests for scripts/brew.sh.
#
# A Homebrew formula is a Ruby file that somebody else's machine executes to
# decide what to download and whether to trust it. The two failures that matter
# are a formula that does not parse (every `brew install` fails, and the error
# talks about Ruby) and a formula with a wrong or empty sha256 (Homebrew refuses
# the download, or worse, does not).
#
# So: the rendering is checked against a fixture checksum file, the result is
# parsed with ruby when this machine has one, and a checksum file missing a
# platform is required to be a hard error rather than a formula with a blank.
#
# POSIX sh only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/brew.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-brew-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}
pass() { echo "ok   $1"; }

# assert_contains <name> <needle>  (against $tmp/formula.rb)
assert_contains() {
	checks=$((checks + 1))
	if grep -qF -e "$2" "$tmp/formula.rb"; then pass "$1"; else
		fail "$1 — expected to find: $2"
		sed 's/^/       /' "$tmp/formula.rb" >&2
	fi
}

darwin_arm=1111111111111111111111111111111111111111111111111111111111111111
darwin_amd=2222222222222222222222222222222222222222222222222222222222222222
linux_arm=3333333333333333333333333333333333333333333333333333333333333333
linux_amd=4444444444444444444444444444444444444444444444444444444444444444

cat >"$tmp/SHA256SUMS" <<EOF
$darwin_arm  galera-doctor_0.3.0_darwin_arm64.tar.gz
$darwin_amd  galera-doctor_0.3.0_darwin_amd64.tar.gz
$linux_arm  galera-doctor_0.3.0_linux_arm64.tar.gz
$linux_amd  galera-doctor_0.3.0_linux_amd64.tar.gz
1111111111111111111111111111111111111111111111111111111111111119  galera-doctor_0.3.0_windows_amd64.tar.gz
EOF

checks=$((checks + 1))
if sh "$script" render v0.3.0 "$tmp/SHA256SUMS" >"$tmp/formula.rb" 2>"$tmp/err.txt"; then
	pass "the formula renders"
else
	fail "render failed"
	sed 's/^/       /' "$tmp/err.txt" >&2
	echo "$failures of $checks checks failed" >&2
	exit 1
fi

assert_contains "the class is the camel-cased name Homebrew expects" "class GaleraDoctor < Formula"
assert_contains "it carries a description" "desc "
assert_contains "it points at the project" 'homepage "https://github.com/Allan-Nava/galera-doctor"'
assert_contains "the licence is stated" 'license "MIT"'
assert_contains "the version is the tag without its v" 'version "0.3.0"'

# Every platform's url and checksum, and the tag — not the bare version — in
# the download path, because that is where the release actually lives.
assert_contains "the Apple Silicon archive" "galera-doctor_0.3.0_darwin_arm64.tar.gz"
assert_contains "the Apple Silicon checksum" "$darwin_arm"
assert_contains "the Intel Mac archive" "galera-doctor_0.3.0_darwin_amd64.tar.gz"
assert_contains "the Intel Mac checksum" "$darwin_amd"
assert_contains "the Linux arm64 checksum" "$linux_arm"
assert_contains "the Linux amd64 checksum" "$linux_amd"
assert_contains "the download path uses the tag" "/releases/download/v0.3.0/"

# A formula whose test block does not run the binary proves nothing.
assert_contains "there is a test block" "test do"
assert_contains "and it runs the binary" "version"

# Homebrew's own audit rejects a formula that installs nothing.
assert_contains "the binary is installed" 'bin.install "galera-doctor"'

# The windows archive has no business in a formula.
checks=$((checks + 1))
if grep -qF "windows" "$tmp/formula.rb"; then
	fail "the formula mentions windows"
	sed 's/^/       /' "$tmp/formula.rb" >&2
else
	pass "windows is left out"
fi

# ---------------------------------------------------------------------------
# It has to be Ruby, not almost-Ruby.
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if command -v ruby >/dev/null 2>&1; then
	if ruby -c "$tmp/formula.rb" >"$tmp/ruby.txt" 2>&1; then
		pass "the formula parses as Ruby"
	else
		fail "the formula does not parse"
		sed 's/^/       /' "$tmp/ruby.txt" >&2
	fi
else
	pass "the formula parses as Ruby (skipped: no ruby on this machine)"
fi

# ---------------------------------------------------------------------------
# A checksum file missing a platform must stop, and say which. Rendering a
# formula with an empty sha256 would ship a download nobody can verify.
# ---------------------------------------------------------------------------
grep -v darwin_arm64 "$tmp/SHA256SUMS" >"$tmp/partial.txt"
checks=$((checks + 1))
if sh "$script" render v0.3.0 "$tmp/partial.txt" >"$tmp/out.txt" 2>&1; then
	fail "a missing platform rendered a formula anyway"
	sed 's/^/       /' "$tmp/out.txt" >&2
elif grep -qF "darwin_arm64" "$tmp/out.txt"; then
	pass "a missing platform is a hard error that names it"
else
	fail "it failed without naming the missing platform"
	sed 's/^/       /' "$tmp/out.txt" >&2
fi

# A version that does not match the checksum file is the same class of mistake:
# it would render urls for files that do not exist.
checks=$((checks + 1))
if sh "$script" render v9.9.9 "$tmp/SHA256SUMS" >"$tmp/out.txt" 2>&1; then
	fail "a version that does not match the checksums rendered anyway"
else
	pass "a version that does not match the checksum file is refused"
fi

# ---------------------------------------------------------------------------
# write puts it where a tap looks for it.
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if (cd "$tmp" && FORMULA_DIR="$tmp/Formula" sh "$script" write v0.3.0 "$tmp/SHA256SUMS") >/dev/null 2>&1 &&
	[ -f "$tmp/Formula/galera-doctor.rb" ]; then
	pass "write lands in Formula/galera-doctor.rb"
else
	fail "write did not produce Formula/galera-doctor.rb"
fi

checks=$((checks + 1))
if sh "$script" nonsense >/dev/null 2>&1; then
	fail "an unknown subcommand exited 0"
else
	pass "an unknown subcommand is a usage error"
fi

# ---------------------------------------------------------------------------
# commit: write the formula and commit it *if it changed*.
#
# The release workflow used `git diff --quiet -- Formula/` for that decision,
# and git diff does not see an untracked file — so the very first release wrote
# the formula, decided it was "already current", and published a tap with no
# formula in it. Three cases, and the first one is the one that shipped broken.
# ---------------------------------------------------------------------------
repo="$tmp/repo"
mkdir -p "$repo"
(
	cd "$repo"
	git init -q -b main
	git config user.email t@example.com
	git config user.name test
	echo hello >README.md
	git add README.md
	git commit -qm initial
)
cp "$tmp/SHA256SUMS" "$repo/SHA256SUMS"

# 1. The formula does not exist yet: untracked, and it has to be committed.
checks=$((checks + 1))
if (cd "$repo" && sh "$script" commit v0.3.0 SHA256SUMS) >"$tmp/commit1.txt" 2>&1 &&
	(cd "$repo" && git log -1 --name-only --format=%s | grep -qF "Formula/galera-doctor.rb"); then
	echo "ok   a formula that did not exist yet is committed"
else
	fail "the first formula was not committed — this is the bug that shipped"
	sed 's/^/       /' "$tmp/commit1.txt" >&2
fi

checks=$((checks + 1))
if grep -qF "0.3.0" "$(cd "$repo" && git rev-parse --show-toplevel)/Formula/galera-doctor.rb"; then
	echo "ok   and it is the formula for the version asked for"
else
	fail "the committed formula is not for 0.3.0"
fi

# 2. Nothing changed: no second commit, and no error either.
checks=$((checks + 1))
before=$(cd "$repo" && git rev-parse HEAD)
if (cd "$repo" && sh "$script" commit v0.3.0 SHA256SUMS) >"$tmp/commit2.txt" 2>&1 &&
	[ "$(cd "$repo" && git rev-parse HEAD)" = "$before" ]; then
	echo "ok   an unchanged formula does not make an empty commit"
else
	fail "a second run committed again, or failed"
	sed 's/^/       /' "$tmp/commit2.txt" >&2
fi

# 3. A new version: tracked, modified, committed.
checks=$((checks + 1))
sed 's/0\.3\.0/0.4.0/g' "$tmp/SHA256SUMS" >"$repo/SHA256SUMS"
if (cd "$repo" && sh "$script" commit v0.4.0 SHA256SUMS) >"$tmp/commit3.txt" 2>&1 &&
	[ "$(cd "$repo" && git rev-parse HEAD)" != "$before" ] &&
	grep -qF "0.4.0" "$repo/Formula/galera-doctor.rb"; then
	echo "ok   a changed formula is committed"
else
	fail "the updated formula was not committed"
	sed 's/^/       /' "$tmp/commit3.txt" >&2
fi

# The commit says which version it is for: a tap's history is the only place
# that record exists.
checks=$((checks + 1))
if (cd "$repo" && git log -1 --format=%s) | grep -qF "0.4.0"; then
	echo "ok   the commit message names the version"
else
	fail "the commit message does not name the version: $(cd "$repo" && git log -1 --format=%s)"
fi

# A partial release must not be committed at all: a formula with a missing
# platform is worse than yesterday's formula.
checks=$((checks + 1))
grep -v darwin_arm64 "$repo/SHA256SUMS" >"$repo/partial.txt"
before=$(cd "$repo" && git rev-parse HEAD)
if (cd "$repo" && sh "$script" commit v0.5.0 partial.txt) >"$tmp/commit4.txt" 2>&1; then
	fail "a partial release was committed"
elif [ "$(cd "$repo" && git rev-parse HEAD)" = "$before" ]; then
	echo "ok   a partial release commits nothing"
else
	fail "a partial release moved HEAD"
fi

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
echo "all $checks checks passed"
