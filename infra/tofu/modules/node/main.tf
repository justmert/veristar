terraform {
  required_providers {
    hcloud = {
      source = "hetznercloud/hcloud"
    }
  }
}

variable "name" {
  type = string
}

variable "tier" {
  type = string

  validation {
    condition     = contains(["edge", "core"], var.tier)
    error_message = "tier must be edge or core."
  }
}

variable "index" {
  type = number
}

variable "location" {
  type = string
}

variable "server_type" {
  type = string
}

variable "image" {
  type = string
}

variable "subnet_id" {
  type = string
}

variable "private_ip" {
  type = string
}

variable "firewall_ids" {
  type = list(number)
}

variable "ssh_key_ids" {
  type = list(string)
}

variable "admin_user" {
  type = string
}

variable "admin_ssh_public_keys" {
  type = list(string)
}

variable "protect" {
  description = "delete and rebuild protection"
  type        = bool
}

variable "labels" {
  type = map(string)
}

locals {
  labels = merge(var.labels, { tier = var.tier })

  # cloud-init only creates the admin user; everything else is ansible, so a
  # converge can be rerun without touching the server
  user_data = "#cloud-config\n${yamlencode({
    users = [{
      name                = var.admin_user
      groups              = ["sudo"]
      shell               = "/bin/bash"
      sudo                = "ALL=(ALL) NOPASSWD:ALL"
      lock_passwd         = true
      ssh_authorized_keys = var.admin_ssh_public_keys
    }]
    ssh_pwauth = false
  })}"
}

# separate primary ips keep the public address when the server is rebuilt,
# so dns and allowlists elsewhere stay valid
resource "hcloud_primary_ip" "v4" {
  name              = "${var.name}-v4"
  type              = "ipv4"
  location          = var.location
  auto_delete       = false
  delete_protection = var.protect
  labels            = local.labels
}

resource "hcloud_primary_ip" "v6" {
  name              = "${var.name}-v6"
  type              = "ipv6"
  location          = var.location
  auto_delete       = false
  delete_protection = var.protect
  labels            = local.labels
}

resource "hcloud_server" "this" {
  name               = var.name
  server_type        = var.server_type
  image              = var.image
  location           = var.location
  ssh_keys           = var.ssh_key_ids
  user_data          = local.user_data
  firewall_ids       = var.firewall_ids
  labels             = local.labels
  delete_protection  = var.protect
  rebuild_protection = var.protect
  # hetzner backups are whole-disk snapshots; postgres backups are pgbackrest
  # (this stage), so these would only add cost
  backups = false

  public_net {
    ipv4_enabled = true
    ipv4         = hcloud_primary_ip.v4.id
    ipv6_enabled = true
    ipv6         = hcloud_primary_ip.v6.id
  }

  network {
    subnet_id = var.subnet_id
    ip        = var.private_ip
    # the provider docs: without this the network is detached and attached on
    # every apply
    alias_ips = []
  }

  lifecycle {
    # key rotation happens through ansible; a change here must not replace a node
    ignore_changes = [ssh_keys, user_data]
  }
}

output "name" {
  value = hcloud_server.this.name
}

output "public_ipv4" {
  value = hcloud_server.this.ipv4_address
}

output "public_ipv6" {
  value = hcloud_server.this.ipv6_address
}

output "private_ip" {
  value = var.private_ip
}
