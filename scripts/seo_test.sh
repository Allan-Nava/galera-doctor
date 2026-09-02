#!/bin/sh
# seo_test.sh — what the landing page has to say about itself.
#
# The page is the first thing somebody sees who searched for the symptom rather
# than for this tool: "galera nodes different schema", "wsrep_flow_control_paused
# always red". Everything checked here is a thing that is silently absent — a
# missing canonical splits the page between two URLs, a relative og:image
# renders a broken preview in Slack and in a search result, and structured data
# with a typo is structured data nobody parses. None of it fails a browser, so
# none of it fails without a test.
#
# POSIX sh, plus python3 for the one thing sed cannot do: parse the JSON-LD.
#
#   scripts/seo_test.sh          check site/
#   SITE_DIR=... scripts/seo_test.sh

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
site="${SITE_DIR:-$root/site}"
page="$site/index.html"
url="https://allan-nava.github.io/galera-doctor/"

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}
pass() { echo "ok   $1"; }

# has <name> <needle>
has() {
	checks=$((checks + 1))
	if grep -qF -e "$2" "$page"; then pass "$1"; else fail "$1 — missing: $2"; fi
}

# count <pattern> — how many lines match
count() { grep -cE "$1" "$page" || true; }

[ -f "$page" ] || { echo "$page: not found" >&2; exit 2; }

# ---------------------------------------------------------------------------
# The basics a search result is built from.
# ---------------------------------------------------------------------------
has "the page declares its language" '<html lang="en"'
has "there is a canonical URL" '<link rel="canonical" href="'"$url"'"'
has "robots are told to index it" '<meta name="robots"'

checks=$((checks + 1))
title=$(sed -n 's/.*<title>\(.*\)<\/title>.*/\1/p' "$page" | head -1)
if [ -n "$title" ]; then
	# Google truncates a title around 60 characters. Longer is not an error,
	# but a title whose distinctive half is cut off is.
	if [ "${#title}" -le 70 ]; then
		pass "the title is $(printf %s "$title" | wc -c | tr -d ' ') characters, short enough to survive truncation"
	else
		fail "the title is ${#title} characters: the end will be cut off — $title"
	fi
else
	fail "there is no title"
fi

checks=$((checks + 1))
desc=$(sed -n 's/.*<meta name="description" content="\([^"]*\)".*/\1/p' "$page" | head -1)
if [ "${#desc}" -ge 70 ] && [ "${#desc}" -le 165 ]; then
	pass "the description is ${#desc} characters, inside what a result shows"
else
	fail "the description is ${#desc} characters, want 70-165: $desc"
fi

checks=$((checks + 1))
if [ "$(count '<h1')" -eq 1 ]; then
	pass "there is exactly one h1"
else
	fail "there are $(count '<h1') h1 elements"
fi

# ---------------------------------------------------------------------------
# The preview card: Slack, Teams, a search result, somebody's tweet.
# ---------------------------------------------------------------------------
has "og:title" '<meta property="og:title"'
has "og:description" '<meta property="og:description"'
has "og:type" '<meta property="og:type" content="website"'
has "og:url is absolute" '<meta property="og:url" content="'"$url"'"'
has "og:site_name" '<meta property="og:site_name"'
has "a large summary card" '<meta name="twitter:card" content="summary_large_image"'
has "twitter:title" '<meta name="twitter:title"'
has "twitter:description" '<meta name="twitter:description"'

# An og:image has to be an absolute URL, and a raster: SVG previews do not
# render in most of the places that matter.
checks=$((checks + 1))
img=$(sed -n 's/.*<meta property="og:image" content="\([^"]*\)".*/\1/p' "$page" | head -1)
case "$img" in
https://*.png) pass "og:image is an absolute URL to a PNG" ;;
"") fail "there is no og:image" ;;
*) fail "og:image must be an absolute https URL to a PNG, got: $img" ;;
esac

checks=$((checks + 1))
name=${img##*/}
if [ -n "$name" ] && [ -f "$site/$name" ]; then
	pass "the og:image file is in the site"
else
	fail "og:image points at $name, which is not in $site"
fi

# 1200x630 is what every card generator crops to.
checks=$((checks + 1))
if [ -f "$site/$name" ] && command -v file >/dev/null 2>&1; then
	if file "$site/$name" | grep -qE '1200 ?x ?630'; then
		pass "the og:image is 1200x630"
	else
		fail "the og:image is not 1200x630: $(file "$site/$name")"
	fi
else
	pass "the og:image is 1200x630 (skipped: no file(1))"
fi

has "og:image carries alt text" '<meta property="og:image:alt"'
has "and its dimensions, so the card does not reflow" '<meta property="og:image:width" content="1200"'

# ---------------------------------------------------------------------------
# Structured data. A typo here is invisible: nothing renders it, parsers just
# skip the block.
# ---------------------------------------------------------------------------
has "there is a JSON-LD block" 'application/ld+json'

checks=$((checks + 1))
if command -v python3 >/dev/null 2>&1; then
	if python3 - "$page" <<'PY'
import json, re, sys

html = open(sys.argv[1], encoding="utf-8").read()
blocks = re.findall(r'<script type="application/ld\+json">(.*?)</script>', html, re.S)
if not blocks:
    print("no JSON-LD block")
    raise SystemExit(1)
for raw in blocks:
    data = json.loads(raw)  # a syntax error fails the test, which is the point
    if data.get("@context") not in ("https://schema.org", "http://schema.org"):
        print("JSON-LD without a schema.org @context:", data.get("@context"))
        raise SystemExit(1)
    if not data.get("@type"):
        print("JSON-LD without an @type")
        raise SystemExit(1)
    if not data.get("name") or not data.get("description"):
        print("JSON-LD without a name or a description")
        raise SystemExit(1)
PY
	then
		pass "the JSON-LD parses and carries a type, a name and a description"
	else
		fail "the JSON-LD is not usable"
	fi
else
	pass "the JSON-LD parses (skipped: no python3)"
fi

# ---------------------------------------------------------------------------
# The crawler's own two files.
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if [ -f "$site/robots.txt" ]; then pass "robots.txt exists"; else fail "no robots.txt"; fi

checks=$((checks + 1))
if [ -f "$site/robots.txt" ] && grep -qF "Sitemap: ${url}sitemap.xml" "$site/robots.txt"; then
	pass "robots.txt points at the sitemap"
else
	fail "robots.txt does not name the sitemap"
fi

checks=$((checks + 1))
if [ -f "$site/sitemap.xml" ] && grep -qF "<loc>$url</loc>" "$site/sitemap.xml"; then
	pass "the sitemap lists the page"
else
	fail "the sitemap does not list $url"
fi

checks=$((checks + 1))
if [ -f "$site/sitemap.xml" ] && command -v python3 >/dev/null 2>&1; then
	if python3 -c 'import sys,xml.dom.minidom as m; m.parse(sys.argv[1])' "$site/sitemap.xml" >/dev/null 2>&1; then
		pass "the sitemap is well-formed XML"
	else
		fail "the sitemap is not well-formed XML"
	fi
else
	pass "the sitemap is well-formed XML (skipped)"
fi

# ---------------------------------------------------------------------------
# Images without alt text are the one accessibility failure a crawler reports.
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if [ "$(count '<img ')" -eq "$(grep -oE '<img [^>]*alt="' "$page" | wc -l | tr -d ' ')" ]; then
	pass "every img has alt text"
else
	fail "$(count '<img ') img elements, $(grep -oE '<img [^>]*alt="' "$page" | wc -l | tr -d ' ') with alt"
fi

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
echo "all $checks checks passed"
