#!/bin/sh
# og_test.sh — tests for scripts/og.sh.
#
# og.sh was the one script in scripts/ without one (GD-78), which is the worst
# place for the gap: it regenerates a binary file nobody reviews in a diff, and
# the thing that would go wrong — the wrong window size, so the preview card is
# silently cropped or letterboxed by every site that renders it — is invisible
# until somebody shares a link.
#
# Chrome is not run. A fake on CHROME records the arguments it was given, which
# is the whole of what this script decides; that the *checked-in* PNG is
# 1200x630 is seo_test.sh's assertion, and the last test here is that the two
# numbers cannot drift apart.
#
# POSIX sh only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/og.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-og-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}

pass() {
	checks=$((checks + 1))
	echo "ok   $1"
}

check() {
	checks=$((checks + 1))
	if [ "$2" = "$3" ]; then
		echo "ok   $1"
	else
		failures=$((failures + 1))
		echo "FAIL: $1" >&2
		echo "       want: $3" >&2
		echo "       got:  $2" >&2
	fi
}

# fake_chrome <mode> — writes $tmp/chrome, which records its arguments in
# $tmp/args and then behaves as told: "ok" produces the screenshot it was
# asked for, "silent" produces nothing (the case where Chrome exits 0 and
# writes no file, which is what a bad --screenshot path looks like).
fake_chrome() {
	mode=$1
	cat >"$tmp/chrome" <<EOF
#!/bin/sh
printf '%s\n' "\$@" >"$tmp/args"
if [ "$mode" = ok ]; then
	for a in "\$@"; do
		case "\$a" in
			--screenshot=*) printf 'not really a png\n' >"\${a#--screenshot=}" ;;
		esac
	done
fi
exit 0
EOF
	chmod +x "$tmp/chrome"
	: >"$tmp/args"
}

arg_matching() {
	grep -E "$1" "$tmp/args" 2>/dev/null | head -1 || true
}

# --- it renders, and says where ---------------------------------------------

fake_chrome ok
out="$tmp/card.png"
if CHROME="$tmp/chrome" sh "$script" "$out" >"$tmp/out.txt" 2>&1; then
	pass "it renders to the path it was given"
else
	fail "og.sh failed with a working Chrome"
	sed 's/^/       /' "$tmp/out.txt" >&2
fi

if [ -f "$out" ]; then
	pass "the screenshot is where the caller asked for it"
else
	fail "no file at $out"
fi

if grep -q "wrote $out" "$tmp/out.txt"; then
	pass "it says what it wrote"
else
	fail "og.sh did not report the file it wrote: $(cat "$tmp/out.txt")"
fi

# --- the arguments that decide the card -------------------------------------
#
# 1200x630 is the whole point: every card generator crops to it, and a card
# rendered at the browser's default size is the bug this test exists for.

check "the window is exactly 1200x630" "$(arg_matching '^--window-size=')" "--window-size=1200,630"
check "the device scale factor is 1, so 1200x630 is pixels" \
	"$(arg_matching '^--force-device-scale-factor=')" "--force-device-scale-factor=1"
check "the screenshot goes to the requested path" \
	"$(arg_matching '^--screenshot=')" "--screenshot=$out"

if [ -n "$(arg_matching '^--headless$')" ]; then
	pass "it runs headless"
else
	fail "og.sh did not pass --headless"
fi

# Rendered from assets/, because the mark is referenced relatively: pointing
# Chrome at the file from anywhere else gives a card with a missing logo.
url=$(arg_matching '^file://')
case "$url" in
	"file://$root/assets/og-image.html") pass "it renders assets/og-image.html from assets/" ;;
	*) fail "og.sh rendered $url, not assets/og-image.html" ;;
esac

if [ -n "$(arg_matching '^--virtual-time-budget=')" ]; then
	pass "it waits for the page before shooting"
else
	fail "og.sh did not pass --virtual-time-budget, so it can shoot a blank page"
fi

# --- Chrome that produces nothing --------------------------------------------

fake_chrome silent
if CHROME="$tmp/chrome" sh "$script" "$tmp/missing.png" >"$tmp/out.txt" 2>&1; then
	fail "a Chrome that wrote no screenshot was reported as success"
else
	pass "a Chrome that wrote no screenshot is a failure"
fi
if grep -qi 'no screenshot' "$tmp/out.txt"; then
	pass "and it says so"
else
	fail "the message does not say the screenshot is missing: $(cat "$tmp/out.txt")"
fi

# --- no Chrome ----------------------------------------------------------------
#
# CHROME naming a binary that is not there has to be the error, not a silent
# fall back to whichever browser the machine happens to have: a card rendered
# by a different engine than the one the person asked for is exactly the kind
# of difference nobody looks for in a PNG. This is also what makes the
# not-found path testable on a laptop that does have Chrome installed.

if CHROME="$tmp/nothing-here" sh "$script" "$tmp/none.png" >"$tmp/out.txt" 2>&1; then
	fail "a CHROME that does not exist was accepted"
else
	rc=$?
	check "an unusable CHROME exits 2" "$rc" "2"
fi
if grep -q 'CHROME' "$tmp/out.txt"; then
	pass "the message names CHROME, so the fix is obvious"
else
	fail "the message does not mention CHROME: $(cat "$tmp/out.txt")"
fi
if [ -f "$tmp/none.png" ]; then
	fail "og.sh left a file behind after failing"
else
	pass "it leaves nothing behind when it cannot run"
fi

# --- the size cannot drift from the one seo_test.sh asserts -------------------
#
# og.sh renders the PNG and seo_test.sh checks the PNG, and they agree on
# 1200x630 in two files that are edited months apart. If one moves, the other
# has to fail, and this is where.

if grep -q 'window-size=1200,630' "$script" && grep -q '1200 ?x ?630' "$root/scripts/seo_test.sh"; then
	pass "og.sh renders the size seo_test.sh asserts"
else
	fail "og.sh and seo_test.sh disagree about the card size"
fi

echo ""
if [ "$failures" -eq 0 ]; then
	echo "all $checks checks passed"
else
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
