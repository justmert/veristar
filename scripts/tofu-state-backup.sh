#!/usr/bin/env bash
# keeps a copy of an environment's opentofu state in the backup bucket
#. the state bucket is
# unversioned (use_lockfile needs conditional puts, which hetzner does not
# support on versioned buckets), so an overwritten or deleted state cannot
# be undone there. after each apply, make <env>-apply runs this script,
# which copies the state object byte for byte into the backup bucket, which
# has versioning and a compliance-mode object lock (roles/storage). the copy
# stays encrypted with the state passphrase: nothing is decrypted here.
#
#   scripts/tofu-state-backup.sh <staging|production>           # copy now
#   scripts/tofu-state-backup.sh <staging|production> list      # list copies
#   scripts/tofu-state-backup.sh <staging|production> restore <key>
#
# restore puts a listed copy back as the live state object; run it only with
# no tofu command running (docs/deploy/opentofu.md section 6).
#
# AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY come from sops exec-env, as
# for make state-bootstrap. TFVARS, S3_HOST, CA_FILE and CONNECT_HOST exist
# for the local test against the object storage stand-in
# (tests/localconverge/ops-local.sh) and are unset otherwise.
set -euo pipefail

env_name="${1:?usage: tofu-state-backup.sh <staging|production> [list|restore <key>]}"
action="${2:-copy}"
case "$env_name" in
  staging|production) ;;
  *) echo "unknown environment: $env_name" >&2; exit 2 ;;
esac

tfvars="${TFVARS:-infra/tofu/envs/$env_name/terraform.tfvars}"
[[ -f "$tfvars" ]] || { echo "$tfvars is missing an operator input" >&2; exit 1; }

# the same plain `key = "value"` reader as scripts/tofu-state-bootstrap.sh
read_var() {
  local value
  value="$(sed -n -E "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*\"([a-z0-9-]+)\"[[:space:]]*$/\1/p" "$tfvars")"
  if [[ -z "$value" || "$(printf '%s\n' "$value" | wc -l)" -ne 1 ]]; then
    echo "$tfvars must contain exactly one line $1 = \"...\"" >&2
    exit 1
  fi
  printf '%s' "$value"
}

state_bucket="$(read_var state_bucket)"
state_location="$(read_var state_location)"
backup_bucket="$(read_var backup_bucket)"
backup_location="$(read_var backup_location)"

: "${AWS_ACCESS_KEY_ID:?AWS_ACCESS_KEY_ID is not set (hetzner object storage key, an operator input}"
: "${AWS_SECRET_ACCESS_KEY:?AWS_SECRET_ACCESS_KEY is not set an operator input}"

# the state key of envs/<env>/versions.tf
state_key="$env_name/tofu.tfstate"
copy_prefix="tofu-state/$env_name/"

host_of() { printf '%s' "${S3_HOST:-$1.your-objectstorage.com}"; }
state_url="https://${state_bucket}.$(host_of "$state_location")"
backup_url="https://${backup_bucket}.$(host_of "$backup_location")"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# s3 <region> <url> [curl args]: prints the status code; the body goes to
# $tmp/body. the key pair goes to curl on stdin, never on the command line.
s3() {
  local region="$1" url="$2" host
  shift 2
  host="${url#https://}"
  host="${host%%/*}"
  local extra=()
  [[ -z "${CA_FILE:-}" ]] || extra+=(--cacert "$CA_FILE")
  [[ -z "${CONNECT_HOST:-}" ]] || extra+=(--connect-to "$host:443:$CONNECT_HOST:443")
  printf 'user = "%s:%s"\n' "$AWS_ACCESS_KEY_ID" "$AWS_SECRET_ACCESS_KEY" |
    curl --config - --silent --show-error "${extra[@]}" \
      --aws-sigv4 "aws:amz:${region}:s3" --output "$tmp/body" --write-out '%{http_code}' "$@" "$url"
}

must() {
  local what="$1" status
  shift
  status="$(s3 "$@")"
  [[ "$status" == 200 ]] || { echo "$what answered $status: $(head -c 300 "$tmp/body")" >&2; exit 1; }
}

case "$action" in
  copy)
    must "reading the state" "$state_location" "$state_url/$state_key"
    mv "$tmp/body" "$tmp/state"
    [[ -s "$tmp/state" ]] || { echo "the state object is empty; not copying it" >&2; exit 1; }
    stamp="$(date -u +%Y%m%dT%H%M%SZ)"
    sha="$(sha256sum "$tmp/state" | cut -d' ' -f1)"
    md5="$(openssl dgst -md5 -binary "$tmp/state" | openssl base64)"
    must "writing the copy" "$backup_location" "$backup_url/${copy_prefix}${stamp}.tfstate" \
      --request PUT --header "Content-MD5: $md5" --header "x-amz-meta-sha256: $sha" --data-binary "@$tmp/state"
    echo "copied $state_key to $backup_bucket/${copy_prefix}${stamp}.tfstate (sha256 $sha)"
    ;;
  list)
    must "listing the copies" "$backup_location" "$backup_url/?list-type=2&prefix=$copy_prefix"
    sed -E 's/<Key>/\n<Key>/g' "$tmp/body" | sed -n -E 's/^<Key>([^<]*)<\/Key>.*<Size>([0-9]*)<\/Size>.*/\1 \2/p'
    ;;
  restore)
    key="${3:?restore needs the key of a copy, as list prints it}"
    [[ "$key" == "$copy_prefix"*.tfstate ]] || { echo "$key is not a copy of $env_name's state" >&2; exit 2; }
    must "reading the copy" "$backup_location" "$backup_url/$key"
    mv "$tmp/body" "$tmp/state"
    md5="$(openssl dgst -md5 -binary "$tmp/state" | openssl base64)"
    must "writing the state" "$state_location" "$state_url/$state_key" \
      --request PUT --header "Content-MD5: $md5" --data-binary "@$tmp/state"
    echo "restored $key as $state_bucket/$state_key"
    ;;
  *) echo "unknown action $action" >&2; exit 2 ;;
esac
