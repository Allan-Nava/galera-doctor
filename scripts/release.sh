#!/bin/sh
# release.sh — the release notes for a tag.
#
# Building, archiving, signing and publishing are goreleaser's
# (.goreleaser.yaml), the same shape as the sibling tools. This is the one part
# that stays here: the notes are lifted from CHANGELOG.md and handed to
# goreleaser with --release-notes, because a changelog written for people beats
# a list of commit subjects.
#
#   scripts/release.sh notes VERSION   the CHANGELOG section for the release page
#
# To build the artefacts locally, without publishing anything:
#
#   goreleaser release --snapshot --clean
#
# POSIX sh only. Tests: scripts/release_test.sh.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-release.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

# notes prints the CHANGELOG section for one version: what GitHub shows on the
# release page. Lifted rather than retyped, and a version with no section is a
# version somebody forgot to write up — which stops the release.
notes() {
	version=${1:-}
	[ -n "$version" ] || {
		echo "usage: scripts/release.sh notes VERSION" >&2
		exit 2
	}
	bare=${version#v}
	changelog="${CHANGELOG_FILE:-$root/CHANGELOG.md}"
	[ -f "$changelog" ] || { echo "release.sh: $changelog: not found" >&2; exit 2; }

	# From the heading for this version to the next version heading, with the
	# heading itself dropped: GitHub already shows the version and the date.
	awk -v want="## [$bare]" '
		index($0, want) == 1 { inside = 1; next }
		inside && /^## \[/ { inside = 0 }
		inside { print }
	' "$changelog" >"$tmp/notes.md"

	if ! grep -q '[^[:space:]]' "$tmp/notes.md"; then
		echo "release.sh: CHANGELOG.md has no section for $bare — write it up before tagging" >&2
		exit 2
	fi
	# Trim the blank lines the section boundary leaves behind.
	awk 'NF { blank = 0; body = 1 } !NF { blank++ } body && (NF || !after) { after = 1 } body { print }' "$tmp/notes.md" |
		sed -e :a -e '/^\n*$/{$d;N;ba' -e '}'
}

case "${1:-}" in
notes)
	shift
	notes "$@"
	;;
*)
	echo "usage: scripts/release.sh notes VERSION" >&2
	exit 2
	;;
esac
