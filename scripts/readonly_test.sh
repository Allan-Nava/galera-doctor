#!/bin/sh
# readonly_test.sh — tests for scripts/readonly.sh, the gate that keeps this
# tool read-only.
#
# The gate has two ways to fail, and they are not equal. A false negative lets
# a writing statement into a tool whose entire promise is that it does not
# write — unacceptable. A false positive fails the build on prose, which is
# what happened on v1.3.0: `"ON UPDATE "+onUpdate` renders a cascading
# constraint for a human, and a hint explains what `CREATE TABLE` does without
# an explicit charset. Neither is a statement, and a red build over prose is
# how a gate gets loosened by somebody in a hurry.
#
# So the rule is sharper than "the word appears somewhere": a statement *begins*
# with its verb. Both directions are tested here, and the two lines from the
# real tree are fixtures rather than a memory.
#
# POSIX sh and awk only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/readonly.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-readonly-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}
pass() { echo "ok   $1"; }

# fixture <name> <content>  → writes $tmp/tree/<name>.go
fixture() {
	mkdir -p "$tmp/tree"
	printf '%s\n' "$2" >"$tmp/tree/$1"
}

# assert_caught <name> <needle-in-output>
assert_caught() {
	checks=$((checks + 1))
	if (cd "$tmp" && SCAN_DIRS=tree sh "$script") >"$tmp/out.txt" 2>&1; then
		fail "$1 — the gate let it through"
		sed 's/^/       /' "$tmp/out.txt" >&2
		return
	fi
	if grep -qF -e "$2" "$tmp/out.txt"; then
		pass "$1"
	else
		fail "$1 — caught, but the message does not name it: wanted $2"
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
}

# assert_allowed <name>
assert_allowed() {
	checks=$((checks + 1))
	if (cd "$tmp" && SCAN_DIRS=tree sh "$script") >"$tmp/out.txt" 2>&1; then
		pass "$1"
	else
		fail "$1 — the gate fired on something that is not a statement"
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
}

# ---------------------------------------------------------------------------
# What has to be caught. Every one of these would send a write.
# ---------------------------------------------------------------------------
rm -rf "$tmp/tree"
fixture write.go 'package x

func f(db *sql.DB) {
	Query(ctx, db, "UPDATE mysql.user SET x = 1")
}'
assert_caught "an UPDATE in a query string is caught" "UPDATE mysql.user"

rm -rf "$tmp/tree"
fixture write.go 'package x

func f(db *sql.DB) {
	Query(ctx, db, `
		DELETE FROM app.events
		 WHERE ts < NOW()`)
}'
assert_caught "a writing statement inside a raw string, on its own line, is caught" "DELETE FROM app.events"

rm -rf "$tmp/tree"
fixture write.go 'package x

func f(db *sql.DB) {
	db.Exec("SELECT 1")
}'
assert_caught "db.Exec is caught whatever it is given" "Exec"

rm -rf "$tmp/tree"
fixture write.go 'package x

func f(tx *sql.Tx) {
	tx.ExecContext(ctx, "SELECT 1")
}'
assert_caught "tx.ExecContext too" "Exec"

rm -rf "$tmp/tree"
fixture write.go 'package x

func f(db *sql.DB) {
	Query(ctx, db, "SET GLOBAL wsrep_desync = ON")
}'
assert_caught "SET GLOBAL is caught" "SET GLOBAL"

rm -rf "$tmp/tree"
fixture write.go 'package x

func f(db *sql.DB) {
	Query(ctx, db, "  \n  FLUSH TABLES")
}'
assert_caught "leading whitespace does not hide a statement" "FLUSH"

# ---------------------------------------------------------------------------
# What must not be caught. These are the lines that broke the build on v1.3.0,
# plus the shapes around them.
# ---------------------------------------------------------------------------
rm -rf "$tmp/tree"
fixture prose.go 'package x

// cascadingKeys renders a constraint for a person to read.
func f() {
	actions = append(actions, "ON UPDATE "+onUpdate)
	actions = append(actions, "ON DELETE "+onDelete)
}'
assert_allowed "a constraint rendered for a human is not a statement"

rm -rf "$tmp/tree"
fixture prose.go 'package x

var hint = "a CREATE TABLE without an explicit charset is a different table depending on which node ran it"
'
assert_allowed "a hint that mentions CREATE TABLE is not a statement"

rm -rf "$tmp/tree"
fixture prose.go 'package x

// A DDL run against the wrong node builds a different table: an ALTER here is
// applied and never replicated, and a DROP would be too.
func f() {}'
assert_allowed "a comment explaining what an ALTER does is not a statement"

rm -rf "$tmp/tree"
fixture reads.go 'package x

func f(db *sql.DB) {
	Query(ctx, db, "SHOW GLOBAL STATUS")
	Query(ctx, db, `
		SELECT MAX(finished_at)
		  FROM ops.backups`)
}'
assert_allowed "the statements this tool does send are allowed"

# A test file may do anything: the write-refusal test has to try a write.
rm -rf "$tmp/tree"
fixture write_test.go 'package x

func TestQueryRefusesAWrite(t *testing.T) {
	if _, err := Query(ctx, db, "DELETE FROM mysql.user"); err == nil {
		t.Fatal("a write must be refused")
	}
}'
assert_allowed "a _test.go file may try a write, which is how the refusal is tested"

# ---------------------------------------------------------------------------
# And the real tree, which is the only fixture that cannot go stale.
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if (cd "$root" && sh "$script") >"$tmp/real.txt" 2>&1; then
	pass "this repository is read-only"
else
	fail "the gate fires on this repository"
	sed 's/^/       /' "$tmp/real.txt" >&2
fi

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
echo "all $checks checks passed"
