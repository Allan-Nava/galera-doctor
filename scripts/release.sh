#!/bin/sh
# release.sh — build the release artefacts for a tag.
#
# One static binary per platform, one tar.gz each carrying the licence and the
# readme, and one SHA256SUMS covering all of them. No goreleaser, no release
# tooling to keep current: this repository has one dependency and its tooling
# has none.
#
#   scripts/release.sh matrix              the platforms, one os/arch per line
#   scripts/release.sh build VERSION [DIR] build them all into DIR (default dist/)
#   scripts/release.sh notes VERSION       the CHANGELOG section for the release page
#
# VERSION is a tag with or without its v: v0.2.1 and 0.2.1 both produce
# galera-doctor_0.2.1_linux_amd64.tar.gz, and the binary reports 0.2.1.
#
# GD_RELEASE_MATRIX overrides the platform list, which is how the test builds
# for the host only instead of cross-compiling six targets.
#
# POSIX sh and the Go toolchain. Tests: scripts/release_test.sh.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-release.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

# The platforms people actually run an audit from: a Linux jump host, an Apple
# laptop, and the two BSD-adjacent ones that cost nothing to produce.
default_matrix="linux/amd64
linux/arm64
darwin/amd64
darwin/arm64
freebsd/amd64
windows/amd64"

matrix() {
	if [ -n "${GD_RELEASE_MATRIX:-}" ]; then
		printf '%s\n' "$GD_RELEASE_MATRIX" | tr ' ' '\n' | grep .
	else
		printf '%s\n' "$default_matrix"
	fi
}

# sha256 of a file, whichever tool this machine has.
sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1"
	else
		shasum -a 256 "$1"
	fi
}

build() {
	version=${1:-}
	out=${2:-$root/dist}
	[ -n "$version" ] || {
		echo "usage: scripts/release.sh build VERSION [DIR]" >&2
		exit 2
	}
	# A tag is v0.2.1; an artefact name is 0.2.1.
	bare=${version#v}

	rm -rf "$out"
	mkdir -p "$out"
	stage="$out/.stage"

	matrix | while IFS=/ read -r os arch; do
		[ -n "$os" ] && [ -n "$arch" ] || continue
		bin=galera-doctor
		[ "$os" = windows ] && bin=galera-doctor.exe
		rm -rf "$stage"
		mkdir -p "$stage"

		# Same flags as the Dockerfile: static, trimmed, version stamped.
		CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
			-trimpath -ldflags "-s -w -X main.version=$bare" \
			-o "$stage/$bin" "$root/cmd/galera-doctor"

		# A binary in a download folder is separated from its repository
		# forever, so the licence and the readme travel with it.
		cp "$root/LICENSE" "$root/README.md" "$stage/"

		archive="galera-doctor_${bare}_${os}_${arch}.tar.gz"
		tar czf "$out/$archive" -C "$stage" "$bin" LICENSE README.md
		echo "  $archive"
	done
	rm -rf "$stage"

	# One checksum file covering every archive, written from the bytes that are
	# actually on disk rather than from what the loop believed it wrote.
	(
		cd "$out"
		: >SHA256SUMS
		for f in *.tar.gz; do
			sha256 "$f" >>SHA256SUMS
		done
	)
	echo "  SHA256SUMS"
	echo "built $(matrix | grep -c .) platform(s) for $bare in $out"
}

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

case "${1:-matrix}" in
matrix) matrix ;;
notes)
	shift
	notes "$@"
	;;
build)
	shift
	build "$@"
	;;
*)
	echo "usage: scripts/release.sh [matrix|build VERSION [DIR]|notes VERSION]" >&2
	exit 2
	;;
esac
