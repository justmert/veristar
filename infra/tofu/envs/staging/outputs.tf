output "ansible_inventory" {
  description = "written to infra/ansible/inventories/<env>/hosts.json by make"
  value       = module.inventory.inventory
}

output "nameservers" {
  description = "cloudflare's nameservers for the zone (set by cloudflare registrar)"
  value       = module.dns.nameservers
}

output "traffic_manager" {
  description = "hostname label to the traffic manager name its cname points at"
  value       = module.failover.fqdns
}
