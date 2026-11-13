# renders the production root with mocked providers from tests/infra/sample.tfvars
# and checks the inventory contract in docs/deploy/opentofu.md section 1,
# the failover profiles, the dns records and the backup storage settings.
# run by make tofu-check.

variables {
  # fixture only: tofu test keeps state in memory, nothing is written
  state_passphrase = "fixture-passphrase-for-tofu-test-only"
}

# arm ids are paths; the mock's random strings would not look like one
mock_provider "azurerm" {
  mock_resource "azurerm_resource_group" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production" }
  }
  mock_resource "azurerm_subnet" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/virtualNetworks/production-vnet/subnets/zone" }
  }
  mock_resource "azurerm_user_assigned_identity" {
    defaults = {
      id           = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.ManagedIdentity/userAssignedIdentities/production-core"
      principal_id = "11111111-1111-1111-1111-111111111111"
    }
  }
  mock_resource "azurerm_virtual_network" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/virtualNetworks/production-vnet" }
  }
  mock_resource "azurerm_network_security_group" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/networkSecurityGroups/production-public" }
  }
  mock_resource "azurerm_public_ip" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/publicIPAddresses/node-v4" }
  }
  mock_resource "azurerm_network_interface" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/networkInterfaces/node-nic" }
  }
  mock_resource "azurerm_linux_virtual_machine" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Compute/virtualMachines/node" }
  }
  mock_resource "azurerm_storage_container" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Storage/storageAccounts/fixturebackups/blobServices/default/containers/pgbackrest" }
  }
  mock_resource "azurerm_traffic_manager_profile" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/trafficManagerProfiles/profile" }
  }
  mock_resource "azurerm_storage_account" {
    defaults = {
      id                    = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Storage/storageAccounts/fixturebackups"
      primary_blob_endpoint = "https://fixturebackups.blob.core.windows.net/"
    }
  }
}

mock_provider "cloudflare" {
  mock_data "cloudflare_zone" {
    defaults = {
      zone_id      = "023e105f4ecef8ad9ca31a8372d0c353"
      name_servers = ["ada.ns.cloudflare.com", "bob.ns.cloudflare.com"]
    }
  }
}

override_resource {
  target = module.node["production-edge-1"].azurerm_public_ip.v4
  values = {
    id         = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/publicIPAddresses/node-v4"
    ip_address = "203.0.113.11"
  }
}

override_resource {
  target = module.node["production-edge-2"].azurerm_public_ip.v4
  values = {
    id         = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/publicIPAddresses/node-v4"
    ip_address = "203.0.113.12"
  }
}

override_resource {
  target = module.node["production-edge-3"].azurerm_public_ip.v4
  values = {
    id         = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/publicIPAddresses/node-v4"
    ip_address = "203.0.113.13"
  }
}

override_resource {
  target = module.node["production-core-1"].azurerm_public_ip.v4
  values = {
    id         = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/publicIPAddresses/node-v4"
    ip_address = "203.0.113.21"
  }
}

override_resource {
  target = module.node["production-core-2"].azurerm_public_ip.v4
  values = {
    id         = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/publicIPAddresses/node-v4"
    ip_address = "203.0.113.22"
  }
}

run "inventory" {
  command = apply

  assert {
    condition     = keys(output.ansible_inventory.all.children) == ["core", "edge", "probe", "restore"]
    error_message = "groups must be exactly edge, core, probe and restore"
  }

  assert {
    condition     = keys(output.ansible_inventory.all.children.edge.hosts) == ["production-edge-1", "production-edge-2", "production-edge-3"]
    error_message = "edge must hold three hosts named <env>-edge-<n>"
  }

  assert {
    condition     = keys(output.ansible_inventory.all.children.core.hosts) == ["production-core-1", "production-core-2"]
    error_message = "core must hold two hosts named <env>-core-<n>"
  }

  # the probe runs outside azure (docs/deploy/probe.md), not from this state
  assert {
    condition     = length(output.ansible_inventory.all.children.probe.hosts) == 0
    error_message = "probe stays empty: the probe is not an azure node"
  }

  assert {
    condition = alltrue(flatten([
      for g in ["edge", "core"] : [
        for name, h in output.ansible_inventory.all.children[g].hosts :
        keys(h) == ["ansible_host", "ansible_user", "sa_index", "sa_location", "sa_private_ip", "sa_provider", "sa_public_ipv4", "sa_public_ipv6", "sa_tier"]
      ]
    ]))
    error_message = "host vars do not match the contract"
  }

  assert {
    condition = alltrue(flatten([
      for g in ["edge", "core"] : [
        for name, h in output.ansible_inventory.all.children[g].hosts :
        h.ansible_user == "ops" && h.sa_tier == g && h.sa_provider == "azure" && h.ansible_host == h.sa_public_ipv4 && can(cidrnetmask("${h.sa_public_ipv4}/32")) && h.sa_public_ipv6 == null
      ]
    ]))
    error_message = "ansible_host, user, tier, provider or address family wrong"
  }

  assert {
    condition     = [for h in values(output.ansible_inventory.all.children.edge.hosts) : h.sa_location] == ["swedencentral-1", "swedencentral-2", "swedencentral-3"]
    error_message = "edge nodes must sit in zones 1, 2 and 3"
  }

  assert {
    condition     = [for h in values(output.ansible_inventory.all.children.core.hosts) : h.sa_location] == ["swedencentral-1", "swedencentral-2"]
    error_message = "core nodes must sit in zones 1 and 2"
  }

  assert {
    condition = [
      for n in ["production-edge-1", "production-edge-2", "production-edge-3", "production-core-1", "production-core-2"] :
      merge(output.ansible_inventory.all.children.edge.hosts, output.ansible_inventory.all.children.core.hosts)[n].sa_private_ip
    ] == ["10.10.1.11", "10.10.2.12", "10.10.3.13", "10.10.1.21", "10.10.2.22"]
    error_message = "private addresses must follow the documented scheme"
  }

  assert {
    condition = jsonencode(output.ansible_inventory.all.vars) == jsonencode({
      sa_environment          = "production"
      sa_base_domain          = "example.org"
      sa_hostnames            = { app = "app.example.org", in = "in.example.org", otlp = "otlp.example.org", web = "web.example.org", status = "status.example.org" }
      sa_private_network_cidr = "10.10.0.0/16"
      sa_admin_cidrs          = ["192.0.2.0/24", "198.51.100.7/32", "2001:db8:ad::/48"]
      sa_object_storage       = { endpoint = "https://fixturebackups.blob.core.windows.net", region = "swedencentral", backup_bucket = "pgbackrest" }
    })
    error_message = "group vars do not match the contract"
  }

  assert {
    condition     = module.node["production-edge-1"].server_type == "Standard_B2als_v2" && module.node["production-core-1"].server_type == "Standard_B2as_v2"
    error_message = "the default vm sizes are the approved ones"
  }
}

# failover: in and otlp over the three edge addresses with 10 s probing,
# app and web over the two core addresses with 30 s probing, all on the
# port 80 health answer where 308 is healthy
run "failover" {
  command = apply

  assert {
    condition = { for k, e in module.failover.endpoints : k => e.target } == merge(
      { for l in ["in", "otlp"] : "${l}/0" => "203.0.113.11" },
      { for l in ["in", "otlp"] : "${l}/1" => "203.0.113.12" },
      { for l in ["in", "otlp"] : "${l}/2" => "203.0.113.13" },
      { for l in ["app", "web"] : "${l}/0" => "203.0.113.21" },
      { for l in ["app", "web"] : "${l}/1" => "203.0.113.22" },
    )
    error_message = "traffic manager endpoints do not carry the expected addresses"
  }

  assert {
    condition = alltrue([
      for l, m in module.failover.monitors :
      m.routing == "MultiValue" && m.protocol == "HTTP" && m.port == 80 && m.path == "/" && contains(m.codes, "308-308")
    ])
    error_message = "every profile must be multivalue and probe http port 80, accepting the 308"
  }

  assert {
    condition = (
      module.failover.monitors["in"].interval == 10 && module.failover.monitors["otlp"].interval == 10 &&
      module.failover.monitors["app"].interval == 30 && module.failover.monitors["web"].interval == 30
    )
    error_message = "ingestion profiles probe every 10 s, dashboard profiles every 30 s"
  }
}

# dns: one dns-only cname per hostname to its profile, plus caa
run "dns" {
  command = apply

  assert {
    condition = { for k, r in module.dns.records : k => r.value if r.type == "CNAME" } == {
      for l in ["app", "in", "otlp", "web"] : "${l}/CNAME" => module.failover.fqdns[l]
    }
    error_message = "every hostname must be a cname to its traffic manager profile"
  }

  assert {
    condition     = alltrue([for r in values(module.dns.records) : r.proxied == false])
    error_message = "no record may be proxied: the gateway needs the client address"
  }

  assert {
    condition     = jsonencode(sort([for r in values(module.dns.records) : r.value if r.type == "CAA"])) == jsonencode([";", "letsencrypt.org"])
    error_message = "caa must allow only let's encrypt and forbid wildcards"
  }
}

run "inventory_without_restore" {
  command = apply

  assert {
    condition     = length(output.ansible_inventory.all.children.restore.hosts) == 0
    error_message = "restore stays empty until restore_enabled"
  }
}

# the restore node of playbooks/restore.yml: core-sized, in
# the chosen zone, on the private network at .41
run "inventory_with_restore" {
  command = apply

  variables {
    restore_enabled = true
    restore_zone    = "3"
  }

  override_resource {
    target = module.node["production-restore-1"].azurerm_public_ip.v4
    values = {
      id         = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/veristar-production/providers/Microsoft.Network/publicIPAddresses/node-v4"
      ip_address = "203.0.113.41"
    }
  }

  assert {
    condition = jsonencode(output.ansible_inventory.all.children.restore.hosts["production-restore-1"]) == jsonencode({
      ansible_host   = "203.0.113.41"
      ansible_user   = "ops"
      sa_index       = 1
      sa_location    = "swedencentral-3"
      sa_private_ip  = "10.10.3.41"
      sa_provider    = "azure"
      sa_public_ipv4 = "203.0.113.41"
      sa_public_ipv6 = null
      sa_tier        = "restore"
    })
    error_message = "restore host vars do not match the contract"
  }

  assert {
    condition     = module.node["production-restore-1"].server_type == "Standard_B2as_v2"
    error_message = "the restore node must be sized like a core node"
  }

  assert {
    condition     = keys(output.ansible_inventory.all.children.core.hosts) == ["production-core-1", "production-core-2"]
    error_message = "the restore node must not join the core group"
  }
}

run "ssh_open_to_world_is_refused" {
  command = plan

  variables {
    admin_cidrs = ["0.0.0.0/0"]
  }

  expect_failures = [var.admin_cidrs]
}

run "short_passphrase_is_refused" {
  command = plan

  variables {
    state_passphrase = "short"
  }

  expect_failures = [var.state_passphrase]
}

run "floating_image_is_refused" {
  command = plan

  variables {
    server_image_version = "latest"
  }

  expect_failures = [var.server_image_version]
}
