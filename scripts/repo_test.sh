#!/bin/sh
# repo_test.sh — tests for scripts/repo.sh, against a fake `gh`.
#
# This script writes to a public repository's front page and reads it back in a
# CI gate, so both directions have to be tested without touching GitHub: the
# fake `gh` below records every call and answers from fixture variables.
#
# What is actually at stake:
#   - `check` runs in CI. A drift it does not notice is a description nobody
#     reviews; a drift it invents fails the build on every commit.
#   - `apply` is run by hand with admin rights. It has one chance to send the
#     right calls, and GitHub rejects the whole topics call over one bad topic —
#     so the linting has to happen before the request, and name the topic.
#   - `apply` also enables Pages, which is the one setting the Pages workflow
#     cannot set for itself. Enabling it when it is already on must not undo it.
#
# POSIX sh only, like the rest of scripts/.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
script="$root/scripts/repo.sh"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/galera-doctor-repo-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

failures=0
checks=0

fail() {
	failures=$((failures + 1))
	echo "FAIL: $1" >&2
}

# ---------------------------------------------------------------------------
# A fake gh. It answers the three reads repo.sh performs, logs every call, and
# takes what the "repository" currently holds from FAKE_* in the environment.
# ---------------------------------------------------------------------------
mkdir -p "$tmp/bin"
cat >"$tmp/bin/gh" <<'GH'
#!/bin/sh
printf '%s\n' "$*" >>"$GH_LOG"
case "$*" in
*"-X PATCH"* | *"-X PUT repos/"*"/topics"*) echo '{}' ;;
*"-X PUT repos/"*"/pages"* | *"-X POST repos/"*"/pages"*) echo '{}' ;;
*"/topics --jq"*) printf '%s\n' "$(printf '%s\n' $FAKE_TOPICS | sort | tr '\n' ' ' | sed 's/ $//')" ;;
*"/pages --jq .build_type"*)
	[ "${FAKE_PAGES:-absent}" = absent ] && exit 1
	printf '%s\n' "$FAKE_PAGES"
	;;
*"/pages"*) [ "${FAKE_PAGES:-absent}" = absent ] && exit 1 || echo '{}' ;;
*".description"*) printf '%s\n' "${FAKE_DESCRIPTION:-}" ;;
*".homepage"*) printf '%s\n' "${FAKE_HOMEPAGE:-}" ;;
*) echo "fake gh: unhandled call: $*" >&2; exit 3 ;;
esac
GH
chmod +x "$tmp/bin/gh"

# write_env <description> <homepage> <topics>
write_env() {
	cat >"$tmp/repo.env" <<EOF
DESCRIPTION="$1"
HOMEPAGE="$2"
TOPICS="$3"
EOF
}

run() {
	: >"$tmp/gh.log"
	PATH="$tmp/bin:$PATH" GH_LOG="$tmp/gh.log" GD_REPO=owner/repo \
		REPO_ENV="$tmp/repo.env" sh "$script" "$@"
}

# assert_pass <name> <command>...
assert_pass() {
	name=$1
	shift
	checks=$((checks + 1))
	if run "$@" >"$tmp/out.txt" 2>&1; then
		echo "ok   $name"
	else
		fail "$name — exited non-zero"
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
}

# assert_fail <name> <needle> <command>...
assert_fail() {
	name=$1
	needle=$2
	shift 2
	checks=$((checks + 1))
	if run "$@" >"$tmp/out.txt" 2>&1; then
		fail "$name — exited 0"
		sed 's/^/       /' "$tmp/out.txt" >&2
		return
	fi
	if grep -qF -e "$needle" "$tmp/out.txt"; then
		echo "ok   $name"
	else
		fail "$name — failed for the wrong reason, wanted: $needle"
		sed 's/^/       /' "$tmp/out.txt" >&2
	fi
}

# assert_logged <name> <needle>
assert_logged() {
	checks=$((checks + 1))
	if grep -qF -e "$2" "$tmp/gh.log"; then
		echo "ok   $1"
	else
		fail "$1 — no such call, wanted: $2"
		sed 's/^/       /' "$tmp/gh.log" >&2
	fi
}

# assert_not_logged <name> <needle>
assert_not_logged() {
	checks=$((checks + 1))
	if grep -qF -e "$2" "$tmp/gh.log"; then
		fail "$1 — unexpected call: $2"
		sed 's/^/       /' "$tmp/gh.log" >&2
	else
		echo "ok   $1"
	fi
}

want_desc="An audit, not a health check"
want_home="https://example.github.io/repo/"
want_topics="galera mariadb read-only"

# ---------------------------------------------------------------------------
# In sync, in a different order: the order in the file is taste, not drift.
# ---------------------------------------------------------------------------
write_env "$want_desc" "$want_home" "$want_topics"
export FAKE_DESCRIPTION="$want_desc" FAKE_HOMEPAGE="$want_home" FAKE_TOPICS="read-only galera mariadb" FAKE_PAGES=workflow
assert_pass "a repository in sync passes, whatever the topic order" check

# ---------------------------------------------------------------------------
# One field drifted at a time, so a failure names the field.
# ---------------------------------------------------------------------------
FAKE_DESCRIPTION="A generic MySQL health check"
assert_fail "a drifted description is caught" "description has drifted" check
assert_fail "and the failure names the command that fixes it" "scripts/repo.sh apply" check
FAKE_DESCRIPTION="$want_desc"

FAKE_HOMEPAGE=""
assert_fail "an empty homepage is caught" "homepage has drifted" check
FAKE_HOMEPAGE="$want_home"

FAKE_TOPICS="galera mariadb"
assert_fail "a missing topic is caught" "topics have drifted" check
FAKE_TOPICS="galera mariadb read-only proxysql"
assert_fail "an extra topic is caught" "topics have drifted" check
FAKE_TOPICS="read-only galera mariadb"

# ---------------------------------------------------------------------------
# The file itself is linted before a request is spent — and GitHub's rules are
# the ones being enforced, so the message has to name the offending value.
# ---------------------------------------------------------------------------
write_env "$want_desc" "$want_home" "Galera mariadb"
assert_fail "an uppercase topic is rejected" "Galera" check
assert_not_logged "and no API call was made" "-X PUT"

write_env "$want_desc" "$want_home" "galera -mariadb"
assert_fail "a topic with a leading hyphen is rejected" "-mariadb" check

write_env "$want_desc" "$want_home" "galera my_sql"
assert_fail "a topic with an underscore is rejected" "my_sql" check

write_env "$want_desc" "$want_home" "t1 t2 t3 t4 t5 t6 t7 t8 t9 t10 t11 t12 t13 t14 t15 t16 t17 t18 t19 t20 t21"
assert_fail "more than twenty topics is rejected" "at most 20" check

long=$(awk 'BEGIN { while (i++ < 360) printf "x" }')
write_env "$long" "$want_home" "$want_topics"
assert_fail "a description over 350 characters is rejected" "350" check

# ---------------------------------------------------------------------------
# apply sends the writes, all of them, and does not touch Pages when Pages is
# already building from the workflow.
# ---------------------------------------------------------------------------
write_env "$want_desc" "$want_home" "$want_topics"
FAKE_PAGES=workflow
assert_pass "apply succeeds" apply
assert_logged "apply patches the description" "-f description=$want_desc"
assert_logged "apply patches the homepage" "-f homepage=$want_home"
assert_logged "apply sends every topic in one call" "names[]=galera -f names[]=mariadb -f names[]=read-only"
assert_not_logged "apply leaves a working Pages setting alone" "-X PUT repos/owner/repo/pages"

# Pages off entirely: apply has to turn it on, because the deploy cannot.
FAKE_PAGES=absent
assert_pass "apply succeeds with Pages disabled" apply
assert_logged "apply enables Pages from the workflow" "-X POST repos/owner/repo/pages -f build_type=workflow"

# Pages on, but building from a branch: the artifact would be ignored.
FAKE_PAGES=legacy
assert_pass "apply succeeds with Pages on a branch" apply
assert_logged "apply switches Pages to the workflow" "-X PUT repos/owner/repo/pages -f build_type=workflow"

# ---------------------------------------------------------------------------
# Usage errors: a missing file and a typo in CI must not look like success.
# ---------------------------------------------------------------------------
checks=$((checks + 1))
if PATH="$tmp/bin:$PATH" GH_LOG="$tmp/gh.log" GD_REPO=owner/repo \
	REPO_ENV="$tmp/nope.env" sh "$script" check >"$tmp/out.txt" 2>&1; then
	fail "a missing repo.env exited 0"
else
	status=$?
	if [ "$status" -eq 2 ] && grep -qF "not found" "$tmp/out.txt"; then
		echo "ok   a missing repo.env is a usage error"
	else
		fail "a missing repo.env exited $status: $(cat "$tmp/out.txt")"
	fi
fi

write_env "$want_desc" "$want_home" "$want_topics"
assert_fail "an unknown subcommand is a usage error" "usage:" publish

# A file that sets none of the three is a usage error, not an apply that wipes
# the About box.
printf 'DESCRIPTION="only this"\n' >"$tmp/repo.env"
assert_fail "a repo.env missing HOMEPAGE and TOPICS is rejected" "HOMEPAGE" apply
assert_not_logged "and nothing was written" "-X PATCH"

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures of $checks checks failed" >&2
	exit 1
fi
echo "all $checks checks passed"
