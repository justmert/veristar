locals {
  environment = "production"
  location    = var.location

  tags = {
    environment = local.environment
    managed-by  = "opentofu"
    project     = "veristar"
  }

  admin_user = "ops"

  # availability zone to subnet number
  zones = {
    "1" = 1
    "2" = 2
    "3" = 3
  }

  # three converged nodes, one per zone (decided 2026-11-13,
  # docs/reference/availability.md): every node runs the edge and the core
  # roles, so every service, the nats cluster, the etcd store and the
  # patroni cluster have one member in each zone. while restore_enabled, the
  # restore node of playbooks/restore.yml joins them, sized like a node
  # because it holds a full copy of the database
  nodes = merge({
    "${local.environment}-node-1" = { tier = "node", index = 1, zone = "1" }
    "${local.environment}-node-2" = { tier = "node", index = 2, zone = "2" }
    "${local.environment}-node-3" = { tier = "node", index = 3, zone = "3" }
    },
    var.restore_enabled ? {
      "${local.environment}-restore-1" = { tier = "restore", index = 1, zone = var.restore_zone }
    } : {},
  )

  # host part encodes the tier: .11 to .13 the nodes, .41 the restore node
  host_base = { node = 10, restore = 40 }

  # each os disk holds the jetstream store, postgresql with its wal, and
  # prometheus (docs/deploy/prerequisites.md, cost table)
  disk_gb = { node = 128, restore = 128 }

  image = {
    publisher = "Debian"
    offer     = "debian-13"
    sku       = "13-gen2"
    version   = var.server_image_version
  }
}

resource "azurerm_resource_group" "this" {
  name     = "veristar-${local.environment}"
  location = local.location
  tags     = local.tags
}

module "network" {
  source = "../../modules/network"

  name_prefix         = local.environment
  resource_group_name = azurerm_resource_group.this.name
  location            = local.location
  ip_range            = local.ip_range
  zones               = local.zones
  admin_cidrs         = var.admin_cidrs
  public_ingress_open = var.public_ingress_open
  tags                = local.tags
}

# the identity of every node and the restore node: pgbackrest reaches the
# backup container with it (repo-azure-key-type=auto), so no storage key
# exists
resource "azurerm_user_assigned_identity" "node" {
  name                = "${local.environment}-node"
  resource_group_name = azurerm_resource_group.this.name
  location            = local.location
  tags                = local.tags
}

module "node" {
  source   = "../../modules/node"
  for_each = local.nodes

  name                  = each.key
  tier                  = each.value.tier
  index                 = each.value.index
  resource_group_name   = azurerm_resource_group.this.name
  location              = local.location
  zone                  = each.value.zone
  size                  = var.node_vm_size
  image                 = local.image
  os_disk_gb            = local.disk_gb[each.value.tier]
  os_disk_type          = "StandardSSD_LRS"
  subnet_id             = module.network.subnets[each.value.zone].id
  private_ip            = cidrhost(module.network.subnets[each.value.zone].ip_range, local.host_base[each.value.tier] + each.value.index)
  identity_ids          = [azurerm_user_assigned_identity.node.id]
  admin_user            = local.admin_user
  admin_ssh_public_keys = var.admin_ssh_public_keys
  protect               = local.protect
  tags                  = local.tags
}

module "backup_storage" {
  source = "../../modules/backup-storage"

  resource_group_name = azurerm_resource_group.this.name
  location            = local.location
  account_name        = var.backup_storage_account
  container_name      = "pgbackrest"
  # below the youngest file pgbackrest expires (eight days with the
  # retention of roles/backup); docs/deploy/backups.md
  lock_days             = 7
  lock_state            = var.backup_lock_state
  previous_version_days = 35
  allowed_subnet_ids    = [for s in values(module.network.subnets) : s.id]
  admin_ipv4_ranges     = [for c in var.admin_cidrs : c if !strcontains(c, ":")]
  writer_principal_ids  = { node = azurerm_user_assigned_identity.node.principal_id }
  protect               = local.protect
  tags                  = local.tags
}

locals {
  serving = [for name, n in local.nodes : module.node[name] if n.tier == "node"]
}

module "failover" {
  source = "../../modules/failover"

  resource_group_name = azurerm_resource_group.this.name
  name_prefix         = "${replace(var.base_domain, ".", "-")}-${local.environment}"
  # every hostname is served by all three nodes. ingestion (99.99 %) gets
  # 10 s probing of the node's edge health, the console (99.90 %) 30 s
  # probing of its core health (deploy/caddy/assemble.sh, tier node). umami
  # has no public hostname: it stays on the private network
  profiles = {
    in   = { ipv4 = local.serving[*].public_ipv4, fast = true, path = "/" }
    otlp = { ipv4 = local.serving[*].public_ipv4, fast = true, path = "/" }
    app  = { ipv4 = local.serving[*].public_ipv4, fast = false, path = "/core" }
    # the public website at the zone apex and www: static files that each
    # node's caddy serves by itself, so its health ("/site") is caddy's own
    # and stays up while the control plane is down
    site = { ipv4 = local.serving[*].public_ipv4, fast = false, path = "/site" }
  }
  tags = local.tags
}

module "dns" {
  source = "../../modules/dns"

  base_domain = var.base_domain
  # the cname never changes; failover happens behind it in traffic manager,
  # whose answers carry their own ttl (module failover)
  ttl    = 300
  cnames = { for label, fqdn in module.failover.fqdns : label => fqdn if label != "site" }
  # the apex and www, flattened by cloudflare (module dns)
  site        = true
  site_target = module.failover.fqdns["site"]
  # caddy gets every certificate from let's encrypt by dns-01
  caa_issuers = ["letsencrypt.org"]
  # the public status page on the probe host in azure east us, outside the
  # platform's own nodes and region (docs/deploy/probe.md); that host's edge
  # caddy gets the certificate by http-01, which the caa issuer allows
  a_records = { status = "192.0.2.10" }
}

module "inventory" {
  source = "../../modules/inventory"

  environment = local.environment
  base_domain = var.base_domain
  # status. is the public status page on the probe host (docs/deploy/probe.md)
  hostnames            = merge(module.dns.hostnames, module.dns.a_hostnames)
  private_network_cidr = module.network.ip_range
  admin_cidrs          = var.admin_cidrs
  admin_user           = local.admin_user
  object_storage       = module.backup_storage.settings
  nodes = {
    for name, n in local.nodes : name => {
      tier        = n.tier
      index       = n.index
      provider    = "azure"
      location    = "${local.location}-${n.zone}"
      public_ipv4 = module.node[name].public_ipv4
      public_ipv6 = module.node[name].public_ipv6
      private_ip  = module.node[name].private_ip
    }
  }
}
