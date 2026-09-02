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
# ASSETS_DIR and SITE_DIR override the two directories, so the tooling can be
# tested against a fixture instead of this repository's own logo.
#
# POSIX sh only. The deploy is .github/workflows/pages.yml.
# Tests: scripts/site_test.sh.

set -eu

svgs="mark.svg logo.svg logo-dark.svg"
assets_dir="${ASSETS_DIR:-assets}"
site_dir="${SITE_DIR:-site}"

sync() {
	for f in $svgs; do cp "$assets_dir/$f" "$site_dir/$f"; done
	echo "synced $svgs into $site_dir/"
}

check() {
	drift=0
	for f in $svgs; do
		cmp "$assets_dir/$f" "$site_dir/$f" >/dev/null 2>&1 ||
			{ echo "$site_dir/$f differs from $assets_dir/$f" >&2; drift=1; }
	done
	[ "$drift" -eq 0 ] || { echo "" >&2; echo "run scripts/site.sh sync and commit the result" >&2; exit 1; }
	echo "$site_dir/ carries the current logo"
}

case "${1:-check}" in
sync) sync ;;
check) check ;;
serve)
	command -v python3 >/dev/null 2>&1 || { echo "python3 is required to serve locally" >&2; exit 2; }
	echo "http://localhost:8000 — Ctrl-C to stop"
	python3 -m http.server 8000 --directory "$site_dir"
	;;
*)
	echo "usage: scripts/site.sh [sync|check|serve]" >&2
	exit 2
	;;
esac
