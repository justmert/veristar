# every input without a default is supplied by the operator (section 3 of the
# plan); there are deliberately no stand-in values.

variable "base_domain" {
  description = "BASE_DOMAIN, an operator input"
  type        = string

  validation {
    condition     = can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", var.base_domain))
    error_message = "base_domain must be a lowercase domain name."
  }
}

variable "admin_cidrs" {
  description = "admin source ranges allowed to reach ssh, an operator input"
  type        = list(string)

  validation {
    condition     = length(var.admin_cidrs) > 0 && alltrue([for c in var.admin_cidrs : can(cidrhost(c, 0))])
    error_message = "admin_cidrs must be a non-empty list of CIDRs."
  }

  validation {
    condition     = !anytrue([for c in var.admin_cidrs : endswith(c, "/0")])
    error_message = "admin_cidrs must not open ssh to the whole internet."
  }
}

variable "admin_ssh_public_keys" {
  description = "ssh public keys installed for the admin user by cloud-init"
  type        = list(string)

  validation {
    condition     = length(var.admin_ssh_public_keys) > 0 && alltrue([for k in var.admin_ssh_public_keys : can(regex("^(ssh-ed25519|ecdsa-sha2-nistp256|ssh-rsa|sk-ssh-ed25519@openssh.com) [A-Za-z0-9+/=]+( .*)?$", k))])
    error_message = "admin_ssh_public_keys must be a non-empty list of openssh public keys."
  }
}

variable "subscription_id" {
  description = "azure subscription id, an operator input"
  type        = string

  validation {
    condition     = can(regex("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", var.subscription_id))
    error_message = "subscription_id must be a subscription guid."
  }
}

variable "state_resource_group" {
  description = "resource group of the state storage account, created by make state-bootstrap"
  type        = string
}

variable "state_storage_account" {
  description = "state storage account, created by make state-bootstrap an operator input"
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9]{3,24}$", var.state_storage_account))
    error_message = "storage account names are 3 to 24 lowercase letters and digits."
  }
}

variable "state_passphrase" {
  description = "state and plan encryption passphrase, from TF_VAR_state_passphrase (sops)"
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.state_passphrase) >= 16
    error_message = "state_passphrase must be at least 16 characters."
  }
}

variable "backup_storage_account" {
  description = "storage account of the pgbackrest repository, unique across azure"
  type        = string
}

variable "backup_lock_state" {
  description = "Unlocked or Locked; see docs/deploy/backups.md before locking"
  type        = string
  default     = "Unlocked"
}

variable "location" {
  description = "azure region; it must offer node_vm_size in availability zones 1, 2 and 3"
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9]+$", var.location))
    error_message = "location must be an azure region name such as germanywestcentral."
  }
}

variable "node_vm_size" {
  description = "vm size of the three converged nodes and the restore node; must be offered in zones 1, 2 and 3"
  type        = string
  default     = "Standard_B2as_v2"
}

variable "restore_enabled" {
  description = "create the restore node that playbooks/restore.yml restores onto (docs/runbooks/disaster-recovery.md); set false again afterwards"
  type        = bool
  default     = false
}

variable "restore_zone" {
  description = "availability zone of the restore node"
  type        = string
  default     = "3"

  validation {
    condition     = contains(["1", "2", "3"], var.restore_zone)
    error_message = "restore_zone must be 1, 2 or 3."
  }
}

variable "server_image_version" {
  description = "version of the Debian debian-13 13-gen2 marketplace image (az vm image list -p Debian -f debian-13 -s 13-gen2 --all)"
  type        = string

  validation {
    condition     = can(regex("^[0-9]+\\.[0-9]+\\.[0-9]+$", var.server_image_version))
    error_message = "server_image_version must be an exact image version, not latest."
  }
}
