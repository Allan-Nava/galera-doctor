#!/bin/sh
# links_test.sh — tests for scripts/links.sh.
#
# A link checker is a CI gate, and a gate has two ways to be useless: missing a
# broken link, and crying about a link that is fine. The second is worse — a
# gate that fires on a legal Markdown construct gets switched off, and then the
# first failure mode arrives for free.
#
# So the fixtures here are the constructs that are legal and look broken: a link
# with a title, a bare anchor, an image, a root-relative path, and the
# github.com/blob/main links on the landing page that look external and are
# really paths in this tree.
#
# POSIX sh and awk only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/links.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-links-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}

# assert_ok <name> <file>...
# Every link in these files resolves: the checker must pass and say nothing.
assert_ok() {
	name=$1
	shift
	checks=$((checks + 1))
	if (cd "$tmp" && sh "$script" "$@") >"$tmp/out.txt" 2>&1; then
		echo "ok   $name"
	else
		fail "$name — the checker reported a broken link"
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
}

# assert_broken <name> <needle> <file>...
# The checker must fail, and name the target it could not resolve.
assert_broken() {
	name=$1
	needle=$2
	shift 2
	checks=$((checks + 1))
	if (cd "$tmp" && sh "$script" "$@") >"$tmp/out.txt" 2>&1; then
		fail "$name — the checker passed"
		sed 's/^/       /' "$tmp/out.txt" >&2
		return
	fi
	if grep -qF "$needle" "$tmp/out.txt"; then
		echo "ok   $name"
	else
		fail "$name — failed for the wrong reason, wanted: $needle"
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
}

mkdir -p "$tmp/docs" "$tmp/site" "$tmp/assets"
: >"$tmp/docs/usage.md"
: >"$tmp/INTENT.md"
: >"$tmp/assets/logo.svg"

# ---------------------------------------------------------------------------
# Everything here is a link that resolves, written the way it is really written.
# ---------------------------------------------------------------------------
cat >"$tmp/good.md" <<'EOF'
# Fixture

A sibling doc: [usage](docs/usage.md) and the charter: [intent](INTENT.md).
An anchor into it: [rates](docs/usage.md#a-total-is-not-a-rate).
A bare anchor on this page: [above](#fixture).
A link with a title: [usage](docs/usage.md "the flags").
Something external: [Galera](https://galeracluster.com/) and <dev-ops@example.com>.
An image: <img src="assets/logo.svg" alt="">
A root-relative path: [charter](/INTENT.md).
A repository path through github.com:
[install](https://github.com/Allan-Nava/galera-doctor/blob/main/docs/usage.md)
EOF

assert_ok "the legal constructs all resolve" good.md

# ---------------------------------------------------------------------------
# One broken link per fixture, so a failure names the construct that broke.
# ---------------------------------------------------------------------------
printf '%s\n' '[gone](docs/gone.md)' >"$tmp/relative.md"
assert_broken "a relative link to a file that is not there" "docs/gone.md" relative.md

printf '%s\n' '[gone](https://github.com/Allan-Nava/galera-doctor/blob/main/docs/gone.md)' >"$tmp/blob.md"
assert_broken "a github.com/blob/main link to a path that is not there" "blob/main/docs/gone.md" blob.md

printf '%s\n' '<img src="assets/gone.svg" alt="">' >"$tmp/img.md"
assert_broken "an image that is not there" "assets/gone.svg" img.md

printf '%s\n' '[gone](../gone.md)' >"$tmp/docs/up.md"
assert_broken "a relative link resolved from the file's own directory" "../gone.md" docs/up.md

# A file that does not exist at all is not the same as a file with a bad link.
assert_broken "a file that cannot be read is reported" "not found" missing.md

# ---------------------------------------------------------------------------
# The <picture> element on the README: a source's srcset is a real path too.
# ---------------------------------------------------------------------------
cat >"$tmp/picture.md" <<'EOF'
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/gone-dark.svg">
  <img alt="galera-doctor" src="assets/logo.svg" width="560">
</picture>
EOF
assert_broken "a srcset pointing at a file that is not there" "assets/gone-dark.svg" picture.md

# ---------------------------------------------------------------------------
# Code is not a link. A CHANGELOG entry quoting the construct that broke the
# checker — `[x](file.md "Title")` — must not be read as a link to file.md, or
# documenting the bug reintroduces it.
# ---------------------------------------------------------------------------
cat >"$tmp/code.md" <<'EOF'
# Fixture

An inline example: `[x](gone.md "Title")` is a code span, not a link.
So is `<img src="gone.svg">`.

```console
$ galera-doctor audit
[gone](gone.md)
<a href="gone.html">also not a link</a>
```

And a real one on a line that also holds code: `--state` and [usage](docs/usage.md).
EOF
assert_ok "a link inside a code span or a fenced block is not a link" code.md

# ...and the real link on that last line is still checked, or the exemption is a
# hole rather than a rule.
cat >"$tmp/code-broken.md" <<'EOF'
An inline example: `[x](gone.md)`, and a real link: [gone](really-gone.md).
EOF
assert_broken "a real link beside a code span is still checked" "really-gone.md" code-broken.md

# ---------------------------------------------------------------------------
# The summary line is what a passing CI run shows: it has to say how much was
# actually checked, or a checker that silently checks nothing looks the same.
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if (cd "$tmp" && sh "$script" good.md) >"$tmp/out.txt" 2>&1 &&
	grep -qE '^[1-9][0-9]* local link\(s\) resolve' "$tmp/out.txt"; then
	echo "ok   the summary counts the links it checked"
else
	fail "the summary did not report a positive count"
	sed 's/^/       /' "$tmp/out.txt" >&2
fi

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
echo "all $checks checks passed"
