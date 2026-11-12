terraform {
  required_providers {
    scaleway = {
      source = "scaleway/scaleway"
    }
  }
}

variable "name" {
  type = string
}

variable "zone" {
  type = string
}

variable "instance_type" {
  type = string
}

variable "image" {
  type = string
}

variable "root_volume_gb" {
  type = number
}

variable "admin_cidrs" {
  type = list(string)
}

variable "admin_user" {
  type = string
}

variable "admin_ssh_public_keys" {
  type = list(string)
}

variable "tags" {
  type = list(string)
}

resource "scaleway_instance_ip" "v4" {
  type = "routed_ipv4"
  zone = var.zone
  tags = var.tags
}

# an explicit group: without one scaleway creates a default group that
# opentofu does not track
resource "scaleway_instance_security_group" "this" {
  name                    = var.name
  zone                    = var.zone
  inbound_default_policy  = "drop"
  outbound_default_policy = "accept"
  stateful                = true
  tags                    = var.tags

  inbound_rule {
    action   = "accept"
    protocol = "TCP"
    port     = 80
  }

  inbound_rule {
    action   = "accept"
    protocol = "TCP"
    port     = 443
  }

  dynamic "inbound_rule" {
    for_each = var.admin_cidrs
    content {
      action   = "accept"
      protocol = "TCP"
      port     = 22
      ip_range = inbound_rule.value
    }
  }
}

resource "scaleway_instance_server" "this" {
  name              = var.name
  zone              = var.zone
  type              = var.instance_type
  image             = var.image
  ip_ids            = [scaleway_instance_ip.v4.id]
  security_group_id = scaleway_instance_security_group.this.id
  tags              = var.tags

  root_volume {
    size_in_gb = var.root_volume_gb
  }

  user_data = {
    cloud-init = "#cloud-config\n${yamlencode({
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

  lifecycle {
    ignore_changes = [user_data]
  }
}

output "name" {
  value = scaleway_instance_server.this.name
}

output "public_ipv4" {
  value = scaleway_instance_ip.v4.address
}
