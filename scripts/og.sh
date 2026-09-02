#!/bin/sh
# og.sh — render site/og-image.png, the preview card.
#
# The card is a page rather than a hand-drawn image so it stays in step with
# what the site actually says: assets/og-image.html is screenshotted at exactly
# 1200x630, which is what every card generator crops to. Run it after changing
# the tagline or the logo; scripts/seo_test.sh fails if the PNG is missing or
# the wrong size.
#
# Needs a headless Chrome. The PNG is checked in, so nobody else needs one.
#
# POSIX sh only.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
out="${1:-$root/site/og-image.png}"

chrome=""
for candidate in \
	"${CHROME:-}" \
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
	"/Applications/Chromium.app/Contents/MacOS/Chromium" \
	"$(command -v google-chrome-stable 2>/dev/null || true)" \
	"$(command -v google-chrome 2>/dev/null || true)" \
	"$(command -v chromium 2>/dev/null || true)"; do
	if [ -n "$candidate" ] && [ -x "$candidate" ]; then
		chrome="$candidate"
		break
	fi
done
[ -n "$chrome" ] || {
	echo "og.sh: no headless Chrome found — set CHROME to one" >&2
	echo "the checked-in site/og-image.png is only regenerated when the card changes" >&2
	exit 2
}

# The mark is referenced relatively, so render from assets/.
"$chrome" --headless --disable-gpu --hide-scrollbars \
	--window-size=1200,630 --force-device-scale-factor=1 \
	--virtual-time-budget=2000 \
	--screenshot="$out" "file://$root/assets/og-image.html" >/dev/null 2>&1

[ -f "$out" ] || { echo "og.sh: Chrome produced no screenshot" >&2; exit 1; }
echo "wrote $out"
