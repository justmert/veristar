terraform {
  required_providers {
    azurerm = {
      source = "hashicorp/azurerm"
    }
  }
}

variable "name_prefix" {
  description = "prefix for resource names, the environment name"
  type        = string
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  description = "azure region"
  type        = string
}

variable "ip_range" {
  description = "virtual network range, a /16"
  type        = string

  validation {
    condition     = can(cidrhost(var.ip_range, 0)) && endswith(var.ip_range, "/16")
    error_message = "ip_range must be an IPv4 /16."
  }
}

variable "zones" {
  description = "availability zone to subnet number; subnet n is the n-th /24 of ip_range"
  type        = map(number)
}

variable "admin_cidrs" {
  description = "source ranges allowed to reach ssh"
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

variable "tags" {
  type = map(string)
}

resource "azurerm_virtual_network" "this" {
  name                = "${var.name_prefix}-vnet"
  resource_group_name = var.resource_group_name
  location            = var.location
  address_space       = [var.ip_range]
  tags                = var.tags
}

# azure subnets span every zone of the region; one /24 per zone only makes a
# private address tell which zone a node runs in. the storage service
# endpoint lets the backup storage account accept these subnets while it
# refuses every other network.
resource "azurerm_subnet" "zone" {
  for_each = var.zones

  name                 = "zone-${each.key}"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = [cidrsubnet(var.ip_range, 8, each.value)]
  # every node has its own public address, so azure's implicit outbound
  # access is never used; turning it off keeps a node without a public
  # address from reaching the internet by accident
  default_outbound_access_enabled = false

  service_endpoint {
    service = "Microsoft.Storage"
  }
}

# the nsg filters internet traffic. azure's default rules allow traffic
# inside the virtual network, which is left to the host firewall of the
# ansible base role and to the internal mtls, as on every private link.
resource "azurerm_network_security_group" "public" {
  name                = "${var.name_prefix}-public"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = var.tags
}

locals {
  rules = {
    # http on port 80 is also the traffic manager health probe
    http  = { priority = 100, protocol = "Tcp", port = "80", sources = ["Internet"], description = "http and the traffic manager health probe" }
    https = { priority = 110, protocol = "Tcp", port = "443", sources = ["Internet"], description = "https and otlp grpc" }
    h3    = { priority = 120, protocol = "Udp", port = "443", sources = ["Internet"], description = "http/3, caddy serves it on udp 443" }
    icmp  = { priority = 130, protocol = "Icmp", port = "*", sources = ["Internet"], description = "icmp, keeps path mtu discovery working" }
    ssh   = { priority = 140, protocol = "Tcp", port = "22", sources = var.admin_cidrs, description = "ssh from admin ranges only" }
  }
}

resource "azurerm_network_security_rule" "inbound" {
  for_each = local.rules

  name                        = each.key
  description                 = each.value.description
  resource_group_name         = var.resource_group_name
  network_security_group_name = azurerm_network_security_group.public.name
  priority                    = each.value.priority
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = each.value.protocol
  source_port_range           = "*"
  destination_port_range      = each.value.port
  source_address_prefix       = length(each.value.sources) == 1 ? each.value.sources[0] : null
  source_address_prefixes     = length(each.value.sources) == 1 ? null : each.value.sources
  destination_address_prefix  = "*"
}

# anything else from the internet; above azure's default rules (65000+),
# below the allow rules
resource "azurerm_network_security_rule" "deny_internet" {
  name                        = "deny-internet"
  description                 = "everything else from the internet"
  resource_group_name         = var.resource_group_name
  network_security_group_name = azurerm_network_security_group.public.name
  priority                    = 4000
  direction                   = "Inbound"
  access                      = "Deny"
  protocol                    = "*"
  source_port_range           = "*"
  destination_port_range      = "*"
  source_address_prefix       = "Internet"
  destination_address_prefix  = "*"
}

resource "azurerm_subnet_network_security_group_association" "zone" {
  for_each = azurerm_subnet.zone

  subnet_id                 = each.value.id
  network_security_group_id = azurerm_network_security_group.public.id
}

output "network_id" {
  value = azurerm_virtual_network.this.id
}

output "ip_range" {
  value = var.ip_range
}

output "subnets" {
  description = "zone to subnet id and range"
  value = {
    for z, s in azurerm_subnet.zone : z => {
      id       = s.id
      ip_range = s.address_prefixes[0]
    }
  }
}

output "public_nsg_id" {
  value = azurerm_network_security_group.public.id
}
