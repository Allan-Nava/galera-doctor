#!/bin/sh
# readonly.sh — the gate that keeps this tool read-only.
#
# Every query goes through cluster.Query, which refuses anything but SHOW and
# SELECT at run time. This is the other half: no writing statement, and no
# Exec, may appear in the source at all. The promise is meant to be a property
# of the code rather than a claim in a README, and a property needs something
# that checks it.
#
#   scripts/readonly.sh          scan cmd/ and internal/
#   SCAN_DIRS="tree" ...         scan somewhere else (the tests do)
#
# The rule is sharper than "the word appears somewhere in a string", which is
# what it used to be. That version failed the build on v1.3.0 over two pieces
# of prose — `"ON UPDATE "+onUpdate`, which renders a cascading constraint for
# a human, and a hint explaining what a CREATE TABLE does without an explicit
# charset — and a red build over prose is how a gate gets loosened by somebody
# in a hurry. A statement *begins* with its verb, so that is what is matched:
# at the start of a string literal, or at the start of a line inside a raw one.
#
# POSIX sh and grep only. Tests: scripts/readonly_test.sh.

set -eu

dirs="${SCAN_DIRS:-cmd internal}"

# The verbs that change something. LOCK TABLES and START TRANSACTION are in
# here because they take a lock this tool has no business taking.
verbs='INSERT|UPDATE|DELETE|REPLACE|DROP|ALTER|TRUNCATE|CREATE|RENAME|GRANT|REVOKE|SET GLOBAL|SET SESSION|FLUSH|LOCK TABLES|UNLOCK TABLES|START TRANSACTION|BEGIN|COMMIT|ROLLBACK|CALL|LOAD DATA|OPTIMIZE|REPAIR|ANALYZE TABLE|CHECK TABLE|KILL'

status=0
found=""

# 1. A string literal that starts with one of them: "UPDATE ...", `DELETE ...`,
#    and the same after escaped whitespace.
if hits=$(grep -rniE "(\"|\`)([[:space:]]|\\\\n|\\\\t)*($verbs)[[:space:]]" $dirs \
	--include='*.go' 2>/dev/null | grep -v '_test\.go'); then
	found="$found$hits
"
	status=1
fi

# 2. A line that begins with one of them, which in Go source only happens
#    inside a raw string — the shape every query in this repository is written
#    in.
if hits=$(grep -rniE "^[[:space:]]*($verbs)[[:space:]]" $dirs \
	--include='*.go' 2>/dev/null | grep -v '_test\.go'); then
	found="$found$hits
"
	status=1
fi

if [ "$status" -ne 0 ]; then
	echo "::error::a writing statement appears in the source: galera-doctor only issues SHOW and SELECT" >&2
	printf '%s' "$found" | grep . >&2
fi

# 3. Exec, whatever it is handed. A read-only auditor has no use for it.
if hits=$(grep -rnE '\b(db|tx|conn)\.Exec' $dirs --include='*.go' 2>/dev/null | grep -v '_test\.go'); then
	echo "::error::Exec has no business in a read-only auditor" >&2
	printf '%s\n' "$hits" >&2
	status=1
fi

if [ "$status" -eq 0 ]; then
	echo "read-only: no writing statement and no Exec in $dirs"
fi
exit "$status"
