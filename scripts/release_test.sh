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
# Building, packaging and publishing are goreleaser's now — the same
# configuration shape as checkfleet, so one person can read both. What is
# checked here is everything that would fail *silently*: a cask that goes to
# the wrong tap, a checksum file renamed out from under the documented download
# command, an image tag that never becomes a manifest.
# ---------------------------------------------------------------------------
config="$root/.goreleaser.yaml"

checks=$((checks + 1))
if [ -f "$config" ]; then pass "there is a goreleaser configuration"; else
	fail "no .goreleaser.yaml"
	echo "$failures of $checks checks failed" >&2
	exit 1
fi

checks=$((checks + 1))
if command -v goreleaser >/dev/null 2>&1; then
	if (cd "$root" && goreleaser check) >"$tmp/grcheck.txt" 2>&1; then
		pass "goreleaser accepts the configuration"
	else
		fail "goreleaser check failed"
		sed 's/^/       /' "$tmp/grcheck.txt" >&2
	fi
else
	pass "goreleaser accepts the configuration (skipped: goreleaser not installed)"
fi

# The download instructions in docs/install.md name SHA256SUMS. goreleaser
# calls it checksums.txt unless told otherwise, and a rename would break every
# published verify command without failing anything.
has_config() {
	checks=$((checks + 1))
	if grep -qF -e "$2" "$config"; then pass "$1"; else
		fail "$1 — missing from .goreleaser.yaml: $2"
	fi
}
has_config "the checksum file keeps the documented name" "SHA256SUMS"
has_config "the archives are named as the docs say" "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
has_config "the version is stamped into the binary" "-X main.version={{ .Version }}"
has_config "the binary is static" "CGO_ENABLED=0"

# The cask, and the tap it goes to. A cask published to the wrong repository is
# a brew install that installs somebody else's tool.
has_config "there is a Homebrew cask" "homebrew_casks:"
has_config "it goes to the shared tap" "name: homebrew-tap"
has_config "with the token that can write there" "HOMEBREW_TAP_GITHUB_TOKEN"
# An unsigned binary is quarantined by Gatekeeper: without the hook the cask
# installs and macOS refuses to run it.
has_config "the quarantine attribute is stripped on install" "com.apple.quarantine"
# A prerelease tag must not push a cask: brew install would hand every user a
# release candidate.
has_config "a prerelease publishes no cask" 'skip_upload: "auto"'

# The bug that failed four releases in a row: ghcr refuses a capital letter,
# and github.repository_owner is spelled Allan-Nava. The image names are
# literals in this file now, so the assertion is that they are lowercase.
checks=$((checks + 1))
# {{ .Version }} is a template, not part of the name, so it is stripped
# before looking — otherwise its capital V fails the check it is not part of.
if grep -oE 'ghcr\.io/[^"]*' "$config" | sed 's/{{[^}]*}}//g' | grep -q '[A-Z]'; then
	fail "an image name has a capital letter: a registry refuses that"
	grep -oE 'ghcr\.io/[^"]*' "$config" | sed 's/{{[^}]*}}//g' | grep '[A-Z]' | sed 's/^/       /' >&2
else
	pass "every image name is lowercase"
fi

# Publishing has to be repeatable: the first attempt at a tag can create the
# release and fail afterwards, and re-running must fix that release rather than
# stop because it exists.
has_config "a release that already exists is replaced, not refused" "mode: replace"

has_config "the image is built for both architectures" "docker_manifests:"
has_config "and the manifest carries the version" "ghcr.io/allan-nava/galera-doctor:{{ .Version }}"

checks=$((checks + 1))
if grep -qF "Dockerfile.release" "$config" && [ -f "$root/Dockerfile.release" ]; then
	pass "goreleaser has a Dockerfile that takes the prebuilt binary"
else
	fail "the image build has no Dockerfile.release (goreleaser injects the binary, it does not compile it)"
fi

# The hand-rolled formula machinery has to be gone, not left beside its
# replacement: two things generating a brew install is how they drift.
for gone in scripts/brew.sh scripts/brew_test.sh Formula/galera-doctor.rb; do
	checks=$((checks + 1))
	if [ -e "$root/$gone" ]; then
		fail "$gone is still here, next to the cask that replaced it"
	else
		pass "$gone is gone"
	fi
done

# ---------------------------------------------------------------------------
# The tap check. checkfleet learned this the hard way twice, and both lessons
# are asserted here rather than rediscovered.
# ---------------------------------------------------------------------------
brew_workflow="$root/.github/workflows/brew.yml"

checks=$((checks + 1))
if [ -f "$brew_workflow" ]; then pass "there is a tap workflow"; else
	fail "no .github/workflows/brew.yml"
fi

has_brew() {
	checks=$((checks + 1))
	if [ -f "$brew_workflow" ] && grep -qF -e "$2" "$brew_workflow"; then pass "$1"; else
		fail "$1 — missing from brew.yml: $2"
	fi
}
has_brew "it installs the cask the way the docs say to" "brew install --cask Allan-Nava/tap/galera-doctor"
has_brew "it runs after a release" "workflow_run"
has_brew "on a schedule too" "schedule:"
has_brew "and on demand" "workflow_dispatch"
# CF-159: macos-13 was retired, and a job asking for a label with no runners
# behind it does not fail — it queues forever, so the workflow never concludes
# and in practice verifies nothing.
has_brew "the Intel leg uses an image that still exists" "macos-15-intel"
checks=$((checks + 1))
if [ -f "$brew_workflow" ] && grep -qE '^ *- *macos-13' "$brew_workflow"; then
	fail "macos-13 was retired: that leg queues forever instead of failing"
else
	pass "no retired runner label"
fi
# The check that actually matters: installing *something* proves nothing when
# the cask on the tap is stale.
has_brew "it asserts the tap serves the latest release" "releases/latest"
has_brew "and that Gatekeeper will let the binary run" "com.apple.quarantine"
has_brew "a hung install fails instead of looking busy" "timeout-minutes"

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
echo "all $checks checks passed"
