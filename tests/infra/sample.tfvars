# test fixture for tests/infra/*.tftest.hcl. every value is reserved for
# documentation (rfc 2606 domain, rfc 5737 and rfc 3849 ranges) so it can
# never be mistaken for a real input. real inputs go to
# infra/tofu/envs/<env>/terraform.tfvars once supplied.
base_domain           = "example.org"
admin_cidrs           = ["192.0.2.0/24", "2001:db8:ad::/48"]
admin_ssh_public_keys = ["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDocumentationOnlyKeyNotForUseAnywhere0 fixture"]
state_bucket          = "fixture-state"
state_location        = "fsn1"
backup_bucket         = "fixture-backups"
backup_location       = "hel1"
edge_server_type      = "fixture-edge-type"
core_server_type      = "fixture-core-type"
server_image          = "fixture-image"
