terraform {
  required_providers {
    bunnynet = {
      source = "BunnyWay/bunnynet"
    }
  }
}

variable "base_domain" {
  description = "zone apex an operator input"
  type        = string
}

variable "ttl" {
  description = "ttl of the monitored record sets in seconds"
  type        = number
}

variable "record_sets" {
  description = "label to the ipv4 and ipv6 addresses behind it; each address is health monitored"
  type = map(object({
    ipv4 = list(string)
    ipv6 = list(string)
  }))

  validation {
    # bunny.net removes unhealthy records only from a set of two or more;
    # a single record is always returned
    condition     = alltrue([for s in values(var.record_sets) : length(s.ipv4) >= 2])
    error_message = "every monitored record set needs at least two addresses."
  }
}

variable "single_records" {
  description = "label to addresses that are not health monitored (the probe's status page)"
  type = map(object({
    ipv4 = list(string)
    ipv6 = list(string)
  }))
  default = {}
}

variable "caa_issuers" {
  description = "certificate authorities allowed to issue for the zone"
  type        = list(string)
}

resource "bunnynet_dns_zone" "this" {
  domain = var.base_domain
  # query logs would hold resolver addresses; nothing here needs them
  log_enabled = false
}

locals {
  monitored = merge([
    for label, set in var.record_sets : merge(
      { for i, ip in set.ipv4 : "${label}/A/${i}" => { name = label, type = "A", value = ip } },
      { for i, ip in set.ipv6 : "${label}/AAAA/${i}" => { name = label, type = "AAAA", value = ip } },
    )
  ]...)

  single = merge([
    for label, set in var.single_records : merge(
      { for i, ip in set.ipv4 : "${label}/A/${i}" => { name = label, type = "A", value = ip } },
      { for i, ip in set.ipv6 : "${label}/AAAA/${i}" => { name = label, type = "AAAA", value = ip } },
    )
  ]...)
}

resource "bunnynet_dns_record" "monitored" {
  for_each = local.monitored

  zone  = bunnynet_dns_zone.this.id
  name  = each.value.name
  type  = each.value.type
  value = each.value.value
  ttl   = var.ttl
  # http on port 80, any non-5xx within 10 s is healthy; the node's port 80
  # answers 503 when its composite check fails
  monitor_type = "Http"
  # equal weights: bunny.net returns up to three healthy records at random
  weight = 100
}

resource "bunnynet_dns_record" "single" {
  for_each = local.single

  zone  = bunnynet_dns_zone.this.id
  name  = each.value.name
  type  = each.value.type
  value = each.value.value
  ttl   = var.ttl
}

resource "bunnynet_dns_record" "caa_issue" {
  for_each = toset(var.caa_issuers)

  zone  = bunnynet_dns_zone.this.id
  name  = ""
  type  = "CAA"
  flags = 0
  tag   = "issue"
  value = each.value
}

# no wildcard certificates are used anywhere
resource "bunnynet_dns_record" "caa_issuewild" {
  zone  = bunnynet_dns_zone.this.id
  name  = ""
  type  = "CAA"
  flags = 0
  tag   = "issuewild"
  value = ";"
}

output "zone_id" {
  value = bunnynet_dns_zone.this.id
}

output "nameservers" {
  description = "set these at the registrar to delegate the zone"
  value       = [bunnynet_dns_zone.this.nameserver1, bunnynet_dns_zone.this.nameserver2]
}

output "hostnames" {
  value = { for label in concat(keys(var.record_sets), keys(var.single_records)) : label => "${label}.${var.base_domain}" }
}

output "records" {
  description = "every record by key, for review and tests"
  value = merge(
    { for k, r in bunnynet_dns_record.monitored : k => { type = r.type, value = r.value, monitor_type = r.monitor_type } },
    { for k, r in bunnynet_dns_record.single : k => { type = r.type, value = r.value, monitor_type = r.monitor_type } },
    { for k, r in bunnynet_dns_record.caa_issue : "@/CAA/issue/${k}" => { type = r.type, value = r.value, monitor_type = r.monitor_type } },
    { "@/CAA/issuewild" = { type = "CAA", value = bunnynet_dns_record.caa_issuewild.value, monitor_type = bunnynet_dns_record.caa_issuewild.monitor_type } },
  )
}
