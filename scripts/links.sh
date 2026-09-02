#!/bin/sh
# links.sh — check that every local link in the docs and on the site resolves.
#
# The three places a link rots here are a Markdown file linking a sibling doc, a
# doc linking a file that moved, and site/index.html linking a repository path
# through github.com — the last one being the easy one to get wrong, because it
# looks like an external URL and is really a path in this tree.
#
#   scripts/links.sh          check every tracked .md file and site/index.html
#   scripts/links.sh FILE...  check only these files
#
# Anchors are stripped, not verified: a heading rename is a smaller failure than
# a moved file, and verifying them needs a Markdown parser this repository is not
# going to grow. POSIX sh and awk only.

set -eu

repo_url="https://github.com/Allan-Nava/galera-doctor/blob/main/"

if [ "$#" -gt 0 ]; then
	set -- "$@"
else
	# Tracked files only: a scratch note in the working tree is not the docs.
	# shellcheck disable=SC2046
	set -- $(git ls-files '*.md' 'site/*.html')
fi

fail=0
checked=0

for file in "$@"; do
	[ -f "$file" ] || { echo "$file: not found" >&2; fail=1; continue; }
	dir=$(dirname "$file")

	# Markdown ](target), HTML href="target" and src="target", one per line.
	targets=$(sed -e 's/](/\n](/g' -e 's/href="/\nhref="/g' -e 's/src="/\nsrc="/g' "$file" |
		sed -n -e 's/^](\([^)]*\)).*/\1/p' -e 's/^href="\([^"]*\)".*/\1/p' -e 's/^src="\([^"]*\)".*/\1/p')

	for target in $targets; do
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
	done
done

if [ "$fail" -ne 0 ]; then
	echo "" >&2
	echo "a link points at something that is not in the tree" >&2
	exit 1
fi
echo "$checked local link(s) resolve across $# file(s)"
