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

variable "state_bucket" {
  description = "state bucket on hetzner object storage, created by make state-bootstrap an operator input"
  type        = string
}

variable "state_location" {
  description = "object storage location of the state bucket"
  type        = string

  validation {
    condition     = contains(["fsn1", "nbg1", "hel1"], var.state_location)
    error_message = "state_location must be fsn1, nbg1 or hel1."
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

variable "backup_bucket" {
  description = "backup bucket name, created by the ansible storage role"
  type        = string
}

variable "backup_location" {
  description = "object storage location of the backup bucket"
  type        = string
}

variable "edge_server_type" {
  description = "hetzner server type for edge nodes; must be orderable in fsn1, nbg1 and hel1"
  type        = string
}

variable "core_server_type" {
  description = "hetzner server type for core nodes; must be orderable in fsn1 and nbg1"
  type        = string
}

variable "restore_enabled" {
  description = "create the restore node that playbooks/restore.yml restores onto (docs/runbooks/disaster-recovery.md); set false again afterwards"
  type        = bool
  default     = false
}

variable "restore_location" {
  description = "location of the restore node"
  type        = string
  default     = "fsn1"

  validation {
    condition     = contains(["fsn1", "nbg1", "hel1"], var.restore_location)
    error_message = "restore_location must be fsn1, nbg1 or hel1."
  }
}

variable "server_image" {
  description = "hetzner image name for debian 13"
  type        = string
}

variable "probe_enabled" {
  description = "create the scaleway probe; applied in this stage with an operator input"
  type        = bool
  default     = false
}

variable "probe_instance_type" {
  description = "scaleway instance type, required when probe_enabled"
  type        = string
  default     = null
}

variable "probe_image" {
  description = "scaleway image label or id for debian 13, required when probe_enabled"
  type        = string
  default     = null
}
