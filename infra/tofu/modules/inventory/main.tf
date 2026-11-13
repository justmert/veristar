# pure function from node data to the ansible inventory object described in
# docs/deploy/opentofu.md section 1. kept provider-free so tests can render it.

variable "environment" {
  type = string
}

variable "base_domain" {
  type = string
}

variable "hostnames" {
  type = map(string)
}

variable "private_network_cidr" {
  type = string
}

variable "admin_cidrs" {
  type = list(string)
}

variable "admin_user" {
  type = string
}

variable "object_storage" {
  type = object({
    endpoint      = string
    region        = string
    backup_bucket = string
  })
}

variable "nodes" {
  description = "every node, keyed by server name"
  type = map(object({
    tier        = string
    index       = number
    provider    = string
    location    = string
    public_ipv4 = string
    public_ipv6 = optional(string)
    private_ip  = optional(string)
  }))

  validation {
    condition     = alltrue([for n in values(var.nodes) : contains(["edge", "core", "probe", "restore"], n.tier)])
    error_message = "tier must be edge, core, probe or restore."
  }
}

locals {
  hosts_of = {
    # restore is the fresh node of playbooks/restore.yml, present only while
    # restore_enabled (a point-in-time restore drill or a disaster recovery)
    for tier in ["edge", "core", "probe", "restore"] : tier => {
      for name, n in var.nodes : name => {
        ansible_host   = n.public_ipv4
        ansible_user   = var.admin_user
        sa_tier        = n.tier
        sa_index       = n.index
        sa_provider    = n.provider
        sa_location    = n.location
        sa_public_ipv4 = n.public_ipv4
        sa_public_ipv6 = n.public_ipv6
        sa_private_ip  = n.private_ip
      } if n.tier == tier
    }
  }
}

output "inventory" {
  value = {
    all = {
      vars = {
        sa_environment          = var.environment
        sa_base_domain          = var.base_domain
        sa_hostnames            = var.hostnames
        sa_private_network_cidr = var.private_network_cidr
        sa_admin_cidrs          = var.admin_cidrs
        sa_object_storage       = var.object_storage
      }
      children = {
        for tier, hosts in local.hosts_of : tier => { hosts = hosts }
      }
    }
  }
}
