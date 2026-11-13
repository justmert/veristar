#!/bin/sh
# daily slo data collection with catch-up (the design, .github/workflows/slo-report.yml).
#
#   scripts/slo-collect.sh missing <today YYYY-MM-DD>
#       prints the days of the catch-up window that have no data file on
#       origin, oldest first
#   scripts/slo-collect.sh run [day]
#       collects those days (or only the given day) from STATUS_BASE_URL onto
#       the branch slo/<YYYY-MM> of each day, commits and pushes them, and
#       writes the monthly report once a month's last day is there
#
# a day lives on origin/slo/<YYYY-MM> until that branch is merged, then on
# origin/main, so both are checked. one failed run (status page unreachable,
# actions outage) therefore costs nothing as long as a later run comes
# within the window.
#
# the window is SLO_CATCHUP_DAYS days back from yesterday (default 6). gatus
# keeps 20160 results per endpoint (roles/observability
# observability_gatus_max_results), which at the 30 s probe interval is
# exactly 7 days, so at the 00:30 run the day 7 days back has already lost
# its first half hour; collecting it would store a short day as if complete.
set -eu

# run switches branches, which can rewrite this file and cmd/sloreport under
# a running shell, so run works from a copy of the script and a binary built
# once from the commit the workflow checked out
if [ "${1:-}" = run ] && [ -z "${SLO_COLLECT_COPY:-}" ]; then
	tmp="$(mktemp -d)"
	cp "$0" "$tmp/slo-collect.sh"
	go build -o "$tmp/sloreport" ./cmd/sloreport
	SLO_COLLECT_COPY="$tmp" exec sh "$tmp/slo-collect.sh" "$@"
fi
sloreport="${SLO_COLLECT_COPY:-}/sloreport"

days="${SLO_CATCHUP_DAYS:-6}"
data=docs/slo/data

has_day() {
	month="${1%-*}"
	git cat-file -e "origin/slo/$month:$data/$1.json" 2>/dev/null ||
		git cat-file -e "origin/main:$data/$1.json" 2>/dev/null
}

has_report() {
	git cat-file -e "origin/slo/$1:docs/slo/$1.md" 2>/dev/null ||
		git cat-file -e "origin/main:docs/slo/$1.md" 2>/dev/null
}

missing() {
	n="$days"
	while [ "$n" -ge 1 ]; do
		d="$(date -u -d "$1 -$n day" +%F)"
		has_day "$d" || echo "$d"
		n=$((n - 1))
	done
}

last_of_month() {
	date -u -d "$1-01 +1 month -1 day" +%F
}

# the ci bot identity is set per command only, never in the repository config
commit() {
	git -c user.name='github-actions[bot]' -c user.email='41898282+github-actions[bot]@users.noreply.github.com' \
		commit -m "$1"
}

checkout_month() {
	b="slo/$1"
	if git ls-remote --exit-code --heads origin "$b" >/dev/null; then
		git fetch -q origin "$b" && git checkout -q -B "$b" "origin/$b"
	else
		git checkout -q -B "$b" origin/main
	fi
}

# set -e does not apply inside a function called with ||, so every step
# returns on failure itself
collect_day() {
	month="${1%-*}"
	checkout_month "$month" || return 1
	"$sloreport" collect -gatus "$STATUS_BASE_URL" -day "$1" -out "$data" || return 1
	git add "$data/$1.json" || return 1
	if ! git diff --cached --quiet; then
		commit "add slo probe results for $1" || return 1
		git push -q origin "slo/$month" || return 1
	fi
}

report_month() {
	checkout_month "$1" || return 1
	"$sloreport" report -month "$1" -data "$data" -out docs/slo || return 1
	git add "docs/slo/$1.md" || return 1
	if ! git diff --cached --quiet; then
		commit "add slo report for $1" || return 1
		git push -q origin "slo/$1" || return 1
	fi
	open="$(gh pr list --head "slo/$1" --base main --state open --json number --jq '.[].number')" || return 1
	if [ -z "$open" ]; then
		gh pr create --base main --head "slo/$1" --title "slo report $1" \
			--body "Monthly SLO report for $1 from the daily probe results in docs/slo/data/ (cmd/sloreport)."
	fi
}

run() {
	: "${STATUS_BASE_URL:?repository variable STATUS_BASE_URL is not set (https://status.<BASE_DOMAIN>, an operator input}"
	git fetch -q origin
	today="$(date -u +%F)"
	if [ -n "${1:-}" ]; then
		todo="$1"
	else
		todo="$(missing "$today")"
	fi
	failed=0
	for d in $todo; do
		echo "collecting $d"
		# one bad day must not stop the others; the run still fails at the end
		collect_day "$d" || failed=1
	done
	git fetch -q origin
	# a month's report is written once its last day is stored, also when an
	# earlier run collected that day but failed before the report
	n="$days"
	while [ "$n" -ge 1 ]; do
		d="$(date -u -d "$today -$n day" +%F)"
		m="${d%-*}"
		if [ "$d" = "$(last_of_month "$m")" ] && has_day "$d" && ! has_report "$m"; then
			report_month "$m" || failed=1
		fi
		n=$((n - 1))
	done
	return "$failed"
}

case "${1:-}" in
missing)
	[ $# -eq 2 ] || { echo "usage: $0 missing <today YYYY-MM-DD>" >&2; exit 2; }
	missing "$2"
	;;
run)
	shift
	run "$@"
	;;
*)
	echo "usage: $0 missing <today> | run [day]" >&2
	exit 2
	;;
esac
