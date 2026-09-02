#!/bin/sh
# repo.sh — keep the repository's About box and Pages source in version control.
#
# `.github/repo.env` holds the description, the website and the topics. This
# script writes them to GitHub, or checks that what is on GitHub still matches.
# The point is that a change to the way the project describes itself arrives in
# a diff like everything else, instead of in somebody's browser.
#
#   scripts/repo.sh check    fail if GitHub has drifted from .github/repo.env
#   scripts/repo.sh apply    write .github/repo.env to GitHub (needs admin)
#   scripts/repo.sh show     print what GitHub has next to what the file wants
#
# `apply` also makes sure Pages is building from the Actions workflow, which is
# the one repository setting the Pages deploy cannot do for itself.
#
# POSIX sh and gh only. GD_REPO overrides the repository slug; REPO_ENV
# overrides the file, so the tooling can be pointed at a fixture.

set -eu

env_file="${REPO_ENV:-.github/repo.env}"

[ -f "$env_file" ] || { echo "$env_file: not found (run from the repository root)" >&2; exit 2; }
command -v gh >/dev/null 2>&1 || { echo "gh is required: https://cli.github.com" >&2; exit 2; }

# shellcheck source=/dev/null
. "$env_file"

: "${DESCRIPTION:?$env_file must set DESCRIPTION}"
: "${HOMEPAGE:?$env_file must set HOMEPAGE}"
: "${TOPICS:?$env_file must set TOPICS}"

# In Actions the slug is in the environment; locally it comes from the remote.
repo="${GD_REPO:-${GITHUB_REPOSITORY:-}}"
if [ -z "$repo" ]; then
	repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
fi

# Topics are the one field GitHub validates for us, and it does it by rejecting
# the whole call — so say which topic is wrong before spending the request.
lint_topics() {
	count=0
	for t in $TOPICS; do
		count=$((count + 1))
		case "$t" in
		*[!a-z0-9-]* | -* | *-) echo "topic \"$t\": lowercase letters, digits and inner hyphens only" >&2; exit 2 ;;
		esac
		[ "${#t}" -le 50 ] || { echo "topic \"$t\": longer than 50 characters" >&2; exit 2; }
	done
	[ "$count" -le 20 ] || { echo "$count topics: GitHub keeps at most 20" >&2; exit 2; }
	[ "${#DESCRIPTION}" -le 350 ] || { echo "DESCRIPTION is ${#DESCRIPTION} characters: GitHub keeps 350" >&2; exit 2; }
}

remote_description() { gh api "repos/$repo" --jq '.description // ""'; }
remote_homepage() { gh api "repos/$repo" --jq '.homepage // ""'; }
# Sorted, so the order in the file is a matter of taste rather than a diff.
remote_topics() { gh api "repos/$repo/topics" --jq '.names | sort | join(" ")'; }
wanted_topics() { printf '%s\n' $TOPICS | sort | tr '\n' ' ' | sed 's/ $//'; }

show() {
	printf 'repository   %s\n' "$repo"
	printf 'description  %s\n             %s\n' "want: $DESCRIPTION" "have: $(remote_description)"
	printf 'homepage     %s\n             %s\n' "want: $HOMEPAGE" "have: $(remote_homepage)"
	printf 'topics       %s\n             %s\n' "want: $(wanted_topics)" "have: $(remote_topics)"
}

check() {
	lint_topics
	drift=0
	if [ "$(remote_description)" != "$DESCRIPTION" ]; then
		echo "description has drifted:" >&2
		echo "  want: $DESCRIPTION" >&2
		echo "  have: $(remote_description)" >&2
		drift=1
	fi
	if [ "$(remote_homepage)" != "$HOMEPAGE" ]; then
		echo "homepage has drifted: want \"$HOMEPAGE\", have \"$(remote_homepage)\"" >&2
		drift=1
	fi
	if [ "$(remote_topics)" != "$(wanted_topics)" ]; then
		echo "topics have drifted:" >&2
		echo "  want: $(wanted_topics)" >&2
		echo "  have: $(remote_topics)" >&2
		drift=1
	fi
	if [ "$drift" -ne 0 ]; then
		echo "" >&2
		echo "run scripts/repo.sh apply to write $env_file to GitHub" >&2
		exit 1
	fi
	echo "$repo About box matches $env_file"
}

apply() {
	lint_topics
	gh api -X PATCH "repos/$repo" -f "description=$DESCRIPTION" -f "homepage=$HOMEPAGE" >/dev/null
	set -- ""
	shift
	for t in $TOPICS; do set -- "$@" -f "names[]=$t"; done
	gh api -X PUT "repos/$repo/topics" "$@" >/dev/null
	echo "wrote description, homepage and $(printf '%s\n' $TOPICS | wc -l | tr -d ' ') topics to $repo"

	# The Pages deploy in .github/workflows/pages.yml cannot enable itself: a
	# repository whose Pages source is still "branch" ignores the artifact.
	if gh api "repos/$repo/pages" >/dev/null 2>&1; then
		if [ "$(gh api "repos/$repo/pages" --jq .build_type)" != "workflow" ]; then
			gh api -X PUT "repos/$repo/pages" -f build_type=workflow >/dev/null
			echo "switched Pages to the Actions workflow"
		else
			echo "Pages already builds from the Actions workflow"
		fi
	else
		gh api -X POST "repos/$repo/pages" -f build_type=workflow >/dev/null
		echo "enabled Pages, building from the Actions workflow"
	fi
}

case "${1:-check}" in
check) check ;;
apply) apply ;;
show) show ;;
*)
	echo "usage: scripts/repo.sh [check|apply|show]" >&2
	exit 2
	;;
esac
