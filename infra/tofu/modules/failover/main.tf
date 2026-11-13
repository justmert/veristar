# health-checked dns failover with azure traffic manager. one profile per
# public hostname, multivalue routing over the nodes' public ipv4
# addresses: every answer carries all healthy addresses, so a client that
# cannot reach one retries another without a new lookup, and a node whose
# port 80 health answer fails leaves the answer within one probe cycle.
# cloudflare dns points each hostname at its profile by cname (module dns).

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

variable "name_prefix" {
  description = "globally unique prefix of the <prefix>-<label>.trafficmanager.net names"
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]{1,40}[a-z0-9]$", var.name_prefix))
    error_message = "name_prefix must be 3 to 42 lowercase letters, digits and hyphens."
  }
}

variable "profiles" {
  description = "label to the ipv4 addresses behind it and whether it uses 10 s fast probing"
  type = map(object({
    ipv4 = list(string)
    fast = bool
  }))

  validation {
    # with a single address there is nothing to fail over to
    condition     = alltrue([for p in values(var.profiles) : length(p.ipv4) >= 2])
    error_message = "every profile needs at least two addresses."
  }
}

variable "tags" {
  type = map(string)
}

locals {
  endpoints = merge([
    for label, p in var.profiles : {
      for i, ip in p.ipv4 : "${label}/${i}" => { label = label, ip = ip, index = i }
    }
  ]...)
}

resource "azurerm_traffic_manager_profile" "this" {
  for_each = var.profiles

  name                   = "${var.name_prefix}-${each.key}"
  resource_group_name    = var.resource_group_name
  traffic_routing_method = "MultiValue"
  # every address of the tier; the largest set is the three edge nodes
  max_return = length(each.value.ipv4)
  tags       = var.tags

  dns_config {
    relative_name = "${var.name_prefix}-${each.key}"
    # a cached answer outlives a failed node by at most this long
    ttl = each.value.fast ? 10 : 30
  }

  monitor_config {
    # the node's port 80 answers 308 to https when its composite health
    # check passes and 503 when it fails or the node drains
    # (deploy/caddy/assemble.sh). traffic manager does not follow redirects,
    # so the 308 itself is the healthy answer.
    protocol                     = "HTTP"
    port                         = 80
    path                         = "/"
    expected_status_code_ranges  = ["200-299", "308-308"]
    interval_in_seconds          = each.value.fast ? 10 : 30
    timeout_in_seconds           = each.value.fast ? 5 : 10
    tolerated_number_of_failures = 2
  }
}

resource "azurerm_traffic_manager_external_endpoint" "this" {
  for_each = local.endpoints

  name       = "${each.value.label}-${each.value.index + 1}"
  profile_id = azurerm_traffic_manager_profile.this[each.value.label].id
  target     = each.value.ip
  enabled    = true
  # multivalue profiles take ipv4 or ipv6 targets only; there is no
  # priority or weight to set
}

output "fqdns" {
  description = "label to the profile's trafficmanager.net name, the cname target"
  value       = { for label, p in azurerm_traffic_manager_profile.this : label => p.fqdn }
}

output "endpoints" {
  description = "every endpoint by key, for review and tests"
  value = {
    for k, e in azurerm_traffic_manager_external_endpoint.this : k => {
      target = e.target
      label  = local.endpoints[k].label
    }
  }
}

output "monitors" {
  description = "label to the monitor settings, for review and tests"
  value = {
    for label, p in azurerm_traffic_manager_profile.this : label => {
      routing  = p.traffic_routing_method
      ttl      = p.dns_config[0].ttl
      protocol = p.monitor_config[0].protocol
      port     = p.monitor_config[0].port
      path     = p.monitor_config[0].path
      interval = p.monitor_config[0].interval_in_seconds
      codes    = p.monitor_config[0].expected_status_code_ranges
    }
  }
}
