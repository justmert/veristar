# test fixture for tests/infra/*.tftest.hcl. every value is reserved for
# documentation (rfc 2606 domain, rfc 5737 and rfc 3849 ranges, the all-zero
# guid) so it can never be mistaken for a real input. the ssh key is a
# real ed25519 public key (azure decodes it) whose private half was
# discarded when it was made. real inputs go to
# infra/tofu/envs/<env>/terraform.tfvars once supplied.
base_domain            = "example.org"
admin_cidrs            = ["192.0.2.0/24", "198.51.100.7/32", "2001:db8:ad::/48"]
admin_ssh_public_keys  = ["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIClgOUZeOGVIOwaYJYyBuzERYrzstkS/+N/r/wQLJo7Q fixture-private-key-discarded"]
subscription_id        = "00000000-0000-0000-0000-000000000000"
state_resource_group   = "fixture-state"
state_storage_account  = "fixturestate"
backup_storage_account = "fixturebackups"
server_image_version   = "0.20261111.2659"
location               = "swedencentral"
