# the pgbackrest repository (docs/deploy/backups.md): one storage account
# with zone-redundant storage, blob versioning and a version-level
# time-based retention policy, reached by the core nodes through their
# managed identity. no account key is ever used.
#
# how the protection works:
#   - the core identity holds storage blob data contributor on the
#     container. that role can write and delete the current version of a
#     blob, but not delete a previous version (deleteBlobVersion is in the
#     owner role only, azure docs "blob versioning", checked 2026-11-13).
#     with versioning, a delete turns the current version into a previous
#     version, so whatever holds the node's identity cannot remove data.
#   - previous versions are removed by the lifecycle policy after
#     previous_version_days, which must cover the backup retention.
#   - every version is immutable for lock_days after it was written. azure
#     refuses to delete a blob while its retention runs, so lock_days must
#     stay below the age of the youngest file pgbackrest expires (the
#     ansible storage role checks this; docs/deploy/backups.md).

terraform {
  required_providers {
    azurerm = {
      source = "hashicorp/azurerm"
    }
  }
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  type = string
}

variable "account_name" {
  description = "storage account name, unique across azure"
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9]{3,24}$", var.account_name))
    error_message = "storage account names are 3 to 24 lowercase letters and digits."
  }
}

variable "container_name" {
  type = string

  validation {
    condition     = can(regex("^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$", var.container_name))
    error_message = "container names are 3 to 63 lowercase letters, digits and hyphens."
  }
}

variable "lock_days" {
  description = "days every blob version stays immutable after it was written"
  type        = number

  validation {
    condition     = var.lock_days >= 1 && var.lock_days == floor(var.lock_days)
    error_message = "lock_days must be a whole number of days, at least 1."
  }
}

variable "lock_state" {
  description = "Unlocked (the period can still be changed) or Locked (it can only grow, and the account cannot be deleted while a version is held)"
  type        = string

  validation {
    condition     = contains(["Unlocked", "Locked"], var.lock_state)
    error_message = "lock_state must be Unlocked or Locked."
  }
}

variable "previous_version_days" {
  description = "days a previous version is kept before the lifecycle policy deletes it"
  type        = number

  validation {
    condition     = var.previous_version_days >= 1
    error_message = "previous_version_days must be at least 1."
  }
}

variable "allowed_subnet_ids" {
  description = "subnets whose nodes may reach the account (storage service endpoint)"
  type        = list(string)
}

variable "admin_ipv4_ranges" {
  description = "admin source ranges allowed to reach the account, ipv4 only (azure takes no ipv6 here)"
  type        = list(string)
}

variable "writer_principal_ids" {
  description = "managed identity principals that read and write the repository"
  type        = map(string)
}

variable "protect" {
  description = "a delete lock on the account"
  type        = bool
}

variable "tags" {
  type = map(string)
}

resource "azurerm_storage_account" "this" {
  name                     = var.account_name
  resource_group_name      = var.resource_group_name
  location                 = var.location
  account_kind             = "StorageV2"
  account_tier             = "Standard"
  account_replication_type = "ZRS"
  access_tier              = "Hot"
  min_tls_version          = "TLS1_2"
  # entra id only: no account key and no shared access signature works
  shared_access_key_enabled        = false
  default_to_oauth_authentication  = true
  allow_nested_items_to_be_public  = false
  cross_tenant_replication_enabled = false
  # pgbackrest's expire fails on a hierarchical namespace (its user guide),
  # and version-level immutability needs a flat one
  is_hns_enabled = false
  tags           = var.tags

  blob_properties {
    versioning_enabled = true

    container_delete_retention_policy {
      days = var.previous_version_days
    }
  }

  immutability_policy {
    allow_protected_append_writes = false
    period_since_creation_in_days = var.lock_days
    state                         = var.lock_state
  }

  network_rules {
    default_action = "Deny"
    # azure ip rules take single addresses or ranges up to /30
    ip_rules                   = [for c in var.admin_ipv4_ranges : endswith(c, "/32") ? cidrhost(c, 0) : c]
    virtual_network_subnet_ids = var.allowed_subnet_ids
    bypass                     = ["AzureServices"]
  }
}

resource "azurerm_storage_container" "repo" {
  name                  = var.container_name
  storage_account_id    = azurerm_storage_account.this.id
  container_access_type = "private"
}

resource "azurerm_storage_management_policy" "this" {
  storage_account_id = azurerm_storage_account.this.id

  rule {
    name    = "previous-versions"
    enabled = true

    filters {
      blob_types   = ["blockBlob"]
      prefix_match = ["${var.container_name}/"]
    }

    actions {
      version {
        delete_after_days_since_creation = var.previous_version_days
      }
    }
  }
}

resource "azurerm_role_assignment" "writer" {
  for_each = var.writer_principal_ids

  scope                = azurerm_storage_container.repo.id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = each.value
  principal_type       = "ServicePrincipal"
}

resource "azurerm_management_lock" "account" {
  count = var.protect ? 1 : 0

  name       = "${var.account_name}-no-delete"
  scope      = azurerm_storage_account.this.id
  lock_level = "CanNotDelete"
  notes      = "managed by opentofu; holds the pgbackrest repository"
}

output "settings" {
  description = "the inventory's sa_object_storage"
  value = {
    endpoint      = trimsuffix(azurerm_storage_account.this.primary_blob_endpoint, "/")
    region        = var.location
    backup_bucket = azurerm_storage_container.repo.name
  }
}

output "account_id" {
  value = azurerm_storage_account.this.id
}
