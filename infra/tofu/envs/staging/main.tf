locals {
  environment = "staging"
  location    = "swedencentral"

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

  # three edge nodes in three zones, two core nodes in two, and
  # while restore_enabled the restore node of playbooks/restore.yml, sized
  # like a core node because it holds a full copy of the database
  nodes = merge({
    "${local.environment}-edge-1" = { tier = "edge", index = 1, zone = "1" }
    "${local.environment}-edge-2" = { tier = "edge", index = 2, zone = "2" }
    "${local.environment}-edge-3" = { tier = "edge", index = 3, zone = "3" }
    "${local.environment}-core-1" = { tier = "core", index = 1, zone = "1" }
    "${local.environment}-core-2" = { tier = "core", index = 2, zone = "2" }
    },
    var.restore_enabled ? {
      "${local.environment}-restore-1" = { tier = "restore", index = 1, zone = var.restore_zone }
    } : {},
  )

  # host part encodes the tier: .11+ edge, .21+ core, .41+ restore
  host_base = { edge = 10, core = 20, restore = 40 }

  # edge os disks hold the jetstream buffer, core os disks postgresql, its
  # wal and prometheus (docs/deploy/prerequisites.md, cost table)
  disk_gb = { edge = 32, core = 128, restore = 128 }

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
  tags                = local.tags
}

# the identity of the core and restore nodes: pgbackrest reaches the backup
# container with it (repo-azure-key-type=auto), so no storage key exists.
# edge nodes need no azure access and get no identity.
resource "azurerm_user_assigned_identity" "core" {
  name                = "${local.environment}-core"
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
  size                  = each.value.tier == "edge" ? var.edge_vm_size : var.core_vm_size
  image                 = local.image
  os_disk_gb            = local.disk_gb[each.value.tier]
  os_disk_type          = "StandardSSD_LRS"
  subnet_id             = module.network.subnets[each.value.zone].id
  private_ip            = cidrhost(module.network.subnets[each.value.zone].ip_range, local.host_base[each.value.tier] + each.value.index)
  identity_ids          = each.value.tier == "edge" ? [] : [azurerm_user_assigned_identity.core.id]
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
  writer_principal_ids  = { core = azurerm_user_assigned_identity.core.principal_id }
  protect               = local.protect
  tags                  = local.tags
}

locals {
  edge = [for name, n in local.nodes : module.node[name] if n.tier == "edge"]
  core = [for name, n in local.nodes : module.node[name] if n.tier == "core"]
}

module "failover" {
  source = "../../modules/failover"

  resource_group_name = azurerm_resource_group.this.name
  name_prefix         = "${replace(var.base_domain, ".", "-")}-${local.environment}"
  # ingestion (99.99 %) gets 10 s probing, the dashboards (99.90 %) 30 s
  profiles = {
    in   = { ipv4 = local.edge[*].public_ipv4, fast = true }
    otlp = { ipv4 = local.edge[*].public_ipv4, fast = true }
    app  = { ipv4 = local.core[*].public_ipv4, fast = false }
    web  = { ipv4 = local.core[*].public_ipv4, fast = false }
  }
  tags = local.tags
}

module "dns" {
  source = "../../modules/dns"

  base_domain = var.base_domain
  # the cname never changes; failover happens behind it in traffic manager,
  # whose answers carry their own ttl (module failover)
  ttl    = 300
  cnames = module.failover.fqdns
  # caddy gets every certificate from let's encrypt by dns-01
  caa_issuers = ["letsencrypt.org"]
}

module "inventory" {
  source = "../../modules/inventory"

  environment = local.environment
  base_domain = var.base_domain
  # status. is the public status page (docs/deploy/probe.md); its record is
  # added where the page is published
  hostnames            = merge(module.dns.hostnames, { status = "status.${var.base_domain}" })
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
