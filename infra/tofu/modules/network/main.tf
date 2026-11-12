terraform {
  required_providers {
    hcloud = {
      source = "hetznercloud/hcloud"
    }
  }
}

variable "name_prefix" {
  description = "prefix for resource names, the environment name"
  type        = string
}

variable "ip_range" {
  description = "private network range, a /16"
  type        = string

  validation {
    condition     = can(cidrhost(var.ip_range, 0)) && endswith(var.ip_range, "/16")
    error_message = "ip_range must be an IPv4 /16."
  }
}

variable "network_zone" {
  description = "hetzner network zone that holds every location"
  type        = string
}

variable "locations" {
  description = "location name to subnet number; subnet n is the n-th /24 of ip_range"
  type        = map(number)
}

variable "admin_cidrs" {
  description = "source ranges allowed to reach ssh an operator input"
  type        = list(string)

  validation {
    condition     = length(var.admin_cidrs) > 0 && alltrue([for c in var.admin_cidrs : can(cidrhost(c, 0))])
    error_message = "admin_cidrs must be a non-empty list of CIDRs."
  }

  validation {
    condition     = !anytrue([for c in var.admin_cidrs : contains(["0.0.0.0/0", "::/0"], c)])
    error_message = "admin_cidrs must not open ssh to the whole internet."
  }
}

variable "labels" {
  type = map(string)
}

resource "hcloud_network" "this" {
  name     = "${var.name_prefix}-private"
  ip_range = var.ip_range
  labels   = var.labels
}

# subnets live in a network zone, not a location; one /24 per location only
# makes a private address tell where the node runs
resource "hcloud_network_subnet" "location" {
  for_each = var.locations

  network_id   = hcloud_network.this.id
  type         = "cloud"
  network_zone = var.network_zone
  ip_range     = cidrsubnet(var.ip_range, 8, each.value)
}

locals {
  anywhere = ["0.0.0.0/0", "::/0"]
}

# cloud firewalls filter only public interfaces. private network traffic is
# left to the host firewall of the ansible base role.
resource "hcloud_firewall" "public" {
  name   = "${var.name_prefix}-public"
  labels = var.labels

  rule {
    description = "http, also the bunny.net health monitor"
    direction   = "in"
    protocol    = "tcp"
    port        = "80"
    source_ips  = local.anywhere
  }

  rule {
    description = "https and otlp grpc"
    direction   = "in"
    protocol    = "tcp"
    port        = "443"
    source_ips  = local.anywhere
  }

  rule {
    description = "http/3, caddy serves it on udp 443"
    direction   = "in"
    protocol    = "udp"
    port        = "443"
    source_ips  = local.anywhere
  }

  rule {
    description = "icmp, keeps path mtu discovery working"
    direction   = "in"
    protocol    = "icmp"
    source_ips  = local.anywhere
  }

  rule {
    description = "ssh from admin ranges only"
    direction   = "in"
    protocol    = "tcp"
    port        = "22"
    source_ips  = var.admin_cidrs
  }
}

output "network_id" {
  value = hcloud_network.this.id
}

output "ip_range" {
  value = hcloud_network.this.ip_range
}

output "subnets" {
  description = "location to subnet id and range"
  value = {
    for loc, s in hcloud_network_subnet.location : loc => {
      id       = s.id
      ip_range = s.ip_range
    }
  }
}

output "public_firewall_id" {
  value = hcloud_firewall.public.id
}
