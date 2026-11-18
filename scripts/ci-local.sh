#!/bin/sh
# runs the jobs of .github/workflows/ci.yml on this machine, so the same
# gates can be run before a push or without github actions. it is not a
# substitute for a recorded run on github.
#
#   scripts/ci-local.sh [job...]      default: every job, in ci.yml order
#
# each job_<name> below is the ci.yml job of that name, step for step, with
# the same make targets (tests/ci checks that every job and every make step
# of ci.yml is here). steps that only exist on github are printed as skipped
# with the reason; nothing is reported as passed that did not run.
#
# the tools are installed at their pins with their checksums into
# CI_TOOLS (default ~/.local/share/veristar/ci-tools), as the
# workflow installs them into the runner's temporary directory.
#
# the host is shared: a job that starts container stacks refuses to start
# while the one-minute load average is at or above CI_LOCAL_MAX_LOAD (default
# 20), and the run stops there with that job named as not run.
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
tools="${CI_TOOLS:-$HOME/.local/share/veristar/ci-tools}"
max_load="${CI_LOCAL_MAX_LOAD:-20}"
mkdir -p "$tools"
PATH="$tools:$PATH"
export PATH
# a fresh venv of its own, as on a runner: uv refuses to recreate an existing
# one, and the shared venv of make ansible-venv may be in use by a converge
ANSIBLE_VENV="$tools/ansible-venv"
export ANSIBLE_VENV

# the golangci-lint version of ci.yml's env block, so the two cannot drift
golangci_version="$(sed -n 's/^  GOLANGCI_LINT_VERSION: //p' .github/workflows/ci.yml)"

all_jobs="go containers js integration image gitleaks licences isolation supplychain actionlint tofu ansible promtool"

say() {
	printf '\n== %s\n' "$*"
}

skip() {
	printf -- '-- skipped (github only): %s\n' "$*"
}

tool() {
	for t in "$@"; do
		[ -x "$tools/$t" ] || scripts/supply-chain/install-tools.sh "$tools" "$t"
	done
}

heavy() {
	load="$(cut -d' ' -f1 /proc/loadavg)"
	if awk -v l="$load" -v m="$max_load" 'BEGIN { exit !(l >= m) }'; then
		echo "job $1 not run: load average $load is at or above CI_LOCAL_MAX_LOAD=$max_load on this shared host" >&2
		exit 1
	fi
}

job_go() {
	skip "actions/setup-go (go is taken from PATH: $(go version))"
	if ! "$tools/golangci-lint" version 2>/dev/null | grep -q "version ${golangci_version#v} "; then
		v="${golangci_version#v}"
		base="https://github.com/golangci/golangci-lint/releases/download/${golangci_version}"
		work="$(mktemp -d)"
		(
			cd "$work"
			curl -fsSLO "${base}/golangci-lint-${v}-linux-amd64.tar.gz"
			curl -fsSLO "${base}/golangci-lint-${v}-checksums.txt"
			grep " golangci-lint-${v}-linux-amd64.tar.gz\$" "golangci-lint-${v}-checksums.txt" | sha256sum -c -
			tar -xzf "golangci-lint-${v}-linux-amd64.tar.gz"
		)
		install -m 0755 "$work/golangci-lint-${v}-linux-amd64/golangci-lint" "$tools/golangci-lint"
		rm -rf "$work"
	fi
	make vet
	make lint GOLANGCI_LINT="$tools/golangci-lint"
	make docs-check
	make geo-fetch
	DBIP_MMDB=.scratch/geo/dbip-country-lite.mmdb make test
}

job_containers() {
	heavy containers
	make geo-fetch
	DBIP_MMDB=.scratch/geo/dbip-country-lite.mmdb make test-containers
}

job_js() {
	skip "pnpm/action-setup and actions/setup-node (pnpm and node are taken from PATH: node $(node --version))"
	pnpm install --frozen-lockfile
	# the browsers come with the playwright image of the browser container
	skip "playwright install --with-deps (the matrix below runs in the browser container)"
	pnpm check
	pnpm test
	# the browser matrix: here in the browser container, out of reach of the
	# shared host's network changes (ci.yml runs it with the runner's browsers)
	heavy "browser matrix"
	make e2e-matrix
}

job_integration() {
	heavy integration
	make dev-up
	make umami-bootstrap
	make umami-bootstrap
	make test-integration
}

job_image() {
	heavy image
	make image CMD=platformctl
}

job_gitleaks() {
	tool gitleaks
	make gitleaks GITLEAKS="$tools/gitleaks"
}

job_licences() {
	pnpm install --frozen-lockfile
	tool trivy
	scripts/supply-chain/licence-gate.sh licence-report
	skip "actions/upload-artifact (the report stays in ./licence-report)"
}

job_isolation() {
	heavy isolation
	make isolation
}

job_supplychain() {
	heavy supplychain
	tool cosign uv
	rm -rf "$ANSIBLE_VENV"
	make ansible-venv
	make supplychain-it
}

job_actionlint() {
	tool actionlint shellcheck
	make actionlint
}

job_tofu() {
	skip "opentofu/setup-opentofu (tofu is taken from PATH: $(tofu version | head -n1))"
	make tofu-check
}

job_ansible() {
	tool uv
	rm -rf "$ANSIBLE_VENV"
	make ansible-venv
	make ansible-lint
	# the nats liveness settings of the rendered role files (tests/natsconf)
	make ansible-render-test
}

job_promtool() {
	tool promtool
	make promtool
}

jobs="${*:-$all_jobs}"
for j in $jobs; do
	case " $all_jobs " in
	*" $j "*) ;;
	*)
		echo "unknown job $j; ci.yml has: $all_jobs" >&2
		exit 2
		;;
	esac
done
for j in $jobs; do
	say "job $j"
	"job_$j"
	say "job $j passed"
done
say "passed: $jobs"
