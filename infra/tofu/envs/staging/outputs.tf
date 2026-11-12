output "ansible_inventory" {
  description = "written to infra/ansible/inventories/<env>/hosts.json by make"
  value       = module.inventory.inventory
}

output "nameservers" {
  description = "delegate the zone to these at the registrar"
  value       = module.dns.nameservers
}
