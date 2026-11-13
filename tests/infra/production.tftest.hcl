# renders the production root with mocked providers from tests/infra/sample.tfvars
# and checks the inventory contract in docs/deploy/opentofu.md section 1.
# run by make tofu-check.

variables {
  # fixture only: tofu test keeps state in memory, nothing is written
  state_passphrase = "fixture-passphrase-for-tofu-test-only"
}

# hetzner ids are numeric strings; the mock's random strings would fail the
# number-typed arguments that reference them
mock_provider "hcloud" {
  mock_resource "hcloud_firewall" {
    defaults = { id = "1001" }
  }
  mock_resource "hcloud_primary_ip" {
    defaults = { id = "2001" }
  }
  mock_resource "hcloud_ssh_key" {
    defaults = { id = "3001" }
  }
  mock_resource "hcloud_network" {
    defaults = { id = "4001" }
  }
}
mock_provider "bunnynet" {}
mock_provider "scaleway" {}

override_resource {
  target = module.node["production-edge-1"].hcloud_server.this
  values = {
    ipv4_address = "203.0.113.11"
    ipv6_address = "2001:db8:11::1"
  }
}

override_resource {
  target = module.node["production-edge-2"].hcloud_server.this
  values = {
    ipv4_address = "203.0.113.12"
    ipv6_address = "2001:db8:12::1"
  }
}

override_resource {
  target = module.node["production-edge-3"].hcloud_server.this
  values = {
    ipv4_address = "203.0.113.13"
    ipv6_address = "2001:db8:13::1"
  }
}

override_resource {
  target = module.node["production-core-1"].hcloud_server.this
  values = {
    ipv4_address = "203.0.113.21"
    ipv6_address = "2001:db8:21::1"
  }
}

override_resource {
  target = module.node["production-core-2"].hcloud_server.this
  values = {
    ipv4_address = "203.0.113.22"
    ipv6_address = "2001:db8:22::1"
  }
}

override_resource {
  target = module.probe[0].scaleway_instance_ip.v4
  values = {
    address = "198.51.100.50"
  }
}


run "inventory_without_probe" {
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

  assert {
    condition     = length(output.ansible_inventory.all.children.probe.hosts) == 0
    error_message = "probe stays empty until probe_enabled"
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
        h.ansible_user == "ops" && h.sa_tier == g && h.sa_provider == "hetzner" && h.ansible_host == h.sa_public_ipv4 && can(cidrnetmask("${h.sa_public_ipv4}/32"))
      ]
    ]))
    error_message = "ansible_host, user, tier or provider wrong"
  }

  assert {
    condition     = [for h in values(output.ansible_inventory.all.children.edge.hosts) : h.sa_location] == ["fsn1", "nbg1", "hel1"]
    error_message = "edge nodes must sit in fsn1, nbg1 and hel1"
  }

  assert {
    condition     = [for h in values(output.ansible_inventory.all.children.core.hosts) : h.sa_location] == ["fsn1", "nbg1"]
    error_message = "core nodes must sit in fsn1 and nbg1"
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
      sa_hostnames            = { in = "in.example.org", otlp = "otlp.example.org", app = "app.example.org", web = "web.example.org", status = "status.example.org" }
      sa_private_network_cidr = "10.10.0.0/16"
      sa_admin_cidrs          = ["192.0.2.0/24", "2001:db8:ad::/48"]
      sa_object_storage       = { endpoint = "https://hel1.your-objectstorage.com", region = "hel1", backup_bucket = "fixture-backups" }
    })
    error_message = "group vars do not match the contract"
  }

  # dns: in and otlp carry the three edge addresses, app and web the two
  # core addresses, each per family and http monitored; plus caa
  assert {
    condition = { for k, r in module.dns.records : k => r.value if r.type != "CAA" } == merge(
      { for l in ["in", "otlp"] : "${l}/A/0" => "203.0.113.11" },
      { for l in ["in", "otlp"] : "${l}/A/1" => "203.0.113.12" },
      { for l in ["in", "otlp"] : "${l}/A/2" => "203.0.113.13" },
      { for l in ["in", "otlp"] : "${l}/AAAA/0" => "2001:db8:11::1" },
      { for l in ["in", "otlp"] : "${l}/AAAA/1" => "2001:db8:12::1" },
      { for l in ["in", "otlp"] : "${l}/AAAA/2" => "2001:db8:13::1" },
      { for l in ["app", "web"] : "${l}/A/0" => "203.0.113.21" },
      { for l in ["app", "web"] : "${l}/A/1" => "203.0.113.22" },
      { for l in ["app", "web"] : "${l}/AAAA/0" => "2001:db8:21::1" },
      { for l in ["app", "web"] : "${l}/AAAA/1" => "2001:db8:22::1" },
    )
    error_message = "dns record sets do not carry the expected addresses"
  }

  assert {
    condition     = alltrue([for r in values(module.dns.records) : r.monitor_type == "Http" if r.type != "CAA"])
    error_message = "every address record must be http monitored"
  }

  assert {
    condition     = jsonencode(sort([for r in values(module.dns.records) : r.value if r.type == "CAA"])) == jsonencode([";", "letsencrypt.org"])
    error_message = "caa must allow only let's encrypt and forbid wildcards"
  }
}

run "inventory_with_probe" {
  command = apply

  variables {
    probe_enabled       = true
    probe_instance_type = "fixture-probe-type"
    probe_image         = "fixture-probe-image"
  }

  assert {
    condition     = keys(output.ansible_inventory.all.children.probe.hosts) == ["production-probe-1"]
    error_message = "probe host missing"
  }

  assert {
    condition = jsonencode(output.ansible_inventory.all.children.probe.hosts["production-probe-1"]) == jsonencode({
      ansible_host   = "198.51.100.50"
      ansible_user   = "ops"
      sa_index       = 1
      sa_location    = "fr-par-1"
      sa_private_ip  = null
      sa_provider    = "scaleway"
      sa_public_ipv4 = "198.51.100.50"
      sa_public_ipv6 = null
      sa_tier        = "probe"
    })
    error_message = "probe host vars do not match the contract"
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
# the chosen location, on the private network at .41
run "inventory_with_restore" {
  command = apply

  variables {
    restore_enabled  = true
    restore_location = "hel1"
  }

  override_resource {
    target = module.node["production-restore-1"].hcloud_server.this
    values = {
      ipv4_address = "203.0.113.41"
      ipv6_address = "2001:db8:41::1"
    }
  }

  assert {
    condition = jsonencode(output.ansible_inventory.all.children.restore.hosts["production-restore-1"]) == jsonencode({
      ansible_host   = "203.0.113.41"
      ansible_user   = "ops"
      sa_index       = 1
      sa_location    = "hel1"
      sa_private_ip  = "10.10.3.41"
      sa_provider    = "hetzner"
      sa_public_ipv4 = "203.0.113.41"
      sa_public_ipv6 = "2001:db8:41::1"
      sa_tier        = "restore"
    })
    error_message = "restore host vars do not match the contract"
  }

  assert {
    condition     = module.node["production-restore-1"].server_type == "fixture-core-type"
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
