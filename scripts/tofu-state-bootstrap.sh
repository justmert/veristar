#!/usr/bin/env bash
# creates the opentofu state bucket for one environment on hetzner object
# storage through the s3 api. run once, before the first `tofu init`, by
# `make state-bootstrap ENV=<env>` inside `sops exec-env`, which provides
# AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY.
#
# curl signs the requests itself (--aws-sigv4), so no s3 client has to be
# installed. the bucket stays unversioned: hetzner does not support conditional
# puts on versioned buckets, and the backend's use_lockfile depends on them.
set -euo pipefail

env_name="${1:?usage: tofu-state-bootstrap.sh <staging|production>}"
case "$env_name" in
  staging|production) ;;
  *) echo "unknown environment: $env_name" >&2; exit 2 ;;
esac

tfvars="infra/tofu/envs/$env_name/terraform.tfvars"
if [[ ! -f "$tfvars" ]]; then
  echo "$tfvars is missing: it needs state_bucket and state_location an operator input" >&2
  exit 1
fi

# reads a plain `key = "value"` line; anything fancier is refused rather than guessed
read_var() {
  local value
  value="$(sed -n -E "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*\"([a-z0-9-]+)\"[[:space:]]*$/\1/p" "$tfvars")"
  if [[ -z "$value" || "$(printf '%s\n' "$value" | wc -l)" -ne 1 ]]; then
    echo "$tfvars must contain exactly one line $1 = \"...\"" >&2
    exit 1
  fi
  printf '%s' "$value"
}

bucket="$(read_var state_bucket)"
location="$(read_var state_location)"
case "$location" in
  fsn1|nbg1|hel1) ;;
  *) echo "state_location must be fsn1, nbg1 or hel1, got $location" >&2; exit 1 ;;
esac

: "${AWS_ACCESS_KEY_ID:?AWS_ACCESS_KEY_ID is not set (hetzner object storage key, an operator input}"
: "${AWS_SECRET_ACCESS_KEY:?AWS_SECRET_ACCESS_KEY is not set an operator input}"

url="https://${bucket}.${location}.your-objectstorage.com/"

# the key pair goes to curl on stdin, never on the command line where other
# users could read it from the process list
s3() {
  printf 'user = "%s:%s"\n' "$AWS_ACCESS_KEY_ID" "$AWS_SECRET_ACCESS_KEY" |
    curl --config - --silent --show-error --aws-sigv4 "aws:amz:${location}:s3" "$@"
}

status="$(s3 --output /dev/null --write-out '%{http_code}' --head "$url")"
case "$status" in
  200)
    echo "state bucket $bucket already exists in $location"
    exit 0
    ;;
  404) ;;
  *)
    echo "HEAD $url answered $status; not creating anything" >&2
    exit 1
    ;;
esac

body="<CreateBucketConfiguration><LocationConstraint>${location}</LocationConstraint></CreateBucketConfiguration>"
status="$(s3 --output /dev/stderr --write-out '%{http_code}' --request PUT \
  --header 'x-amz-acl: private' --data-binary "$body" "$url")"
if [[ "$status" != "200" ]]; then
  echo "creating $bucket answered $status" >&2
  exit 1
fi
echo "created state bucket $bucket in $location (private, unversioned)"
