#!/bin/sh
# release_test.sh — tests for scripts/release.sh.
#
# A release script runs once per version, usually in CI, and its mistakes are
# permanent: an archive that is missing a platform, a checksum file that does
# not match what was uploaded, a binary that reports "dev" as its version. All
# three are invisible until somebody downloads the thing.
#
# The build itself is exercised for the host platform only — cross-compiling
# six targets in a unit test is a minute nobody has — but the matrix, the
# naming and the checksums are checked in full.
#
# POSIX sh only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/release.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-release-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}

pass() { echo "ok   $1"; }

# assert_contains <name> <needle> <file>
assert_contains() {
	checks=$((checks + 1))
	if grep -qF -e "$2" "$3"; then pass "$1"; else
		fail "$1 — expected to find: $2"
		sed 's/^/       /' "$3" >&2
	fi
}

# ---------------------------------------------------------------------------
# The matrix. Six platforms, and the two that people actually run this from —
# an Apple laptop and a Linux jump host — must be in it.
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if (cd "$root" && sh "$script" matrix) >"$tmp/matrix.txt" 2>&1; then
	pass "the matrix can be listed"
else
	fail "scripts/release.sh matrix failed"
	sed 's/^/       /' "$tmp/matrix.txt" >&2
fi

for want in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
	assert_contains "the matrix covers $want" "$want" "$tmp/matrix.txt"
done

checks=$((checks + 1))
if [ "$(grep -c . "$tmp/matrix.txt")" -ge 4 ]; then
	pass "the matrix is not a single platform"
else
	fail "the matrix has $(grep -c . "$tmp/matrix.txt") entries"
fi

# Every entry is os/arch, or the build loop silently produces nonsense names.
checks=$((checks + 1))
if grep -qvE '^[a-z0-9]+/[a-z0-9]+$' "$tmp/matrix.txt"; then
	fail "the matrix has an entry that is not os/arch"
	sed 's/^/       /' "$tmp/matrix.txt" >&2
else
	pass "every matrix entry is os/arch"
fi

# ---------------------------------------------------------------------------
# A build for the host platform only: names, checksums, and the version that
# ends up inside the binary.
# ---------------------------------------------------------------------------
host="$(go env GOOS)/$(go env GOARCH)"
checks=$((checks + 1))
if (cd "$root" && GD_RELEASE_MATRIX="$host" sh "$script" build v9.9.9 "$tmp/dist") >"$tmp/build.txt" 2>&1; then
	pass "a build succeeds"
else
	fail "the build failed"
	sed 's/^/       /' "$tmp/build.txt" >&2
	echo "$failures of $checks checks failed" >&2
	exit 1
fi

archive="galera-doctor_9.9.9_$(go env GOOS)_$(go env GOARCH).tar.gz"
checks=$((checks + 1))
if [ -f "$tmp/dist/$archive" ]; then
	pass "the archive is named for the version and the platform"
else
	fail "no $archive in the output"
	ls -la "$tmp/dist" >&2
fi

# The checksum file is what people verify against, so it has to match the bytes
# that were actually written — and cover every archive, not the last one.
checks=$((checks + 1))
if [ -f "$tmp/dist/SHA256SUMS" ]; then
	pass "SHA256SUMS is written"
else
	fail "no SHA256SUMS"
fi
checks=$((checks + 1))
if (cd "$tmp/dist" && shasum -a 256 -c SHA256SUMS >/dev/null 2>&1 ||
	sha256sum -c SHA256SUMS >/dev/null 2>&1); then
	pass "SHA256SUMS verifies against the archives"
else
	fail "SHA256SUMS does not verify"
	cat "$tmp/dist/SHA256SUMS" >&2
fi
checks=$((checks + 1))
if [ "$(grep -c . "$tmp/dist/SHA256SUMS")" -eq "$(ls "$tmp/dist"/*.tar.gz | wc -l | tr -d ' ')" ]; then
	pass "every archive has a line in SHA256SUMS"
else
	fail "SHA256SUMS covers $(grep -c . "$tmp/dist/SHA256SUMS") of $(ls "$tmp/dist"/*.tar.gz | wc -l | tr -d ' ') archives"
fi

# A binary that reports "dev" is a binary nobody can place in a ticket.
checks=$((checks + 1))
(cd "$tmp/dist" && tar xzf "$archive")
if "$tmp/dist/galera-doctor" version >"$tmp/version.txt" 2>&1 &&
	grep -qF "9.9.9" "$tmp/version.txt"; then
	pass "the version is compiled into the binary"
else
	fail "the binary does not report the release version"
	sed 's/^/       /' "$tmp/version.txt" >&2
fi

# The archive carries the licence and the readme with it: a binary in a
# download folder is separated from its repository forever.
for extra in LICENSE README.md; do
	checks=$((checks + 1))
	if tar tzf "$tmp/dist/$archive" | grep -qxF "$extra"; then
		pass "the archive carries $extra"
	else
		fail "$extra is not in the archive"
		tar tzf "$tmp/dist/$archive" >&2
	fi
done

# ---------------------------------------------------------------------------
# Usage errors: a release built without a version is the one mistake that must
# not produce artefacts at all.
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if (cd "$root" && sh "$script" build) >"$tmp/usage.txt" 2>&1; then
	fail "a build without a version exited 0"
else
	pass "a build without a version is a usage error"
fi

checks=$((checks + 1))
if (cd "$root" && sh "$script" publish) >/dev/null 2>&1; then
	fail "an unknown subcommand exited 0"
else
	pass "an unknown subcommand is a usage error"
fi

# ---------------------------------------------------------------------------
# The release notes are lifted from CHANGELOG.md, not retyped. A release whose
# notes say "see the changelog" is a release nobody reads, and a release built
# from a version with no changelog section at all is a version somebody forgot
# to write up — which has to stop the release, not produce an empty one.
# ---------------------------------------------------------------------------
cat >"$tmp/CHANGELOG.md" <<'EOF'
# Changelog

Preamble that must not end up in any release's notes.

## [0.3.0] - 2026-09-02

### Added

- **A check** (GD-1) — the interesting one.

## [0.2.0] - 2026-08-01

### Added

- **An older check** (GD-0) — from the previous release.
EOF

checks=$((checks + 1))
if (cd "$root" && CHANGELOG_FILE="$tmp/CHANGELOG.md" sh "$script" notes v0.3.0) >"$tmp/notes.txt" 2>&1; then
	pass "the notes can be extracted"
else
	fail "notes failed"
	sed 's/^/       /' "$tmp/notes.txt" >&2
fi
assert_contains "the notes carry this version's items" "the interesting one" "$tmp/notes.txt"

checks=$((checks + 1))
if grep -qF "An older check" "$tmp/notes.txt"; then
	fail "the notes leaked the previous release"
	sed 's/^/       /' "$tmp/notes.txt" >&2
else
	pass "the notes stop at the previous release"
fi
checks=$((checks + 1))
if grep -qF "Preamble" "$tmp/notes.txt"; then
	fail "the notes carry the changelog preamble"
else
	pass "the notes leave the preamble out"
fi
checks=$((checks + 1))
if grep -qF "## [0.3.0]" "$tmp/notes.txt"; then
	fail "the notes repeat the version heading GitHub already shows"
else
	pass "the version heading is not repeated"
fi

checks=$((checks + 1))
if (cd "$root" && CHANGELOG_FILE="$tmp/CHANGELOG.md" sh "$script" notes v9.9.9) >"$tmp/missing.txt" 2>&1; then
	fail "a version with no changelog section produced notes anyway"
	sed 's/^/       /' "$tmp/missing.txt" >&2
elif grep -qF "9.9.9" "$tmp/missing.txt"; then
	pass "a version with no changelog section is a hard error that names it"
else
	fail "it failed without naming the version"
	sed 's/^/       /' "$tmp/missing.txt" >&2
fi

# The real changelog has to work, or the workflow discovers this on a tag.
checks=$((checks + 1))
current=$(grep -m1 -E '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' "$root/CHANGELOG.md" | sed -E 's/^## \[([^]]*)\].*/\1/')
if (cd "$root" && sh "$script" notes "$current") >"$tmp/real.txt" 2>&1 && [ -s "$tmp/real.txt" ]; then
	pass "the notes for this repository's latest version ($current) are not empty"
else
	fail "notes for $current came out empty"
	sed 's/^/       /' "$tmp/real.txt" >&2
fi

# ---------------------------------------------------------------------------
# The container image name.
#
# Every release from v0.3.0 to v0.5.1 published its archives and failed to
# publish an image, with one error: `invalid tag
# "ghcr.io/Allan-Nava/galera-doctor:0.5.1": repository name must be lowercase`.
# github.repository_owner is spelled the way the account is spelled, and a
# registry does not accept a capital letter. actionlint cannot see this; the
# only thing that can is a check that the name is lowercased before it reaches
# a tag, and that the docs name the same image.
# ---------------------------------------------------------------------------
workflow="$root/.github/workflows/release.yml"

checks=$((checks + 1))
if grep -qE 'ghcr\.io/\$\{\{ *github\.repository(_owner)? *\}\}' "$workflow"; then
	fail "an image tag interpolates the owner directly — it is spelled Allan-Nava and a registry wants lowercase"
	grep -nE 'ghcr\.io/' "$workflow" | sed 's/^/       /' >&2
else
	pass "no image tag interpolates the repository owner directly"
fi

checks=$((checks + 1))
if grep -qE "tr '?A-Z'? '?a-z'?|,,}" "$workflow"; then
	pass "the image name is lowercased before it is used"
else
	fail "nothing in the workflow lowercases the image name"
fi

# The name in the workflow and the name in the docs have to be the same string,
# or the docs point at an image that does not exist.
checks=$((checks + 1))
if grep -qF "ghcr.io/allan-nava/galera-doctor" "$root/docs/install.md" &&
	grep -qF "ghcr.io/allan-nava/galera-doctor" "$root/README.md"; then
	pass "the docs name the lowercase image"
else
	fail "the docs do not name ghcr.io/allan-nava/galera-doctor"
fi

# ---------------------------------------------------------------------------
# Publishing has to be repeatable. The first attempt at v0.5.1 created the
# release and then failed elsewhere; re-running the workflow for that tag must
# fix the release rather than fail on "already exists".
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if grep -qE 'gh release (view|edit)' "$workflow" && grep -qF -- "--clobber" "$workflow"; then
	pass "a release that already exists is updated, not refused"
else
	fail "re-running the workflow for an existing tag would fail on gh release create"
fi

# ---------------------------------------------------------------------------
# The Homebrew formula is generated, committed and published by the workflow —
# and until now nothing ever installed it. A formula with a correct-looking
# sha256 and a wrong url, a platform block that never matches, or a test block
# that fails, would sit on main and break on somebody's laptop instead of in
# CI. So the release has to install what it just published, on a real macOS
# runner, and the tap has to be checked periodically because a release asset
# can be deleted long after the run that made it went green.
# ---------------------------------------------------------------------------
brew_workflow="$root/.github/workflows/brew.yml"

# The brew job's own lines. An awk range like /^  brew:/,/^  [a-z]/ matches a
# single line, because the start line satisfies the end pattern too — the flag
# is what makes this the job rather than its heading.
brew_job() {
	awk '/^  brew:/ { f = 1; next } f && /^  [a-z_-]+:/ { f = 0 } f' "$workflow"
}

checks=$((checks + 1))
if grep -qE '^  brew:' "$workflow"; then
	pass "the release has a brew job"
else
	fail "nothing in the release workflow installs the formula it publishes"
fi

checks=$((checks + 1))
if brew_job | grep -qE 'runs-on: *macos'; then
	pass "and it runs on macOS, where brew installs are real"
else
	fail "the brew job does not run on macOS"
fi

checks=$((checks + 1))
if brew_job | grep -qF "needs: artifacts"; then
	pass "it waits for the release to exist"
else
	fail "the brew job does not wait for the archives to be published"
fi

for want in "brew install" "brew test" "brew style"; do
	checks=$((checks + 1))
	if brew_job | grep -qF -e "$want"; then
		pass "the brew job runs $want"
	else
		fail "the brew job never runs $want"
	fi
done

# Installing is not proof the right thing was installed.
checks=$((checks + 1))
if brew_job | grep -qF "galera-doctor version"; then
	pass "and asserts the installed binary reports the released version"
else
	fail "nothing checks that the installed binary is the released one"
fi

# The tap is a published thing with a life after the release run.
checks=$((checks + 1))
if [ -f "$brew_workflow" ]; then
	pass "there is a workflow for the published tap"
else
	fail "no .github/workflows/brew.yml: nothing ever checks the tap again"
fi

checks=$((checks + 1))
if [ -f "$brew_workflow" ] && grep -qF "schedule:" "$brew_workflow"; then
	pass "the tap is checked on a schedule, not only when somebody remembers"
else
	fail "the tap check has no schedule"
fi

checks=$((checks + 1))
if [ -f "$brew_workflow" ] && grep -qF "workflow_dispatch" "$brew_workflow"; then
	pass "and can be run by hand"
else
	fail "the tap check cannot be run on demand"
fi

# The real user path, not a local file: brew tap <url> && brew install.
checks=$((checks + 1))
if [ -f "$brew_workflow" ] && grep -qF "brew tap" "$brew_workflow"; then
	pass "the tap check installs the way the docs say to"
else
	fail "the tap check does not go through brew tap"
fi

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
echo "all $checks checks passed"
