#!/bin/sh
# links.sh — check that every local link in the docs and on the site resolves.
#
# The places a link rots here are a Markdown file linking a sibling doc, a doc
# linking a file that moved, the <picture> element in the README whose dark
# variant lives in a `srcset`, and site/index.html linking a repository path
# through github.com — the last one being the easy one to get wrong, because it
# looks like an external URL and is really a path in this tree.
#
#   scripts/links.sh          check every tracked .md file and site/*.html
#   scripts/links.sh FILE...  check only these files
#
# Anchors are stripped, not verified: a heading rename is a smaller failure than
# a moved file, and verifying them needs a Markdown parser this repository is not
# going to grow. POSIX sh and awk only. Tests: scripts/links_test.sh.

set -eu

repo_url="https://github.com/Allan-Nava/galera-doctor/blob/main/"

if [ "$#" -eq 0 ]; then
	# Tracked files only: a scratch note in the working tree is not the docs.
	# shellcheck disable=SC2046
	set -- $(git ls-files '*.md' '*.html')
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-links.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

# Every link target in the file, one per line. Extracting these with awk rather
# than a pipeline of sed substitutions is what makes a target containing a space
# — a Markdown title, an srcset descriptor — a target rather than two.
extract() {
	awk '
	function emit(t) {
		sub(/^[ \t]+/, "", t); sub(/[ \t]+$/, "", t)
		if (t != "") print t
	}
	# A Markdown target may carry a title — [x](file.md "Title") — and may be
	# wrapped in angle brackets when it contains a space.
	function md(t) {
		if (t ~ /^</) { sub(/^</, "", t); sub(/>.*$/, "", t); emit(t); return }
		sub(/[ \t].*$/, "", t)
		emit(t)
	}
	# srcset is a comma-separated candidate list, each "url [descriptor]".
	function srcset(v,   n, i, parts) {
		n = split(v, parts, ",")
		for (i = 1; i <= n; i++) md(parts[i])
	}
	# A fenced block and an inline code span are examples, not links — and the
	# CHANGELOG entry for this bug quotes the construct that caused it.
	/^[ \t]*(```|~~~)/ { fenced = !fenced; next }
	fenced { next }
	{
		# Drop paired code spans, longest run of backticks first.
		gsub(/``[^`]*``/, " ", $0)
		gsub(/`[^`]*`/, " ", $0)
		line = $0
		while (match(line, /\]\([^)]*\)/)) {
			md(substr(line, RSTART + 2, RLENGTH - 3))
			line = substr(line, RSTART + RLENGTH)
		}
		line = $0
		while (match(line, /(href|src|srcset)="[^"]*"/)) {
			attr = substr(line, RSTART, RLENGTH)
			eq = index(attr, "=")
			name = substr(attr, 1, eq - 1)
			value = substr(attr, eq + 2, length(attr) - eq - 2)
			if (name == "srcset") srcset(value); else md(value)
			line = substr(line, RSTART + RLENGTH)
		}
	}
	' "$1"
}

fail=0
checked=0

for file in "$@"; do
	[ -f "$file" ] || { echo "$file: not found" >&2; fail=1; continue; }
	dir=$(dirname "$file")

	extract "$file" >"$tmp/targets"
	while IFS= read -r target; do
		case "$target" in
		"$repo_url"*)
			# A link into this repository's own tree: the path has to exist.
			path=${target#"$repo_url"}
			path=${path%%#*}
			;;
		http*://* | mailto:* | \#* | data:*) continue ;;
		*)
			path=${target%%#*}
			[ -n "$path" ] || continue
			case "$path" in
			/*) path=".$path" ;;
			*) path="$dir/$path" ;;
			esac
			;;
		esac

		checked=$((checked + 1))
		if [ ! -e "$path" ]; then
			echo "$file: broken link to $target" >&2
			fail=1
		fi
	done <"$tmp/targets"
done

if [ "$fail" -ne 0 ]; then
	echo "" >&2
	echo "a link points at something that is not in the tree" >&2
	exit 1
fi
echo "$checked local link(s) resolve across $# file(s)"
