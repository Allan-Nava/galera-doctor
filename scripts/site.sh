#!/bin/sh
# site.sh — the landing page in site/, without a build step.
#
# site/index.html is hand-written and self-contained: no generator, no theme, no
# dependency to keep current. The only thing it needs from the rest of the tree
# is the logo, and assets/ is the source of truth for that.
#
#   scripts/site.sh sync      copy assets/*.svg into site/ (run after editing the logo)
#   scripts/site.sh check     fail if the copies have drifted (CI gate)
#   scripts/site.sh serve     serve site/ on http://localhost:8000
#
# POSIX sh only. The deploy is .github/workflows/pages.yml.

set -eu

svgs="mark.svg logo.svg logo-dark.svg"

sync() {
	for f in $svgs; do cp "assets/$f" "site/$f"; done
	echo "synced $svgs into site/"
}

check() {
	drift=0
	for f in $svgs; do
		cmp "assets/$f" "site/$f" >/dev/null 2>&1 || { echo "site/$f differs from assets/$f" >&2; drift=1; }
	done
	[ "$drift" -eq 0 ] || { echo "" >&2; echo "run scripts/site.sh sync and commit the result" >&2; exit 1; }
	echo "site/ carries the current logo"
}

case "${1:-check}" in
sync) sync ;;
check) check ;;
serve)
	command -v python3 >/dev/null 2>&1 || { echo "python3 is required to serve locally" >&2; exit 2; }
	echo "http://localhost:8000 — Ctrl-C to stop"
	python3 -m http.server 8000 --directory site
	;;
*)
	echo "usage: scripts/site.sh [sync|check|serve]" >&2
	exit 2
	;;
esac
