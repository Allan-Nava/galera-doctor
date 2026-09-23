#!/bin/sh
# coverage_test.sh — tests for scripts/coverage.sh.
#
# The gate this tests is itself a reaction to a gate that did not exist: CI
# printed the total coverage on every run and compared it with nothing, so the
# figure fell for several releases and no build went red (GD-85). A test for
# the thing that fixes that has one job above all others — prove it can fail.
#
# The script is driven off COVER_OUTPUT and FLOORS_FILE so nothing here runs
# `go test` or reads this repository's real numbers: a test whose fixture is
# the code under test passes for the wrong reason the day the code changes.
#
# POSIX sh only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/coverage.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-coverage-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

pass() {
	checks=$((checks + 1))
	echo "ok   $1"
}

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
	[ -f "$tmp/out.txt" ] && sed 's/^/       /' "$tmp/out.txt" >&2
	return 0
}

# floors <lines...> — the floor table the script reads: "<package> <minimum>".
floors() {
	printf '%s\n' "$@" >"$tmp/floors"
}

# cover <lines...> — a captured `go test -cover ./...` output.
cover() {
	printf '%s\n' "$@" >"$tmp/cover"
}

run() {
	COVER_OUTPUT="$tmp/cover" FLOORS_FILE="$tmp/floors" sh "$script" >"$tmp/out.txt" 2>&1
}

# assert_pass <name>
assert_pass() {
	checks=$((checks + 1))
	if run; then
		echo "ok   $1"
	else
		failures=$((failures + 1))
		echo "FAIL: $1 — the gate rejected a tree it should accept" >&2
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
}

# assert_fail <name> <needle>
assert_fail() {
	checks=$((checks + 1))
	if run; then
		failures=$((failures + 1))
		echo "FAIL: $1 — the gate passed" >&2
		sed 's/^/       /' "$tmp/out.txt" >&2
	elif ! grep -q "$2" "$tmp/out.txt"; then
		failures=$((failures + 1))
		echo "FAIL: $1 — it failed without saying $2" >&2
		sed 's/^/       /' "$tmp/out.txt" >&2
	else
		echo "ok   $1"
	fi
}

ok_line() { printf 'ok  \t%s\t0.2s\tcoverage: %s%% of statements' "$1" "$2"; }

# --- a tree that meets every floor -------------------------------------------

floors "example/a 90.0" "example/b 20.0"
cover "$(ok_line example/a 96.6)" "$(ok_line example/b 23.7)"
assert_pass "every package above its floor passes"

if grep -q '96.6' "$tmp/out.txt" && grep -q 'example/a' "$tmp/out.txt"; then
	pass "it reports what it measured, not just a verdict"
else
	fail "the output does not show the per-package numbers"
fi

# Exactly on the floor is not below it. Coverage lands on round numbers often
# enough that an off-by-one here would make the gate flap.
floors "example/a 96.6"
cover "$(ok_line example/a 96.6)"
assert_pass "a package exactly on its floor passes"

# --- the case this whole script exists for -----------------------------------

floors "example/a 90.0" "example/b 20.0"
cover "$(ok_line example/a 84.2)" "$(ok_line example/b 23.7)"
assert_fail "a package below its floor fails the build" "example/a"
if grep -q '90' "$tmp/out.txt" && grep -q '84.2' "$tmp/out.txt"; then
	pass "and it says both the floor and what was measured"
else
	fail "the message does not carry both numbers"
fi

# A tenth of a point below is below. Rounding in the gate's favour is how a
# floor becomes a suggestion.
floors "example/a 90.0"
cover "$(ok_line example/a 89.9)"
assert_fail "a tenth of a point below the floor still fails" "example/a"

# --- the ways this gate would quietly stop covering things -------------------

floors "example/a 90.0"
cover "$(ok_line example/a 96.6)" "$(ok_line example/new 41.0)"
assert_fail "a package with no floor declared fails" "example/new"

floors "example/a 90.0" "example/gone 50.0"
cover "$(ok_line example/a 96.6)"
assert_fail "a floor for a package that no longer exists fails" "example/gone"

# A package with no test files reports no percentage at all. Reading that as
# 0%, or skipping the line, are both wrong: it is a package nobody tested.
floors "example/a 90.0" "example/bare 10.0"
cover "$(ok_line example/a 96.6)" "$(printf '?   \texample/bare\t[no test files]')"
assert_fail "a package with no test files is not silently skipped" "example/bare"

# --- a test run that did not actually run ------------------------------------
#
# The worst outcome is a gate that reads a broken build as a clean one. A FAIL
# line, or a build error, must not be parsed into a percentage.

floors "example/a 90.0"
cover "$(printf 'FAIL\texample/a [build failed]')"
assert_fail "a build failure is not a coverage result" "example/a"

floors "example/a 90.0"
cover "$(printf 'FAIL\texample/a\t0.2s\tcoverage: 96.6%% of statements')"
assert_fail "a failing test suite is not a pass, whatever its coverage" "example/a"

floors "example/a 90.0"
cover ""
assert_fail "empty output is not a clean run" "example/a"

# --- the floor that has been left behind --------------------------------------
#
# A note and not a failure: the gate's job is to stop a fall, and raising a
# floor is a decision with a commit behind it. But a floor twenty points under
# the real number stops catching anything long before anybody notices.

floors "example/a 60.0"
cover "$(ok_line example/a 96.6)"
assert_pass "a floor far under the real number still passes"
if grep -qi 'note' "$tmp/out.txt" && grep -q 'example/a' "$tmp/out.txt"; then
	pass "but the script says the floor has been left behind"
else
	fail "a floor 36 points low was not mentioned"
fi

floors "example/a 95.0"
cover "$(ok_line example/a 96.6)"
assert_pass "a floor just under the real number passes"
if grep -qi 'note' "$tmp/out.txt"; then
	fail "a floor 1.6 points low should not produce a note"
else
	pass "and produces no note: it is doing its job"
fi

# --- every awk on this machine ------------------------------------------------
#
# The gate shipped once having passed here and died on the runner: `next` in a
# BEGIN action is undefined in POSIX, tolerated by the awk on macOS and a hard
# error in gawk. A whole suite of green checks proved nothing about the only
# interpreter that was going to run it.
#
# So the checks above run again under every awk this machine has. On a laptop
# with one that is one extra pass; on CI, and on anybody with gawk or mawk
# installed, it is the difference between catching that class of bug here and
# catching it after a push.

# AWK names one command, not a command line, so the candidates are binaries.
# busybox awk needs no entry of its own: on a system where it is the awk, it
# is what the first candidate runs.
for bin in awk gawk mawk original-awk; do
	command -v "$bin" >/dev/null 2>&1 || continue

	floors "example/a 90.0"
	cover "$(ok_line example/a 96.6)"
	checks=$((checks + 1))
	if COVER_OUTPUT="$tmp/cover" FLOORS_FILE="$tmp/floors" AWK="$bin" \
		sh "$script" >"$tmp/out.txt" 2>&1; then
		echo "ok   it accepts a passing tree under $bin"
	else
		failures=$((failures + 1))
		echo "FAIL: $candidate rejected a tree it should accept" >&2
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi

	# The half that matters: an interpreter that errors out early exits
	# non-zero too, so only asserting the failure would call a broken script
	# a working gate.
	floors "example/a 99.0"
	cover "$(ok_line example/a 96.6)"
	checks=$((checks + 1))
	if COVER_OUTPUT="$tmp/cover" FLOORS_FILE="$tmp/floors" AWK="$bin" \
		sh "$script" >"$tmp/out.txt" 2>&1; then
		failures=$((failures + 1))
		echo "FAIL: $candidate passed a package below its floor" >&2
	elif grep -q 'below its floor' "$tmp/out.txt"; then
		echo "ok   it fails a low package under $bin, for the right reason"
	else
		failures=$((failures + 1))
		echo "FAIL: $candidate failed without grading anything — the program did not run" >&2
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
done

# --- the real floors, against the real packages -------------------------------
#
# The one fixture that cannot go stale. Every other test here proves the gate
# works; this proves it is wired to this repository — a floor table naming
# packages that were renamed months ago passes every test above and checks
# nothing.

checks=$((checks + 1))
if ! command -v go >/dev/null 2>&1; then
	# Said out loud rather than skipped in silence: this is the one check here
	# that touches the real packages, and a run without it has not verified
	# that the floor table still names them.
	echo "ok   the checked-in floors (skipped: no go toolchain on this machine)"
elif FLOORS_FILE="" COVER_OUTPUT="" sh "$script" >"$tmp/real.txt" 2>&1; then
	echo "ok   the checked-in floors hold against a real go test run"
else
	failures=$((failures + 1))
	echo "FAIL: the checked-in floors do not hold" >&2
	sed 's/^/       /' "$tmp/real.txt" >&2
fi

echo ""
if [ "$failures" -eq 0 ]; then
	echo "all $checks checks passed"
else
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
