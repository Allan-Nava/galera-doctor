#!/bin/sh
# docs.sh — render docs/*.md into the published site.
#
# The landing page is hand-written; the reference pages are generated from the
# same Markdown that renders on GitHub, so there is one source for each
# document and no second copy to keep in step.
#
#   scripts/docs.sh build    render docs/*.md into site/docs/
#   scripts/docs.sh check    fail if a page is stale (CI gate)
#
# SRC_DIR and OUT_DIR override the two directories so the renderer can be
# tested against fixtures.
#
# The Markdown subset is deliberately the one this repository's docs use:
# headings, paragraphs, fenced blocks, tables, lists, blockquotes, bold, inline
# code and links. Anything else is escaped and passed through as text rather
# than guessed at — a renderer that guesses produces a page that still looks
# plausible, which is worse than one that leaves a line alone.
#
# POSIX sh and awk only. Tests: scripts/docs_test.sh.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
src="${SRC_DIR:-$root/docs}"
out="${OUT_DIR:-$root/site/docs}"
base="https://allan-nava.github.io/galera-doctor/docs/"
repo="https://github.com/Allan-Nava/galera-doctor/blob/main/"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-docs.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

# render <file.md> <slug> — one page on stdout.
render() {
	awk -v slug="$2" -v base="$base" -v repo="$repo" '
	function esc(s) {
		gsub(/&/, "\\&amp;", s)
		gsub(/</, "\\&lt;", s)
		gsub(/>/, "\\&gt;", s)
		return s
	}
	# A link target that is a sibling document becomes its published page; one
	# that leaves the docs directory goes to the repository, because that is
	# where it is published. Anything absolute is left alone.
	function href(t,   anchor, i) {
		if (t ~ /^[a-z]+:/ || t ~ /^#/ || t ~ /^\/\//) return t
		anchor = ""
		if ((i = index(t, "#")) > 0) { anchor = substr(t, i); t = substr(t, 1, i - 1) }
		if (t ~ /^\.\.\//) { sub(/^\.\.\//, "", t); return repo t anchor }
		if (t ~ /\.md$/) { sub(/\.md$/, ".html", t); return t anchor }
		return t anchor
	}
	# Inline markup, applied to already-escaped text.
	#
	# Code spans are replaced by placeholders first, so a ** or a [ inside one
	# is never read as markup — and then the rest of the pass sees one
	# continuous string, which is what lets **`check/name`** close. Splitting
	# on backticks and running bold on the fragments leaves both markers
	# stranded, in a page that otherwise looks finished.
	function inline(s,   n, i, parts, held, out) {
		n = split(s, parts, "`")
		out = ""
		ncode = 0
		for (i = 1; i <= n; i++) {
			if (i % 2 == 0) {
				ncode++
				code[ncode] = parts[i]
				out = out "\002" ncode "\002"
			} else {
				out = out parts[i]
			}
		}
		out = markup(out)
		# Put the code spans back by hand: in the replacement text of sub(),
		# awk reads an & as the whole match — so &lt; would come back as the
		# placeholder followed by lt;, the escaping undone by the unescaping.
		# (And no apostrophes in here: the whole program is one single-quoted
		# shell string.)
		for (i = 1; i <= ncode; i++) {
			held = "\002" i "\002"
			pos = index(out, held)
			if (pos > 0) {
				out = substr(out, 1, pos - 1) "<code>" code[i] "</code>" substr(out, pos + length(held))
			}
		}
		return out
	}
	function markup(s,   res, pre, mid, rest) {
		# [text](target)
		res = ""
		while (match(s, /\[[^]]*\]\([^)]*\)/)) {
			pre = substr(s, 1, RSTART - 1)
			mid = substr(s, RSTART, RLENGTH)
			rest = substr(s, RSTART + RLENGTH)
			text = substr(mid, 2, index(mid, "](") - 2)
			target = substr(mid, index(mid, "](") + 2, length(mid) - index(mid, "](") - 2)
			res = res bold(pre) "<a href=\"" href(target) "\">" bold(text) "</a>"
			s = rest
		}
		return res bold(s)
	}
	function bold(s) {
		while (match(s, /\*\*[^*]+\*\*/)) {
			s = substr(s, 1, RSTART - 1) "<strong>" substr(s, RSTART + 2, RLENGTH - 4) "</strong>" substr(s, RSTART + RLENGTH)
		}
		return em(s)
	}
	# Emphasis, after bold so a ** is never read as two single stars. The
	# pattern deliberately refuses a star followed or preceded by a space:
	# `3 * 4` and a lone `wsrep_*` are content, and a renderer that pairs them
	# eats the rest of the line and still produces a plausible page.
	function em(s) {
		while (match(s, /\*[^ \t*][^*]*\*/)) {
			s = substr(s, 1, RSTART - 1) "<em>" substr(s, RSTART + 1, RLENGTH - 2) "</em>" substr(s, RSTART + RLENGTH)
		}
		return s
	}
	function slugify(s,   t) {
		t = tolower(s)
		gsub(/<[^>]*>/, "", t)
		gsub(/[^a-z0-9]+/, "-", t)
		gsub(/^-+|-+$/, "", t)
		return t
	}
	# Markdown authors wrap paragraphs and list items, and the markup does not
	# care: a **bold** span may start on one line and close on the next. So
	# text is buffered until the block ends and the inline pass runs on the
	# whole thing — line by line would leave the markers dangling at both ends
	# and still produce a page that looks almost right.
	function buffer(line) {
		if (buf == "") buf = line
		else buf = buf " " line
	}
	function flushpara() {
		if (buf == "") return
		print inline(esc(buf))
		buf = ""
	}
	function flushitem() {
		if (buf == "") return
		print "<li>" inline(esc(buf)) "</li>"
		buf = ""
	}
	function closeblocks() {
		if (inlist) { flushitem(); print "</ul>"; inlist = 0 }
		if (inpara) { flushpara(); print "</p>"; inpara = 0 }
		if (intable) { print "</tbody></table></div>"; intable = 0 }
		if (inquote) { flushpara(); print "</blockquote>"; inquote = 0 }
	}
	# A table row, split on unescaped pipes: an escaped one is content, and it
	# has broken a generator in this family before.
	function cells(line,   n, i, raw, parts, res) {
		gsub(/^[ \t]*\|/, "", line)
		gsub(/\|[ \t]*$/, "", line)
		gsub(/\\\|/, "\001", line)
		n = split(line, parts, "|")
		for (i = 1; i <= n; i++) {
			gsub(/\001/, "|", parts[i])
			gsub(/^[ \t]+|[ \t]+$/, "", parts[i])
			cell[i] = inline(esc(parts[i]))
		}
		return n
	}

	BEGIN { print "@@BODY@@" }

	/^```/ {
		if (infence) { print "</code></pre>"; infence = 0; next }
		closeblocks()
		infence = 1
		print "<pre><code>"
		next
	}
	infence { print esc($0); next }

	/^#{1,6} / {
		closeblocks()
		level = length($0) - length(substr($0, match($0, / /) + 1)) - 1
		# match() above gives the first space; the marker length is that index.
		level = index($0, " ") - 1
		text = substr($0, level + 2)
		html = inline(esc(text))
		if (level == 1) {
			if (title == "") title = text
			print "<h1>" html "</h1>"
		} else {
			id = slugify(text)
			printf "<h%d id=\"%s\">%s</h%d>\n", level, id, html, level
		}
		next
	}

	/^[ \t]*\|/ {
		# The alignment row carries no content.
		if ($0 ~ /^[ \t]*\|[ \t:|-]+\|?[ \t]*$/ && intable) { next }
		if (!intable) {
			closeblocks()
			intable = 1
			n = cells($0)
			print "<div class=\"scroll\"><table><thead><tr>"
			for (i = 1; i <= n; i++) print "<th>" cell[i] "</th>"
			print "</tr></thead><tbody>"
			next
		}
		n = cells($0)
		print "<tr>"
		for (i = 1; i <= n; i++) print "<td>" cell[i] "</td>"
		print "</tr>"
		next
	}

	/^[-*] / {
		if (inpara) { flushpara(); print "</p>"; inpara = 0 }
		if (!inlist) { closeblocks(); inlist = 1; print "<ul>" }
		flushitem()
		buffer(substr($0, 3))
		next
	}

	/^> / {
		if (!inquote) { closeblocks(); inquote = 1; print "<blockquote>" }
		buffer(substr($0, 3))
		next
	}

	/^[ \t]*$/ { closeblocks(); next }
	/^---+[ \t]*$/ { closeblocks(); print "<hr>"; next }

	{
		# A continuation line: it belongs to whatever block is open. Inside a
		# list it continues the current item, which is how a wrapped bullet
		# keeps its markup.
		sub(/^[ \t]+/, "", $0)
		if (intable) { print inline(esc($0)); next }
		if (inlist || inquote) { buffer($0); next }
		if (!inpara) { print "<p>"; inpara = 1 }
		buffer($0)
	}

	END {
		if (infence) print "</code></pre>"
		closeblocks()
		print "@@TITLE@@" (title == "" ? slug : title)
	}
	' "$1"
}

page() {
	file=$1
	slug=$(basename "$file" .md)
	render "$file" "$slug" >"$tmp/body.html"
	title=$(sed -n 's/^@@TITLE@@//p' "$tmp/body.html")
	[ -n "$title" ] || title="$slug"

	{
		cat <<EOF
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>$title — galera-doctor</title>
<meta name="description" content="$title — galera-doctor, a read-only audit of a MariaDB/MySQL Galera cluster.">
<link rel="canonical" href="$base$slug.html">
<meta name="robots" content="index, follow">
<meta name="theme-color" content="#0b1120">
<link rel="icon" href="../mark.svg" type="image/svg+xml">
<meta property="og:title" content="$title — galera-doctor">
<meta property="og:description" content="galera-doctor: the Galera states no wsrep_* counter can show you.">
<meta property="og:type" content="article">
<meta property="og:url" content="$base$slug.html">
<meta property="og:image" content="https://allan-nava.github.io/galera-doctor/og-image.png">
<meta name="twitter:card" content="summary_large_image">
<link rel="stylesheet" href="docs.css">
</head>
<body>
<nav>
  <a class="home" href="../"><img src="../mark.svg" alt="" width="26" height="26"> galera-doctor</a>
  <a href="https://github.com/Allan-Nava/galera-doctor">GitHub</a>
</nav>
<main>
EOF
		sed -e '/^@@BODY@@$/d' -e '/^@@TITLE@@/d' "$tmp/body.html"
		cat <<EOF
</main>
<footer>
  <a href="../">galera-doctor</a> ·
  <a href="https://github.com/Allan-Nava/galera-doctor/blob/main/docs/$slug.md">this page on GitHub</a> ·
  MIT
</footer>
</body>
</html>
EOF
	} >"$out/$slug.html"
	echo "  $slug.html"
}

build() {
	mkdir -p "$out"
	# A page whose source is gone must not linger on the site.
	for old in "$out"/*.html; do
		[ -e "$old" ] || continue
		slug=$(basename "$old" .html)
		[ -f "$src/$slug.md" ] || rm -f "$old"
	done
	for file in "$src"/*.md; do
		[ -e "$file" ] || continue
		page "$file"
	done
	stylesheet >"$out/docs.css"
	echo "rendered $(ls -1 "$out"/*.html 2>/dev/null | wc -l | tr -d ' ') page(s) into $out"
}

check() {
	mine="$tmp/out"
	mkdir -p "$mine"
	OUT_DIR="$mine" SRC_DIR="$src" sh "$0" build >/dev/null
	if ! diff -ru "$out" "$mine" >"$tmp/diff.txt" 2>&1; then
		sed 's/^/  /' "$tmp/diff.txt" >&2
		echo "" >&2
		echo "site/docs is stale: run scripts/docs.sh build and commit the result" >&2
		exit 1
	fi
	echo "site/docs is up to date"
}

# The doc pages share one stylesheet, generated with them so there is no third
# place to keep the palette.
stylesheet() {
	cat <<'EOF'
/* Generated by scripts/docs.sh — do not edit. */
:root {
  --bg: #0b1120; --bg-soft: #111a2e; --panel: #0f172a; --line: #1e293b;
  --fg: #e2e8f0; --fg-dim: #94a3b8; --fg-faint: #64748b; --green: #10b981;
  --mono: ui-monospace, SFMono-Regular, Menlo, Consolas, "DejaVu Sans Mono", monospace;
  --sans: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Helvetica, Arial, sans-serif;
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.7 var(--sans); }
a { color: var(--green); text-decoration: none; }
a:hover { text-decoration: underline; }
code, pre { font-family: var(--mono); }
nav {
  display: flex; align-items: center; justify-content: space-between; gap: 16px;
  max-width: 860px; margin: 0 auto; padding: 18px 22px; border-bottom: 1px solid var(--line);
}
nav .home { display: flex; align-items: center; gap: 9px; color: var(--fg); font-weight: 600; font-family: var(--mono); }
main { max-width: 860px; margin: 0 auto; padding: 34px 22px 10px; }
h1 { font-family: var(--mono); font-size: clamp(26px, 5vw, 36px); letter-spacing: -.02em; margin: 0 0 22px; }
h2 { font-size: 21px; margin: 42px 0 12px; padding-bottom: 8px; border-bottom: 1px solid var(--line); }
h3 { font-size: 17px; margin: 30px 0 8px; }
h2 code, h3 code { color: var(--green); font-size: .95em; }
p { margin: 0 0 16px; }
ul { padding-left: 22px; }
li { margin: 6px 0; }
p code, li code, td code, th code { background: var(--bg-soft); border: 1px solid var(--line); border-radius: 5px; padding: 1px 5px; font-size: 13.5px; }
pre {
  background: var(--panel); border: 1px solid var(--line); border-radius: 12px;
  padding: 15px 17px; overflow-x: auto; font-size: 13.5px; line-height: 1.6; margin: 0 0 18px;
}
pre code { background: none; border: 0; padding: 0; font-size: inherit; }
blockquote { margin: 0 0 18px; padding: 2px 0 2px 16px; border-left: 3px solid var(--green); color: var(--fg-dim); }
.scroll { overflow-x: auto; margin: 0 0 18px; }
table { width: 100%; border-collapse: collapse; font-size: 14.5px; }
td, th { text-align: left; vertical-align: top; padding: 10px 12px; border-bottom: 1px solid var(--line); }
th { color: var(--fg-faint); font-size: 12px; letter-spacing: .08em; text-transform: uppercase; }
hr { border: 0; border-top: 1px solid var(--line); margin: 30px 0; }
footer {
  max-width: 860px; margin: 40px auto 0; padding: 22px; border-top: 1px solid var(--line);
  color: var(--fg-faint); font-size: 13.5px;
}
footer a { color: var(--fg-dim); }
@media (prefers-color-scheme: light) {
  :root:not([data-theme="dark"]) {
    --bg: #ffffff; --bg-soft: #f8fafc; --panel: #0f172a; --line: #e2e8f0;
    --fg: #0f172a; --fg-dim: #475569; --fg-faint: #64748b;
  }
  :root:not([data-theme="light"]) pre code { color: #e2e8f0; }
}
EOF
}

case "${1:-check}" in
build) build ;;
check) check ;;
css) stylesheet ;;
*)
	echo "usage: scripts/docs.sh [build|check]" >&2
	exit 2
	;;
esac
