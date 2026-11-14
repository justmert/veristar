#!/usr/bin/env bash
# lists and restores earlier versions of an environment's opentofu state,
# so a bad apply or a corrupted state can be rolled back. the state account
# that scripts/tofu-state-bootstrap.sh creates has blob versioning and soft
# delete, so every state write keeps the one before it as a version; no
# separate copy is needed.
#
#   scripts/tofu-state-backup.sh <staging|production> [list]
#   scripts/tofu-state-backup.sh <staging|production> restore <version-id>
#
# restore copies a listed version over the current state blob, which itself
# becomes a version; run it only with no tofu command running
# (docs/deploy/opentofu.md section 6). the versions stay encrypted with the
# state passphrase: nothing is decrypted here. needs an az cli login.
set -euo pipefail

env_name="${1:?usage: tofu-state-backup.sh <staging|production> [list|restore <version-id>]}"
action="${2:-list}"
case "$env_name" in
  staging|production) ;;
  *) echo "unknown environment: $env_name" >&2; exit 2 ;;
esac

tfvars="infra/tofu/envs/$env_name/terraform.tfvars"
[[ -f "$tfvars" ]] || { echo "$tfvars is missing (copy terraform.tfvars.example)" >&2; exit 1; }

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

subscription="$(read_var subscription_id)"
account="$(read_var state_storage_account)"
# the state key of envs/<env>/versions.tf
state_key="$env_name/tofu.tfstate"

az_() { az "$@" --subscription "$subscription" --only-show-errors --auth-mode login; }

case "$action" in
  list)
    az_ storage blob list --account-name "$account" --container-name tfstate --prefix "$state_key" \
      --include v --query "[].[versionId, isCurrentVersion, properties.lastModified, properties.contentLength]" -o tsv
    ;;
  restore)
    version="${3:?restore needs a version id, as list prints it}"
    [[ "$version" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z$ ]] || { echo "$version is not a blob version id" >&2; exit 2; }
    src="https://${account}.blob.core.windows.net/tfstate/${state_key}?versionid=${version}"
    az_ storage blob copy start --account-name "$account" --destination-container tfstate \
      --destination-blob "$state_key" --source-uri "$src" -o none
    echo "restored version $version of $state_key in $account"
    ;;
  *) echo "unknown action $action" >&2; exit 2 ;;
esac
