#!/usr/bin/env bash
# creates the opentofu state storage of one environment in azure: a resource
# group, a storage account (zone-redundant, entra id only, blob versioning,
# 35 days of soft delete) and the tfstate container. run once, before the
# first `tofu init`, by `make state-bootstrap ENV=<env>` with an az cli
# login (`az login`) on the subscription of an operator input.
#
# the state account lives outside the environment's resource group, so a
# `make <env>-destroy` never removes the state it runs on. the backend locks
# the state with a blob lease, and versioning keeps every earlier state
# (scripts/tofu-state-backup.sh lists and restores them).
#
# the script reads back what it set and exits non-zero on anything it could
# not apply. it is safe to rerun.
set -euo pipefail

env_name="${1:?usage: tofu-state-bootstrap.sh <staging|production>}"
case "$env_name" in
  staging|production) ;;
  *) echo "unknown environment: $env_name" >&2; exit 2 ;;
esac

tfvars="infra/tofu/envs/$env_name/terraform.tfvars"
if [[ ! -f "$tfvars" ]]; then
  echo "$tfvars is missing: it needs subscription_id, state_resource_group and state_storage_account an operator input" >&2
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

subscription="$(read_var subscription_id)"
group="$(read_var state_resource_group)"
account="$(read_var state_storage_account)"
location=swedencentral

command -v az >/dev/null || { echo "the az cli is not installed (docs/deploy/prerequisites.md)" >&2; exit 1; }
az account show --subscription "$subscription" --query id -o tsv >/dev/null ||
  { echo "not signed in to subscription $subscription: run az login" >&2; exit 1; }

az_() { az "$@" --subscription "$subscription" --only-show-errors; }

if [[ "$(az_ group exists --name "$group")" != true ]]; then
  az_ group create --name "$group" --location "$location" --tags managed-by=state-bootstrap project=veristar -o none
  echo "created resource group $group"
fi

if ! az_ storage account show --resource-group "$group" --name "$account" -o none 2>/dev/null; then
  az_ storage account create --resource-group "$group" --name "$account" --location "$location" \
    --kind StorageV2 --sku Standard_ZRS --access-tier Hot --min-tls-version TLS1_2 \
    --allow-shared-key-access false --allow-blob-public-access false \
    --tags managed-by=state-bootstrap project=veristar -o none
  echo "created state storage account $account"
fi

az_ storage account blob-service-properties update --resource-group "$group" --account-name "$account" \
  --enable-versioning true --enable-delete-retention true --delete-retention-days 35 \
  --enable-container-delete-retention true --container-delete-retention-days 35 -o none

# the signed-in user writes the state through entra id, so it needs a data
# role on the account; owner of the subscription alone does not grant it
me="$(az ad signed-in-user show --query id -o tsv)"
scope="$(az_ storage account show --resource-group "$group" --name "$account" --query id -o tsv)"
if [[ -z "$(az_ role assignment list --assignee "$me" --scope "$scope" --role 'Storage Blob Data Contributor' --query '[].id' -o tsv)" ]]; then
  az_ role assignment create --assignee-object-id "$me" --assignee-principal-type User \
    --role 'Storage Blob Data Contributor' --scope "$scope" -o none
  echo "granted Storage Blob Data Contributor on $account to the signed-in user"
fi

# a new role assignment takes a few minutes to apply to the data plane
for _ in $(seq 1 30); do
  if az_ storage container create --account-name "$account" --name tfstate --auth-mode login -o none 2>/dev/null; then
    break
  fi
  sleep 10
done

# read everything back
props="$(az_ storage account blob-service-properties show --resource-group "$group" --account-name "$account" \
  --query '[isVersioningEnabled, deleteRetentionPolicy.enabled]' -o tsv | tr '\n' ' ')"
[[ "$props" == "True True " ]] || { echo "versioning or soft delete of $account is not enabled: $props" >&2; exit 1; }
[[ "$(az_ storage account show --resource-group "$group" --name "$account" --query allowSharedKeyAccess -o tsv)" == false ]] ||
  { echo "$account still accepts account keys" >&2; exit 1; }
[[ "$(az_ storage container exists --account-name "$account" --name tfstate --auth-mode login --query exists -o tsv)" == true ]] ||
  { echo "container tfstate of $account does not exist; rerun in a few minutes" >&2; exit 1; }
echo "ok: state storage $account in $group (zrs, versioned, entra id only)"
