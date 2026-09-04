#!/bin/sh
# docs_test.sh — tests for scripts/docs.sh, the docs renderer.
#
# A Markdown renderer written in awk is exactly the kind of tool that fails
# quietly: it produces a page that still looks plausible, with one table row
# eaten, one code block turned into prose, or one link pointing at a `.md` file
# nobody publishes. None of that fails a browser.
#
# So the fixtures here are the constructs this repository's docs actually use —
# tables, fenced console blocks, inline code containing HTML-significant
# characters, links between pages — plus the two that would corrupt a page
# silently: a `<` inside a code span, and a pipe inside a table cell.
#
# POSIX sh and awk only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/docs.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-docs-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}
pass() { echo "ok   $1"; }

# has <name> <needle> [file]
has() {
	file=${3:-$tmp/out/fixture.html}
	checks=$((checks + 1))
	if grep -qF -e "$2" "$file"; then pass "$1"; else
		fail "$1 — missing: $2"
		sed 's/^/       /' "$file" >&2
	fi
}

# absent <name> <needle> [file]
absent() {
	file=${3:-$tmp/out/fixture.html}
	checks=$((checks + 1))
	if grep -qF -e "$2" "$file"; then
		fail "$1 — did not expect: $2"
		sed 's/^/       /' "$file" >&2
	else
		pass "$1"
	fi
}

mkdir -p "$tmp/src" "$tmp/out"
: >"$tmp/src/other.md"

cat >"$tmp/src/fixture.md" <<'EOF'
# The one true heading

An opening paragraph with **bold**, *emphasis*, `inline code`, and a
[link to another page](other.md) plus an [external one](https://example.com/x).

Galera *does* replicate this, and a lone star in `proxysql/*` or a bare 3 * 4
must survive being left alone.

## A section

- **`sst/donor`** — a list item whose bold wraps a code span
- a list item with `code`
- another one

| What is wrong | Why no metric shows it |
|---|---|
| **A drifted table** | Galera does not replicate `mysql.*` maintenance |
| A pipe in a cell | `--profile a\|b` is legal |

```console
$ galera-doctor audit --node "sg-01=audit:***@tcp(10.11.1.5:3306)/"
BAD   cluster/uuid   compress   5 < 6 && 7 > 2
```

### A sub-section

A paragraph mentioning `a < b` and `x > y` and an & ampersand.

**A sentence in bold that a markdown author wrapped
across two lines** like every paragraph in these docs.

- [Usage](usage.md) — a wrapped list item whose **bold
  also spans the break**
EOF

checks=$((checks + 1))
if (cd "$tmp" && SRC_DIR="$tmp/src" OUT_DIR="$tmp/out" sh "$script" build) >"$tmp/build.txt" 2>&1; then
	pass "the docs build"
else
	fail "the build failed"
	sed 's/^/       /' "$tmp/build.txt" >&2
	echo "$failures of $checks checks failed" >&2
	exit 1
fi

checks=$((checks + 1))
if [ -f "$tmp/out/fixture.html" ]; then pass "a page is produced per source file"; else
	fail "no fixture.html"
	ls -la "$tmp/out" >&2
fi

# --- structure ---------------------------------------------------------------
has "the page is a document" "<!doctype html>"
has "the h1 comes from the first heading" "<h1>The one true heading</h1>"
has "the title comes from it too" "<title>The one true heading — galera-doctor</title>"
has "a canonical URL is set" '<link rel="canonical" href="https://allan-nava.github.io/galera-doctor/docs/fixture.html">'
has "sections become h2" "<h2"
has "sub-sections become h3" "<h3"
has "paragraphs are paragraphs" "<p>"
has "bold survives" "<strong>bold</strong>"
has "inline code survives" "<code>inline code</code>"
has "lists become lists" "<li>"
has "tables become tables" "<table>"
has "a table header is a header cell" "<th>"
has "a fenced block becomes pre" "<pre>"

# --- the ways it could corrupt a page silently -------------------------------
# Markdown in the source must never reach the browser as markup.
absent "no stray asterisks from bold" "**bold**"
has "emphasis becomes em" "<em>emphasis</em>"
# The docs are full of **`check/name`** — bold wrapping a code span. Taking
# code spans out first and running bold on the fragments leaves both markers
# stranded, in a page that otherwise looks finished.
has "bold wrapping a code span closes" "<strong><code>sst/donor</code></strong>"
has "emphasis mid-sentence too" "<em>does</em>"
absent "no stray asterisks from emphasis" "*emphasis*"
# A lone star is not an unclosed emphasis: leaving it alone is the only safe
# reading, and eating the rest of the line is the failure to avoid.
has "a lone star inside code is untouched" "proxysql/*"
has "arithmetic in prose survives" "3 * 4"
# Markdown authors wrap paragraphs; the markup does not care and neither may
# the renderer. Line-by-line inline processing leaves the ** dangling at both
# ends and produces a page that still looks almost right.
has "bold across a line break closes" "<strong>A sentence in bold that a markdown author wrapped across two lines</strong>"
has "bold across a break inside a list item closes" "<strong>bold also spans the break</strong>"
# ...anywhere outside a code block. Inside one, `audit:***@tcp(...)` is a
# redacted password and has to stay exactly as written — which is why this
# assertion strips <pre> blocks instead of grepping the whole page.
checks=$((checks + 1))
if awk '/<pre>/{p=1} /<\/pre>/{p=0; next} !p' "$tmp/out/fixture.html" | grep -qF -e "**"; then
	fail "a bold marker survived outside a code block"
	awk '/<pre>/{p=1} /<\/pre>/{p=0; next} !p' "$tmp/out/fixture.html" | grep -nF -e "**" | sed 's/^/       /' >&2
else
	pass "no dangling bold markers outside code blocks"
fi

# And the redacted password inside the block is untouched.
has "a redacted password in a code block is left alone" "audit:***@tcp"
absent "no stray backticks from code spans" "\`inline code\`"
absent "no unrendered heading marks" "## A section"

# HTML-significant characters inside code and prose have to be escaped, or the
# page silently loses everything after a stray <.
has "a less-than inside a code block is escaped" "5 &lt; 6"
has "an ampersand inside a code block is escaped" "&amp;&amp; 7 &gt; 2"
has "a less-than in prose is escaped" "a &lt; b"
has "an ampersand in prose is escaped" "an &amp; ampersand"

# A pipe inside a table cell is legal Markdown when escaped, and it broke the
# roadmap generator once already (GD-63 in the sibling tool).
has "an escaped pipe stays inside its cell" "--profile a|b"
checks=$((checks + 1))
if [ "$(grep -c '<tr>' "$tmp/out/fixture.html")" -eq 3 ]; then
	pass "the table has exactly its three rows"
else
	fail "the table has $(grep -c '<tr>' "$tmp/out/fixture.html") rows, want 3 (header + 2)"
fi

# --- links -------------------------------------------------------------------
has "a link between pages points at the published page" 'href="other.html"'
absent "and never at the source" 'href="other.md"'
has "an external link is left alone" 'href="https://example.com/x"'

# A link to a file that is not published has to leave the site rather than
# 404: the repository is where it lives.
cat >"$tmp/src/outside.md" <<'EOF'
# Outside

The charter is [INTENT.md](../INTENT.md) and the brief is [AGENTS.md](../AGENTS.md).
EOF
(cd "$tmp" && SRC_DIR="$tmp/src" OUT_DIR="$tmp/out" sh "$script" build) >/dev/null 2>&1
has "a link outside the docs goes to the repository" "https://github.com/Allan-Nava/galera-doctor/blob/main/INTENT.md" "$tmp/out/outside.html"

# --- the gate ----------------------------------------------------------------
checks=$((checks + 1))
if (cd "$tmp" && SRC_DIR="$tmp/src" OUT_DIR="$tmp/out" sh "$script" check) >"$tmp/check1.txt" 2>&1; then
	pass "a freshly built site passes the check"
else
	fail "check failed on a fresh build"
	sed 's/^/       /' "$tmp/check1.txt" >&2
fi

checks=$((checks + 1))
printf '\n## A section nobody rendered\n\nNew prose.\n' >>"$tmp/src/fixture.md"
if (cd "$tmp" && SRC_DIR="$tmp/src" OUT_DIR="$tmp/out" sh "$script" check) >"$tmp/check2.txt" 2>&1; then
	fail "an edited source passed the staleness check"
	sed 's/^/       /' "$tmp/check2.txt" >&2
else
	if grep -qF "docs.sh build" "$tmp/check2.txt"; then
		pass "a stale page fails the check and names the fix"
	else
		fail "the failure did not name scripts/docs.sh build"
		sed 's/^/       /' "$tmp/check2.txt" >&2
	fi
fi

# A page whose source is gone must not linger on the site.
checks=$((checks + 1))
rm "$tmp/src/outside.md"
(cd "$tmp" && SRC_DIR="$tmp/src" OUT_DIR="$tmp/out" sh "$script" build) >/dev/null 2>&1
if [ -f "$tmp/out/outside.html" ]; then
	fail "a page whose source was deleted is still published"
else
	pass "a deleted source removes its page"
fi

checks=$((checks + 1))
if (cd "$tmp" && SRC_DIR="$tmp/src" OUT_DIR="$tmp/out" sh "$script" nonsense) >/dev/null 2>&1; then
	fail "an unknown subcommand exited 0"
else
	pass "an unknown subcommand is a usage error"
fi

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
echo "all $checks checks passed"
