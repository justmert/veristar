locals {
  environment = "production"

  labels = {
    environment = local.environment
    managed-by  = "opentofu"
  }

  admin_user = "ops"

  locations = {
    fsn1 = 1
    nbg1 = 2
    hel1 = 3
  }

  # three edge nodes in three locations, two core nodes in two
  nodes = {
    "${local.environment}-edge-1" = { tier = "edge", index = 1, location = "fsn1" }
    "${local.environment}-edge-2" = { tier = "edge", index = 2, location = "nbg1" }
    "${local.environment}-edge-3" = { tier = "edge", index = 3, location = "hel1" }
    "${local.environment}-core-1" = { tier = "core", index = 1, location = "fsn1" }
    "${local.environment}-core-2" = { tier = "core", index = 2, location = "nbg1" }
  }

  # host part encodes the tier: .11+ edge, .21+ core
  host_base = { edge = 10, core = 20 }

  # bunny.net does not document a minimum ttl; checked on the first apply
  dns_ttl = 60
}

module "network" {
  source = "../../modules/network"

  name_prefix  = local.environment
  ip_range     = local.ip_range
  network_zone = "eu-central"
  locations    = local.locations
  admin_cidrs  = var.admin_cidrs
  labels       = local.labels
}

resource "hcloud_ssh_key" "admin" {
  for_each = { for i, k in var.admin_ssh_public_keys : tostring(i) => k }

  name       = "${local.environment}-admin-${each.key}"
  public_key = each.value
  labels     = local.labels
}

module "node" {
  source   = "../../modules/node"
  for_each = local.nodes

  name                  = each.key
  tier                  = each.value.tier
  index                 = each.value.index
  location              = each.value.location
  server_type           = each.value.tier == "edge" ? var.edge_server_type : var.core_server_type
  image                 = var.server_image
  subnet_id             = module.network.subnets[each.value.location].id
  private_ip            = cidrhost(module.network.subnets[each.value.location].ip_range, local.host_base[each.value.tier] + each.value.index)
  firewall_ids          = [module.network.public_firewall_id]
  ssh_key_ids           = [for k in hcloud_ssh_key.admin : k.id]
  admin_user            = local.admin_user
  admin_ssh_public_keys = var.admin_ssh_public_keys
  protect               = local.protect
  labels                = local.labels
}

module "probe" {
  source = "../../modules/probe"
  count  = var.probe_enabled ? 1 : 0

  name                  = "${local.environment}-probe-1"
  zone                  = "fr-par-1"
  instance_type         = var.probe_instance_type
  image                 = var.probe_image
  root_volume_gb        = 20
  admin_cidrs           = var.admin_cidrs
  admin_user            = local.admin_user
  admin_ssh_public_keys = var.admin_ssh_public_keys
  tags                  = ["environment=${local.environment}", "managed-by=opentofu"]
}

module "object_storage" {
  source = "../../modules/object-storage"

  location      = var.backup_location
  backup_bucket = var.backup_bucket
}

locals {
  edge = [for name, n in local.nodes : module.node[name] if n.tier == "edge"]
  core = [for name, n in local.nodes : module.node[name] if n.tier == "core"]
}

module "dns" {
  source = "../../modules/dns"

  base_domain = var.base_domain
  ttl         = local.dns_ttl
  record_sets = {
    in   = { ipv4 = local.edge[*].public_ipv4, ipv6 = local.edge[*].public_ipv6 }
    otlp = { ipv4 = local.edge[*].public_ipv4, ipv6 = local.edge[*].public_ipv6 }
    app  = { ipv4 = local.core[*].public_ipv4, ipv6 = local.core[*].public_ipv6 }
    web  = { ipv4 = local.core[*].public_ipv4, ipv6 = local.core[*].public_ipv6 }
  }
  single_records = var.probe_enabled ? {
    status = { ipv4 = [module.probe[0].public_ipv4], ipv6 = [] }
  } : {}
  # caddy gets every certificate from let's encrypt by dns-01
  caa_issuers = ["letsencrypt.org"]
}

check "probe_inputs" {
  assert {
    condition     = !var.probe_enabled || (var.probe_instance_type != null && var.probe_image != null)
    error_message = "probe_enabled needs probe_instance_type and probe_image an operator input."
  }
}

module "inventory" {
  source = "../../modules/inventory"

  environment          = local.environment
  base_domain          = var.base_domain
  hostnames            = merge(module.dns.hostnames, { status = "status.${var.base_domain}" })
  private_network_cidr = module.network.ip_range
  admin_cidrs          = var.admin_cidrs
  admin_user           = local.admin_user
  object_storage       = module.object_storage.settings
  nodes = merge(
    {
      for name, n in local.nodes : name => {
        tier        = n.tier
        index       = n.index
        provider    = "hetzner"
        location    = n.location
        public_ipv4 = module.node[name].public_ipv4
        public_ipv6 = module.node[name].public_ipv6
        private_ip  = module.node[name].private_ip
      }
    },
    {
      for p in module.probe : p.name => {
        tier        = "probe"
        index       = 1
        provider    = "scaleway"
        location    = "fr-par-1"
        public_ipv4 = p.public_ipv4
        public_ipv6 = null
        private_ip  = null
      }
    },
  )
}
