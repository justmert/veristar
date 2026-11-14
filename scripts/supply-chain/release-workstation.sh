#!/bin/sh
# a release built and signed on a workstation, the same steps as
# .github/workflows/release-images.yml in key mode, for the time GitHub
# Actions is off (docs/security/supply-chain.md section 2.1):
#   1. build and push every platform image of the workflow's matrix to
#      ghcr.io/justmert/veristar/<name> with the same Dockerfile, context and
#      build arguments
#   2. gate each pushed digest with scan-image.sh (syft sbom, trivy fails on a
#      fixable critical or high finding)
#   3. sign it and attest its sbom with the release key from the production
#      sops file, with no transparency log upload
#   4. write release-images.json, verify every digest with the committed public
#      key, and attach the file to the github release of the tag
#
#   scripts/supply-chain/release-workstation.sh <tag> <work dir>
#
# needs: a clean checkout at the tag, docker with buildx, cosign 3.1.4,
# syft 1.57.1 and trivy 0.76.2 on PATH, sops 3.13.3 at $SOPS, the operator's
# age identity, GH_TOKEN of the release account with write:packages (also
# used for the docker login), and python3.
set -eu

tag="${1:?usage: release-workstation.sh <tag> <work dir>}"
work="${2:?usage: release-workstation.sh <tag> <work dir>}"
sops="${SOPS:-sops}"
repo_prefix="ghcr.io/justmert/veristar"
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"

: "${GH_TOKEN:?set GH_TOKEN to a token of the release account with write:packages}"
[ "$(git describe --tags --exact-match HEAD 2>/dev/null)" = "$tag" ] || { echo "HEAD is not at tag $tag" >&2; exit 1; }
[ -z "$(git status --porcelain --untracked-files=no)" ] || { echo "the checkout has local changes" >&2; exit 1; }
cosign version 2>/dev/null | grep -q 'GitVersion: *v3\.1\.4$' || { echo "cosign is not 3.1.4" >&2; exit 1; }

mkdir -p "$work/digests" "$work/scan"
printf '%s' "$GH_TOKEN" | docker login ghcr.io -u justmert --password-stdin >/dev/null

# the release key, from the production sops file, only in this process's
# environment (cosign reads env://), never written to disk
COSIGN_PRIVATE_KEY="$("$sops" decrypt --extract '["cosign_private_key"]' infra/secrets/production/secrets.enc.yaml)"
COSIGN_PASSWORD="$("$sops" decrypt --extract '["cosign_password"]' infra/secrets/production/secrets.enc.yaml)"
export COSIGN_PRIVATE_KEY COSIGN_PASSWORD

month="$(git log -1 --format=%cd --date=format:%Y-%m HEAD)"

# name dockerfile context [build-arg...]
release() {
	name="$1" file="$2" ctx="$3"
	shift 3
	ref="$repo_prefix/$name"
	t="$tag"
	if [ "$name" = postgres-ha ]; then
		t="$(sed -n 's/^ARG PG_VERSION=//p' deploy/images/postgres-ha.Dockerfile)-$tag"
	fi
	args=""
	for a in "VERSION=$tag" "$@"; do args="$args --build-arg $a"; done
	echo "== $name"
	# shellcheck disable=SC2086 # the build arguments are split on purpose
	docker buildx build --push --provenance=false --sbom=false -q \
		--metadata-file "$work/$name.meta.json" -f "$file" $args -t "$ref:$t" "$ctx" >/dev/null
	digest="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["containerimage.digest"])' "$work/$name.meta.json")"
	scripts/supply-chain/scan-image.sh "$ref@$digest" "$work/scan"
	cosign sign --yes --key env://COSIGN_PRIVATE_KEY --use-signing-config=false --tlog-upload=false "$ref@$digest" >/dev/null 2>&1
	sbom="$work/scan/$(printf '%s' "$ref@$digest" | tr '/:@' '___').spdx.json"
	cosign attest --yes --key env://COSIGN_PRIVATE_KEY --use-signing-config=false --tlog-upload=false \
		--type spdxjson --predicate "$sbom" "$ref@$digest" >/dev/null 2>&1
	printf '{"name":"%s","repo":"%s","tag":"%s","digest":"%s"}\n' "$name" "$ref" "$t" "$digest" >"$work/digests/$name.json"
	echo "$name $ref:$t@$digest"
}

# caddy first: the console image is built on top of this release's caddy
release caddy deploy/images/caddy.Dockerfile deploy/images
caddy_ref="$repo_prefix/caddy@$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["digest"])' "$work/digests/caddy.json")"
release gateway deploy/images/gateway.Dockerfile . "DBIP_MONTH=$month"
release collector deploy/images/collector.Dockerfile .
release controlplane deploy/images/controlplane.Dockerfile .
for c in replayer metricswriter jobs platformctl prober; do
	release "$c" deploy/images/go-service.Dockerfile . "CMD=$c"
done
release console deploy/images/console.Dockerfile . "CADDY_IMAGE=$caddy_ref"
release postgres-ha deploy/images/postgres-ha.Dockerfile deploy/images

python3 scripts/supply-chain/release-manifest.py "$work/digests" "$tag" >"$work/release-images.json"
python3 -c 'import json,sys; [print(i["repo"]+"@"+i["digest"]) for i in json.load(open(sys.argv[1]))["images"].values()]' "$work/release-images.json" |
	while read -r ref; do
		cosign verify --key infra/ansible/vars/release-cosign.pub --insecure-ignore-tlog=true "$ref" >/dev/null 2>&1 ||
			{ echo "verify failed: $ref" >&2; exit 1; }
	done
gh release view "$tag" >/dev/null 2>&1 ||
	gh release create "$tag" --verify-tag --title "$tag" --notes "images: release-images.json (built and signed on a workstation, docs/security/supply-chain.md section 2.1)"
gh release upload "$tag" "$work/release-images.json" --clobber
echo "release $tag: $work/release-images.json"
