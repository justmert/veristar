#!/bin/sh
# sbom and vulnerability gate for one container image. the
# release workflow, the hourly security workflow and local runs all call this
# script, so the policy lives in one place:
#   - syft writes an spdx json sbom of the image
#   - trivy writes the full report (every severity, fixed or not) as json
#   - trivy then fails when a critical or high finding has a fix available.
#     unfixed findings are recorded but do not fail, because nothing can be
#     done about them except the mitigations in docs/runbooks/security-patch.md
#
#   scripts/supply-chain/scan-image.sh <image reference> <output dir>
#
# needs syft 1.57.1 and trivy 0.76.2 on PATH. the version
# check stops a run with another version instead of applying a policy that
# was never reviewed against it.
set -eu

if [ $# -ne 2 ]; then
	echo "usage: $0 <image reference> <output dir>" >&2
	exit 2
fi
image="$1"
out="$2"

syft version | grep -q '^Version: *1\.57\.1$' || { echo "syft is not 1.57.1" >&2; exit 1; }
trivy --version | grep -q '^Version: 0\.76\.2$' || { echo "trivy is not 0.76.2" >&2; exit 1; }

mkdir -p "$out"
# a file name from the reference without registry slashes or the digest colon
name="$(printf '%s' "$image" | tr '/:@' '___')"

syft scan --quiet -o spdx-json="$out/$name.spdx.json" "$image"

# full record first, so a failing gate still leaves the evidence behind
trivy image --quiet --scanners vuln --format json --output "$out/$name.trivy.json" "$image"

# the gate: fixable critical and high findings only
# the table goes to a file and is printed after, never through a pipe: a
# pipeline's status is the last command's, which would hide trivy's exit 1
rc=0
trivy image --quiet --scanners vuln --severity CRITICAL,HIGH --ignore-unfixed \
	--format table --exit-code 1 --output "$out/$name.gate.txt" "$image" || rc=$?
cat "$out/$name.gate.txt"
if [ "$rc" -ne 0 ]; then
	echo "gate failed for $image: critical or high findings with a fix (exit $rc)" >&2
fi
exit "$rc"
