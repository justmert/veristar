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
#   the licence gate (licence-gate.sh) runs first, so a release whose
#   dependencies fail it is never built, and its report (licences.json and
#   console-bundle.txt) is attached to the release beside release-images.json
#
#   scripts/supply-chain/release-workstation.sh <tag> <work dir>
#
# the website in the console image takes two optional values from the
# environment, passed on as the console's build arguments of the same name:
#   SITE_ORIGIN  its canonical origin (canonical link, open graph, sitemap);
#                unset or empty, the site's own default applies
#   REPO_URL     its source repository links; unset or empty, they stay hidden
# release-images.yml passes the repository variable SITE_ORIGIN and the
# repository's own url, so a workstation release of the public repository
# sets both, for example
#   SITE_ORIGIN=https://veristar.app REPO_URL=https://github.com/justmert/veristar \
#     scripts/supply-chain/release-workstation.sh <tag> <work dir>
#
# needs: a clean checkout at the tag, docker with buildx, go, node with
# pnpm, cosign 3.1.4, syft 1.57.1 and trivy 0.76.2 on PATH, sops 3.13.3 at $SOPS, the operator's
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

# the licence report of the tag; trivy reads licences from the module cache
# and node_modules, which a fresh checkout does not have yet
pnpm install --frozen-lockfile >/dev/null
scripts/supply-chain/licence-gate.sh "$work/licences"
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
echo "console website: SITE_ORIGIN=${SITE_ORIGIN:-(site default)} REPO_URL=${REPO_URL:-(no repository links)}"
# an unset or empty value is left out, so the dockerfile's empty default applies
release console deploy/images/console.Dockerfile . "CADDY_IMAGE=$caddy_ref" \
	${SITE_ORIGIN:+"SITE_ORIGIN=$SITE_ORIGIN"} ${REPO_URL:+"REPO_URL=$REPO_URL"}
release postgres-ha deploy/images/postgres-ha.Dockerfile deploy/images

python3 scripts/supply-chain/release-manifest.py "$work/digests" "$tag" >"$work/release-images.json"
python3 -c 'import json,sys; [print(i["repo"]+"@"+i["digest"]) for i in json.load(open(sys.argv[1]))["images"].values()]' "$work/release-images.json" |
	while read -r ref; do
		cosign verify --key infra/ansible/vars/release-cosign.pub --insecure-ignore-tlog=true "$ref" >/dev/null 2>&1 ||
			{ echo "verify failed: $ref" >&2; exit 1; }
	done
gh release view "$tag" >/dev/null 2>&1 ||
	gh release create "$tag" --verify-tag --title "$tag" --notes "images: release-images.json (built and signed on a workstation, docs/security/supply-chain.md section 2.1)"
gh release upload "$tag" "$work/release-images.json" "$work/licences/licences.json" "$work/licences/console-bundle.txt" --clobber
echo "release $tag: $work/release-images.json"
