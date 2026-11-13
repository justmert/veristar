#!/usr/bin/env bash
# converges the pgbackrest backup bucket of one environment through the s3
# api (the design, role storage): the bucket exists, was created with
# object lock, has versioning enabled and carries a default retention of
# LOCK_MODE for LOCK_DAYS days. run by roles/storage on the controller.
#
# why object lock: the s3 keys on the core nodes can delete objects. hetzner
# object storage has no per-key permissions that would stop that, and object
# lock can only be turned on when a bucket is created
# (docs.hetzner.com/storage/object-storage/howto-protect-objects/
# protect-object-lock-retention, checked 2026-11-13). with versioning and a
# compliance-mode default retention, a delete only adds a delete marker and
# every version stays readable until its retention ends, whoever holds the
# keys. pgbackrest can read the repository as it was before such a delete
# (repo-target-time).
#
# inputs (environment): S3_ENDPOINT (https://host), S3_REGION, BUCKET,
# LOCK_MODE (COMPLIANCE or GOVERNANCE), LOCK_DAYS, AWS_ACCESS_KEY_ID,
# AWS_SECRET_ACCESS_KEY. optional, for the local converge's stand-in only:
# CA_FILE (the stand-in's private ca) and CONNECT_HOST (the address to
# connect to instead of resolving the virtual-host name; the request, its
# host header and the signature stay the same).
#
# prints one line per change ("changed: ...") so the caller can tell a
# no-op run from a converging one. exits non-zero on anything it cannot fix,
# for example a bucket that exists without object lock.
set -euo pipefail

: "${S3_ENDPOINT:?}" "${S3_REGION:?}" "${BUCKET:?}" "${LOCK_MODE:?}" "${LOCK_DAYS:?}"
: "${AWS_ACCESS_KEY_ID:?object storage access key missing an operator input}"
: "${AWS_SECRET_ACCESS_KEY:?object storage secret key missing an operator input}"

case "$LOCK_MODE" in COMPLIANCE|GOVERNANCE) ;; *) echo "LOCK_MODE must be COMPLIANCE or GOVERNANCE" >&2; exit 2 ;; esac
[[ "$LOCK_DAYS" =~ ^[1-9][0-9]*$ ]] || { echo "LOCK_DAYS must be a positive integer" >&2; exit 2; }

host="${S3_ENDPOINT#https://}"
host="${host%/}"
[[ "$S3_ENDPOINT" == https://* && "$host" != */* ]] || { echo "S3_ENDPOINT must be https://<host>" >&2; exit 2; }
# virtual-host style, as hetzner documents and as pgbackrest uses it
# (repo1-s3-uri-style=host)
url="https://${BUCKET}.${host}/"

extra=()
[[ -z "${CA_FILE:-}" ]] || extra+=(--cacert "$CA_FILE")
[[ -z "${CONNECT_HOST:-}" ]] || extra+=(--connect-to "${BUCKET}.${host}:443:${CONNECT_HOST}:443")

body_file="$(mktemp)"
trap 'rm -f "$body_file"' EXIT

# s3 <curl args...>: one signed request; the body lands in $body_file and
# the status code on stdout. the key pair goes to curl on stdin, never on the
# command line where other users could read it from the process list.
s3() {
  printf 'user = "%s:%s"\n' "$AWS_ACCESS_KEY_ID" "$AWS_SECRET_ACCESS_KEY" |
    curl --config - --silent --show-error "${extra[@]}" \
      --aws-sigv4 "aws:amz:${S3_REGION}:s3" \
      --output "$body_file" --write-out '%{http_code}' "$@"
}

# a request whose answer must be 200; anything else stops the run
s3_ok() {
  local what="$1" status
  shift
  status="$(s3 "$@")"
  if [[ "$status" != 200 ]]; then
    echo "$what answered $status: $(head -c 500 "$body_file")" >&2
    exit 1
  fi
}

# puts that carry an xml body need content-md5 (s3 requires it for the
# object lock configuration)
s3_put_xml() {
  local what="$1" query="$2" xml="$3" md5
  md5="$(printf '%s' "$xml" | openssl dgst -md5 -binary | openssl base64)"
  s3_ok "$what" --request PUT --header "Content-MD5: $md5" \
    --header 'Content-Type: application/xml' --data-binary "$xml" "${url}?${query}"
}

xml_value() {
  # first <tag>value</tag> of the last answer; the answers are small, flat xml
  sed -n -E "s/.*<$1>([^<]*)<\/$1>.*/\1/p" "$body_file" | head -n1
}

status="$(s3 --head "$url")"
case "$status" in
  200) ;;
  404)
    s3_ok "creating $BUCKET" --request PUT --header 'x-amz-acl: private' \
      --header 'x-amz-bucket-object-lock-enabled: true' \
      --data-binary "<CreateBucketConfiguration><LocationConstraint>${S3_REGION}</LocationConstraint></CreateBucketConfiguration>" \
      "$url"
    echo "changed: created bucket $BUCKET with object lock"
    ;;
  *) echo "HEAD $url answered $status; not changing anything" >&2; exit 1 ;;
esac

# object lock cannot be added later, so a bucket without it is a stop, not a fix
status="$(s3 "${url}?object-lock")"
if [[ "$status" != 200 || "$(xml_value ObjectLockEnabled)" != Enabled ]]; then
  echo "bucket $BUCKET exists without object lock (answer $status); it must be recreated with object lock, see docs/deploy/backups.md" >&2
  exit 1
fi
mode="$(xml_value Mode)"
days="$(xml_value Days)"

s3_ok "reading the versioning of $BUCKET" "${url}?versioning"
if [[ "$(xml_value Status)" != Enabled ]]; then
  s3_put_xml "enabling versioning" versioning \
    '<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Enabled</Status></VersioningConfiguration>'
  echo "changed: enabled versioning on $BUCKET"
fi

if [[ "$mode" != "$LOCK_MODE" || "$days" != "$LOCK_DAYS" ]]; then
  s3_put_xml "setting the default retention" object-lock \
    "<ObjectLockConfiguration xmlns=\"http://s3.amazonaws.com/doc/2006-03-01/\"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>${LOCK_MODE}</Mode><Days>${LOCK_DAYS}</Days></DefaultRetention></Rule></ObjectLockConfiguration>"
  echo "changed: default retention ${mode:-none} ${days:-0}d -> ${LOCK_MODE} ${LOCK_DAYS}d"
fi

# read everything back, so a provider that accepts a setting without
# applying it fails here and not on the day a backup is needed
s3_ok "reading the versioning of $BUCKET" "${url}?versioning"
[[ "$(xml_value Status)" == Enabled ]] || { echo "versioning of $BUCKET is not Enabled after setting it" >&2; exit 1; }
s3_ok "reading the object lock of $BUCKET" "${url}?object-lock"
if [[ "$(xml_value Mode)" != "$LOCK_MODE" || "$(xml_value Days)" != "$LOCK_DAYS" ]]; then
  # hetzner notes that a new default retention takes a few minutes to apply
  echo "default retention of $BUCKET reads $(xml_value Mode) $(xml_value Days)d, expected $LOCK_MODE ${LOCK_DAYS}d; rerun in a few minutes" >&2
  exit 1
fi
echo "ok: $BUCKET versioned, object lock $LOCK_MODE ${LOCK_DAYS}d"
