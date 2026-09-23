#!/bin/sh
# coverage.sh — a coverage floor per package, checked.
#
# CI printed the total coverage on every run since the first release and
# compared it with nothing (GD-85). A number that is printed and not asserted
# is decoration: the figure fell over several releases and no build went red.
#
# The floor is per package on purpose. internal/cluster sits low because its
# SQL belongs to the integration test behind GD_TEST_DSN and cannot be
# unit-tested honestly; internal/audit sits high because a drop there means a
# check shipped untested. One global number lets the second rot while the
# first holds the average up, which is the failure this is meant to catch.
#
# A floor is a ratchet, not a target. It goes up in a commit, with the tests
# that earned it — never down to make a build green.
#
#   ./scripts/coverage.sh          run go test and check the floors
#
# COVER_OUTPUT and FLOORS_FILE override the two inputs, which is how
# scripts/coverage_test.sh drives it off a fixture instead of this repository.
# AWK picks the implementation: the parsing below has to hold under the awk on
# a maintainer's Mac and the gawk in CI alike, and it did not — the first
# version used `next` in a BEGIN action, which one accepts and the other
# rejects outright, so the gate passed locally and died on the runner. The
# override is what lets the test say so before a push does.
#
# POSIX sh only.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

# The floors. Each is a couple of points under the measurement at the time it
# was set: close enough to catch a real fall, far enough not to flap on a
# refactor that moves ten statements around.
default_floors() {
	cat <<'EOF'
github.com/Allan-Nava/galera-doctor/cmd/galera-doctor 12.0
github.com/Allan-Nava/galera-doctor/internal/audit 94.0
github.com/Allan-Nava/galera-doctor/internal/cluster 22.0
github.com/Allan-Nava/galera-doctor/internal/config 88.0
github.com/Allan-Nava/galera-doctor/internal/finding 92.0
github.com/Allan-Nava/galera-doctor/internal/output 86.0
github.com/Allan-Nava/galera-doctor/internal/proxysql 55.0
github.com/Allan-Nava/galera-doctor/internal/state 88.0
EOF
}

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-coverage.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

if [ -n "${FLOORS_FILE:-}" ]; then
	cat "$FLOORS_FILE" >"$tmp/floors"
else
	default_floors >"$tmp/floors"
fi

if [ -n "${COVER_OUTPUT:-}" ]; then
	cat "$COVER_OUTPUT" >"$tmp/cover"
else
	# A failing suite still has to reach the parser below, which refuses to
	# read a FAIL line as a percentage — so the exit status is deliberately
	# not checked here.
	(cd "$root" && go test -cover ./... >"$tmp/cover" 2>&1) || true
fi

# One pass in awk: it has the float comparison, and shelling out to a
# comparison per package would make this the slowest gate in CI for no reason.
"${AWK:-awk}" -v floors="$tmp/floors" '
BEGIN {
	while ((getline line < floors) > 0) {
		if (line ~ /^[ \t]*($|#)/) continue
		n = split(line, f, /[ \t]+/)
		# continue, not next: next in a BEGIN action is undefined in POSIX,
		# tolerated by the awk on macOS and a hard error in gawk, which is
		# what CI runs. The whole gate died on it there while passing here.
		if (n < 2) { printf "coverage.sh: unreadable floor: %s\n", line > "/dev/stderr"; bad = 1; continue }
		floor[f[1]] = f[2] + 0
		declared[f[1]] = 1
	}
}

# "ok  \tpkg\t0.2s\tcoverage: 96.6% of statements", and the cached variant.
$1 == "ok" && /coverage:/ {
	pkg = $2
	for (i = 1; i <= NF; i++) if ($i == "coverage:") { pct = $(i+1); break }
	sub(/%$/, "", pct)
	seen[pkg] = 1
	got[pkg] = pct + 0
	next
}

# A package with no test files reports no percentage. It is not 0% and it is
# not nothing: it is a package nobody tested, and the floor table is where
# that gets decided rather than here.
$1 == "?" {
	seen[$2] = 1
	notested[$2] = 1
	next
}

# Anything that says FAIL is a run that did not happen, whatever else is on
# the line. Reading a percentage off a failing suite is how a gate reports a
# broken build as a healthy one.
/^(FAIL|---)/ || $1 == "FAIL" {
	pkg = ($2 != "" ? $2 : "(unknown)")
	seen[pkg] = 1
	broke[pkg] = 1
	next
}

# keys_sorted collects the union of what was seen and what was declared, in a
# stable order. An insertion sort over eight packages is portable in a way that
# asort() and a `| sort` are not — and the pipe was worse than unportable: it
# replaced the exit status of awk with the exit status of sort, so every
# failure below reported success. That bug is what the "a build failure is not
# a coverage result" check caught.
function keys_sorted(out,   pkg, n, i, j, k) {
	n = 0
	for (pkg in seen) out[++n] = pkg
	for (pkg in declared) if (!seen[pkg]) out[++n] = pkg
	for (i = 2; i <= n; i++) {
		k = out[i]
		for (j = i - 1; j >= 1 && out[j] > k; j--) out[j+1] = out[j]
		out[j+1] = k
	}
	return n
}

END {
	if (bad) exit 2

	n = keys_sorted(order)
	for (idx = 1; idx <= n; idx++) {
		pkg = order[idx]
		if (!seen[pkg]) {
			printf "  %-58s a floor for a package that no longer exists\n", pkg
			fail = 1
			continue
		}
		if (broke[pkg]) {
			printf "  %-58s the test run failed — no coverage was measured\n", pkg
			fail = 1
			continue
		}
		if (!declared[pkg]) {
			printf "  %-58s no floor declared for this package\n", pkg
			fail = 1
			continue
		}
		if (notested[pkg]) {
			printf "  %-58s no test files\n", pkg
			fail = 1
			continue
		}
		if (got[pkg] + 0 < floor[pkg] + 0) {
			printf "  %-58s %5.1f%%  below its floor of %.1f%%\n", pkg, got[pkg], floor[pkg]
			fail = 1
		} else {
			printf "  %-58s %5.1f%%  (floor %.1f%%)\n", pkg, got[pkg], floor[pkg]
			# A floor this far under the real number stopped catching
			# anything a long time ago. Said, not enforced: raising one is a
			# decision with a commit behind it.
			if (got[pkg] - floor[pkg] >= 10) behind[pkg] = 1
		}
	}

	if (fail) {
		printf "\ncoverage.sh: the floors are not met\n" > "/dev/stderr"
		printf "a floor goes up in a commit with the tests that earned it, never down to go green\n" > "/dev/stderr"
		exit 1
	}

	notes = 0
	for (idx = 1; idx <= n; idx++) {
		pkg = order[idx]
		if (!behind[pkg]) continue
		if (!notes) { printf "\nnote:\n"; notes = 1 }
		printf "  %s is %.1f points above its floor — consider raising it\n", pkg, got[pkg] - floor[pkg]
	}
	printf "\nevery package meets its coverage floor\n"
}
' "$tmp/cover"
