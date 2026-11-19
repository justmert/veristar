# records in the existing cloudflare zone of BASE_DOMAIN. the
# zone itself belongs to the registrar account and is only looked up here.
# every record is dns only: the gateway needs the client's own address, and
# a proxy in front of the collect path would replace it.

terraform {
  required_providers {
    cloudflare = {
      source = "cloudflare/cloudflare"
    }
  }
}

variable "base_domain" {
  description = "zone apex"
  type        = string
}

variable "ttl" {
  description = "ttl of the cname records in seconds; cloudflare's minimum for dns-only records is 60"
  type        = number

  validation {
    condition     = var.ttl >= 60
    error_message = "ttl must be at least 60."
  }
}

variable "cnames" {
  description = "label to the name the label points at (a traffic manager profile)"
  type        = map(string)
}

variable "site" {
  description = "whether the zone apex and www point at site_target; false leaves both names alone. a separate flag because site_target is only known after apply on the first run, and for_each needs its keys at plan time"
  type        = bool
  default     = false
}

variable "site_target" {
  description = "the name the website at the zone apex and www point at (a traffic manager profile), used when site is true"
  type        = string
  default     = null
}

variable "a_records" {
  description = "label to an ipv4 address, for names served outside the platform's nodes (the status page)"
  type        = map(string)
  default     = {}
}

variable "caa_issuers" {
  description = "certificate authorities allowed to issue for the zone"
  type        = list(string)
}

data "cloudflare_zone" "this" {
  filter = {
    name = var.base_domain
  }
}

locals {
  zone_id = data.cloudflare_zone.this.zone_id
}

resource "cloudflare_dns_record" "cname" {
  for_each = var.cnames

  zone_id = local.zone_id
  name    = "${each.key}.${var.base_domain}"
  type    = "CNAME"
  content = each.value
  ttl     = var.ttl
  proxied = false
  comment = "managed by opentofu; health-checked by azure traffic manager"
}

# the public website at the zone apex and www. the apex cannot hold a plain
# cname, so cloudflare flattens it: it resolves the profile and answers
# with its addresses, on every plan. dns only, like every other name here,
# so the nodes' caddy terminates tls and the profile's health decides the
# answer. www redirects to the apex in caddy (deploy/caddy/app.Caddyfile).
resource "cloudflare_dns_record" "site" {
  for_each = var.site ? { "@" = var.base_domain, www = "www.${var.base_domain}" } : {}

  zone_id = local.zone_id
  name    = each.value
  type    = "CNAME"
  content = var.site_target
  ttl     = var.ttl
  proxied = false
  comment = "managed by opentofu; health-checked by azure traffic manager"
}

# a name served by a host outside the platform, such as the status page on
# the probe host (docs/deploy/probe.md); dns only, so that host's caddy gets
# its own certificate
resource "cloudflare_dns_record" "a" {
  for_each = var.a_records

  zone_id = local.zone_id
  name    = "${each.key}.${var.base_domain}"
  type    = "A"
  content = each.value
  ttl     = var.ttl
  proxied = false
  comment = "managed by opentofu"
}

resource "cloudflare_dns_record" "caa_issue" {
  for_each = toset(var.caa_issuers)

  zone_id = local.zone_id
  name    = var.base_domain
  type    = "CAA"
  ttl     = 3600
  data = {
    flags = 0
    tag   = "issue"
    value = each.value
  }
  comment = "managed by opentofu"
}

# no wildcard certificates are used anywhere
resource "cloudflare_dns_record" "caa_issuewild" {
  zone_id = local.zone_id
  name    = var.base_domain
  type    = "CAA"
  ttl     = 3600
  data = {
    flags = 0
    tag   = "issuewild"
    value = ";"
  }
  comment = "managed by opentofu"
}

output "zone_id" {
  value = local.zone_id
}

output "nameservers" {
  description = "cloudflare's nameservers for the zone, set by cloudflare registrar"
  value       = data.cloudflare_zone.this.name_servers
}

output "hostnames" {
  description = "label to hostname; site is the website at the zone apex"
  value = merge(
    { for label in keys(var.cnames) : label => "${label}.${var.base_domain}" },
    var.site ? { site = var.base_domain } : {},
  )
}

output "a_hostnames" {
  value = { for label in keys(var.a_records) : label => "${label}.${var.base_domain}" }
}

output "records" {
  description = "every record by key, for review and tests"
  value = merge(
    { for k, r in cloudflare_dns_record.cname : "${k}/CNAME" => { type = r.type, value = r.content, proxied = r.proxied } },
    { for k, r in cloudflare_dns_record.site : "${k}/CNAME" => { type = r.type, value = r.content, proxied = r.proxied } },
    { for k, r in cloudflare_dns_record.a : "${k}/A" => { type = r.type, value = r.content, proxied = r.proxied } },
    { for k, r in cloudflare_dns_record.caa_issue : "@/CAA/issue/${k}" => { type = r.type, value = r.data.value, proxied = false } },
    { "@/CAA/issuewild" = { type = "CAA", value = cloudflare_dns_record.caa_issuewild.data.value, proxied = false } },
  )
}
