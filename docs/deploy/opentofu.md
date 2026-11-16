---
title: "OpenTofu: infrastructure, state and the Ansible inventory contract"
sidebarTitle: "OpenTofu"
description: "The OpenTofu infrastructure, its state, and the inventory it hands to Ansible."
---

OpenTofu creates every cloud resource of an environment: the Azure network, the VMs, Traffic
Manager, the backup storage account and the Cloudflare DNS records. It then writes the Ansible
inventory that the rest of the deployment reads. This page gives the procedure first, then the
inventory contract, the layout and the design choices behind it.

## Procedure

Staging is shown; production is the same with `production` (`CONFIRM=production` is needed only
for `production-destroy`). You need [prerequisites.md](prerequisites.md) done (including
`az login`), `sops` and `tofu` on `PATH`, and `infra/secrets/staging/tofu.enc.env` from
[secrets.md](secrets.md) section 5.

1. **Write `infra/tofu/envs/staging/terraform.tfvars`.** Copy `terraform.tfvars.example` from
   the same directory and fill in your values:

   ```hcl
   base_domain            = "<BASE_DOMAIN>"
   admin_cidrs            = ["<admin cidr>"]
   admin_ssh_public_keys  = ["<admin ssh public key>"]
   subscription_id        = "<subscription id>"
   state_resource_group   = "<state resource group>"
   state_storage_account  = "<globally unique, 3 to 24 lowercase letters and digits>"
   backup_storage_account = "<globally unique, 3 to 24 lowercase letters and digits>"
   server_image_version   = "0.20261111.2659"
   location               = "denmarkeast"
   ```

   `subscription_id`, `state_resource_group` and `state_storage_account` must each be on one
   plain `key = "value"` line, because the scripts read them with `sed`. Check that the account
   names are free with `az storage account check-name -n <name>`, and pick the image version as
   described in [prerequisites.md](prerequisites.md) ("Check the Azure subscription").

2. **Create the state storage** (once per environment):

   ```sh
   make state-bootstrap ENV=staging
   ```

   Expected: the created resources, then
   `ok: state storage <account> in <state resource group> (zrs, versioned, entra id only)`.

3. **Plan:**

   ```sh
   make staging-plan
   ```

   Expected on a new environment: a plan that only adds resources, `0 to change, 0 to destroy`.

4. **Apply:**

   ```sh
   make staging-apply
   ```

   Expected: `Apply complete!`, then the target writes
   `infra/ansible/inventories/staging/hosts.json`. Without a plan file it stops with
   `run make staging-plan first`.

5. **Check the inventory** against the contract below:

   ```sh
   python3 -m json.tool infra/ansible/inventories/staging/hosts.json | head -40
   ```

6. **Check SSH** to one node (cloud-init created `ops` with your admin keys):

   ```sh
   ssh ops@<ansible_host of staging-node-1> true && echo ok
   ```

7. **Check Traffic Manager** once Ansible has converged the nodes. Before the converge, port 80
   does not answer and every endpoint shows `Degraded`.

   ```sh
   az network traffic-manager endpoint list -g veristar-staging \
     --profile-name <profile name of in> --type externalEndpoints \
     --query "[].{name:name,target:target,status:endpointMonitorStatus}" -o table
   ```

   List the profile names with `az network traffic-manager profile list -g veristar-staging -o table`.
   Expected after the converge: every endpoint `Online`.

8. **Rewrite the inventory** later without changing anything: `make staging-inventory`.

If an apply stops on an Azure Resource Manager error after some resources were created (for
example a `404 ResourceNotFound` on the provider's read right after a create), resources can
exist in Azure without being in the state. `tofu import` cannot adopt them, because the root
module's `for_each` keys depend on values known only after apply. Delete them with `az` and run
the plan and apply again.

## Make targets

| Target | Does |
|---|---|
| `state-bootstrap ENV=staging` | creates the environment's state resource group, storage account and container with the az CLI; safe to rerun |
| `staging-plan` | `init`, then `plan -out=tfplan` (the plan file is encrypted) |
| `staging-apply` | applies `tfplan`, then writes the inventory |
| `staging-state-backup` | lists the earlier versions of the state blob; `STATE_ARGS="restore <version-id>"` puts one back |
| `staging-inventory` | rewrites `hosts.json` from the state |
| `staging-destroy` | `destroy`, keeping the backup storage ([teardown.md](teardown.md)) |
| `production-*` | the same; `production-destroy` also needs `CONFIRM=production` and fails while the delete locks exist |
| `tofu-check` | `fmt -check`, `init -backend=false`, `validate` and `tofu test` for both environments |

### Restoring an earlier state

Blob versioning on the state account keeps every earlier state as a version, still encrypted
with the state passphrase, and soft delete keeps a deleted blob for 35 days. To recover from an
overwritten or deleted state, with no `tofu` command running:

```sh
make staging-state-backup                   # lists version ids, current version last
make staging-state-backup STATE_ARGS="restore 2026-11-20T08:00:00.1234567Z"
make staging-plan                           # must show no unexpected changes
```

### Restore node

`restore_enabled = true` adds `<env>-restore-1` (tier `restore`, sized like a node, with the
node identity, private address `.41` in `restore_zone`) to the VMs and to the inventory group
`restore`, which `playbooks/restore.yml` targets
([../runbooks/point-in-time-restore.md](../runbooks/point-in-time-restore.md),
[../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md)). Set it back to `false`
after the restore.

### Locking the backup immutability policy

`backup_lock_state = "Locked"` makes the backup storage's immutability policy permanent. Set it
in production after the first week of verified backups ([backups.md](backups.md) section 1).

## Inventory contract (OpenTofu to Ansible)

This section is the agreement with the Ansible side. Change it only together.

### File

- `make staging-apply` and `make staging-inventory` write
  `infra/ansible/inventories/staging/hosts.json`; production writes
  `infra/ansible/inventories/production/hosts.json`.
- The file is the JSON value of the OpenTofu output `ansible_inventory`
  (`tofu output -json ansible_inventory`). It is generated, never edited, and gitignored.
- The format is Ansible's YAML inventory format written as JSON. The stock `yaml`
  inventory plugin reads `.json` files, so no custom plugin is needed. Point Ansible at
  the directory (`-i infra/ansible/inventories/staging/`) so `group_vars/` beside
  `hosts.json` is picked up. `group_vars/all.yml` is committed and holds what every Azure node
  shares (`sa_tls: cloudflare`, `sa_public_bind_ipv4`, see below).

### Shape

```json
{
  "all": {
    "vars": {
      "sa_environment": "staging",
      "sa_base_domain": "<BASE_DOMAIN>",
      "sa_hostnames": {
        "in": "in.<BASE_DOMAIN>", "otlp": "otlp.<BASE_DOMAIN>", "app": "app.<BASE_DOMAIN>",
        "web": "web.<BASE_DOMAIN>", "status": "status.<BASE_DOMAIN>"
      },
      "sa_private_network_cidr": "10.20.0.0/16",
      "sa_admin_cidrs": ["<admin cidr>", "..."],
      "sa_object_storage": {
        "endpoint": "https://<backup storage account>.blob.core.windows.net",
        "region": "denmarkeast",
        "backup_bucket": "pgbackrest"
      }
    },
    "children": {
      "edge": {
        "hosts": {
          "staging-node-1": { "...host vars, see below..." },
          "staging-node-2": {},
          "staging-node-3": {}
        }
      },
      "core": {
        "hosts": { "staging-node-1": {}, "staging-node-2": {}, "staging-node-3": {} }
      },
      "probe": { "hosts": {} },
      "restore": { "hosts": {} }
    }
  }
}
```

- Groups are always `edge` and `core`, which both list the same three converged nodes (tier
  `node`), `probe` and `restore` (0 or 1 host,
  present only while `restore_enabled`, see "Restore node" above). The group keys are always present. The
  probe runs as a Compose project on a separate virtual machine in Azure East US ([probe.md](probe.md)),
  outside this state, so OpenTofu leaves `probe` empty; the key stays so playbooks that name
  the group keep working. Its one record here is the DNS-only A record `status.` (`a_records`
  of module `dns`).
- Inventory host names equal the VM names: `<env>-<tier>-<index>`, index from 1.
  Every node runs a Patroni member; index 1 is the one that initialises the cluster.
- Values written as `<BASE_DOMAIN>` and `<admin cidr>` above are your values from
  `terraform.tfvars`; the file never contains anything else in their place.
- `sa_object_storage` keeps the shape it had for S3: `endpoint` is the storage account's blob
  endpoint, `backup_bucket` the container. The `backup` role derives the account name from the
  endpoint.

### Host vars (every host)

| Var | Type | Meaning |
|---|---|---|
| `ansible_host` | string | public IPv4, used for SSH |
| `ansible_user` | string | always `ops`, created by cloud-init with the admin SSH keys and passwordless sudo; root login is left to the `base` role to disable |
| `sa_tier` | string | `node` (edge and core roles on one node) or `restore`; the roles also accept `edge` and `core` for nodes that run one tier, as the local converge's default topology does |
| `sa_index` | number | 1-based index inside the tier |
| `sa_provider` | string | `azure` |
| `sa_location` | string | region and availability zone, `denmarkeast-1` to `denmarkeast-3` |
| `sa_public_ipv4` | string | same as `ansible_host` |
| `sa_public_ipv6` | null | the nodes are IPv4 only |
| `sa_private_ip` | string | address in the virtual network |

Peers are derived by Ansible from the groups (for example NATS routes and etcd members
from `groups['edge'] | map('extract', hostvars, 'sa_private_ip')`). OpenTofu does not
emit peer lists, so there is one source of truth.

### Things the Ansible and Caddy side can rely on

- Azure maps each public IPv4 address onto the VM's one network interface (1:1 NAT): the VM
  holds only its private address. Public ports are therefore published on the private address
  (`sa_public_bind_ipv4` in `group_vars/all.yml`), and the `base` role accepts that the
  public and private interface are the same on Azure. The client's source address is kept.
- The network security group, on every subnet: TCP 80, TCP 443, UDP 443 (HTTP/3) and ICMP from
  the internet; TCP 22 only from `admin_cidrs`; everything else from the internet is
  denied. Azure's default rules allow traffic inside the virtual network; the host firewall of
  the `base` role and the internal mTLS cover it, as on every private link.
- `public_ingress_open = false` in `terraform.tfvars` limits TCP 80, TCP 443 and UDP 443 to
  `admin_cidrs` as well, without stopping anything. Traffic Manager's health probes are refused
  then, so every endpoint shows as degraded and the names still resolve to all nodes. Set it back
  to `true` (the default) and apply to serve the internet again.
- Traffic Manager probes every node over HTTP on port 80, path `/`, and counts 200 to 299 and
  308 as healthy (it does not follow redirects). The Host header it sends is the endpoint's
  address, so the port 80 health answer must not depend on the Host header (it does not).
- Cloud-init only creates `ops` and installs SSH keys. Changes to `custom_data`, the admin keys
  and the image version are ignored after creation, so key rotation through Ansible never
  replaces a VM.
- Every node and the restore node carry the user-assigned managed identity `<env>-node`,
  which holds Storage Blob Data Contributor on the backup container and nothing else.
- Traffic Manager asks `/` for `in` and `otlp` (the node's ingestion health) and `/core` for
  `app` and `web` (its dashboard health); both answer 308 when healthy
  (`deploy/caddy/assemble.sh`, tier `node`).

### Secrets handed to OpenTofu

Azure is reached through the operator's `az login` (the azurerm provider and backend use the
Azure CLI credential), so no Azure secret is stored. The make targets load the rest with
`sops exec-env infra/secrets/<env>/tofu.enc.env '<tofu command>'`. That SOPS dotenv file must be
covered by the `creation_rules` in `infra/secrets/.sops.yaml` and holds:

| Variable | Used by | Source |
|---|---|---|
| `CLOUDFLARE_API_TOKEN` | cloudflare provider: zone lookup and records (`Zone:Zone:Read`, `Zone:DNS:Edit` on the zone only) | your Cloudflare API token |
| `TF_VAR_state_passphrase` | state and plan encryption, at least 16 characters, generated, never reused across environments | generated, encrypted to the environment's age recipients |

Non-secret values live in `infra/tofu/envs/<env>/terraform.tfvars`, which is gitignored
because it holds the operator's own addresses, keys and account names. Copy it from
`terraform.tfvars.example` in the same directory. `tests/infra/sample.tfvars` shows every
variable with documentation-only values for the tests.

## Layout

```
infra/tofu/
  modules/network          virtual network, one /24 subnet per zone, network security group
  modules/node             static public IPv4, network interface, Debian 13 VM, cloud-init, delete locks
  modules/failover         Traffic Manager profiles (MultiValue) with the nodes as external endpoints
  modules/dns              Cloudflare records in the existing zone: CNAMEs to the profiles, CAA
  modules/backup-storage   storage account (ZRS, versioning, immutability), container, lifecycle, role
  modules/inventory        pure function: node data in, inventory object out
  envs/staging             root module, azurerm backend, state encryption
  envs/production          same modules, production network range and delete locks
tests/infra/               tofu test files and the sample tfvars
```

| Env | Resource group | Region (`location`) | Virtual network | Nodes | Protection |
|---|---|---|---|---|---|
| staging | `veristar-staging` | from `terraform.tfvars` | 10.20.0.0/16 | zones 1, 2, 3 | off |
| production | `veristar-production` | `denmarkeast` | 10.10.0.0/16 | zones 1, 2, 3 | `CanNotDelete` locks on VMs, public addresses and the backup account |

Subnets: `10.x.1.0/24` zone 1, `10.x.2.0/24` zone 2, `10.x.3.0/24` zone 3. Node addresses in
their zone's subnet are `.11` to `.13` for nodes 1 to 3 and `.41` for the restore node
(production: node-1 `10.10.1.11`, node-2 `10.10.2.12`, node-3 `10.10.3.13`). Each subnet has the
`Microsoft.Storage` service endpoint and no default outbound access (every node has its own
public address).

| Node | Size | OS disk | Image |
|---|---|---|---|
| node (3), restore (0 or 1) | `Standard_B2as_v2` (`node_vm_size`), 2 vCPU, 8 GB | Standard SSD, 128 GiB | `Debian` `debian-13` `13-gen2`, version pinned by `server_image_version` |

### Why `Standard_B2as_v2`

 The compose files set no container memory limits, so the size rests
on fixed allocations and measured use. Fixed: PostgreSQL `shared_buffers` 2 GB and
`effective_cache_size` 4 GB (`roles/core/defaults/main.yml`), the NATS memory store capped at
512 MiB (`edge_nats_max_memory_store`), Prometheus with 30 days of retention. Measured on the
local converge with the three-node layout ([local-converge.md](local-converge.md), 2026-11-13, `docker stats` after the first
converge, idle, one Umami process): 1,012 MiB on the Patroni leader's node and 941 MiB on a
replica's node over 21 containers; the largest were Grafana 275 to 299 MiB, PostgreSQL 127 to
190 MiB, Umami 109 to 149 MiB (about 115 MB per further process), Prometheus 74 to
79 MiB. With `shared_buffers` filled, the NATS store at its cap and the second Umami process,
a node needs about 4 GB before the page cache, which rules out the 4 GB sizes and leaves room
for the cache in 8 GB. `Standard_B2as_v2` is the cheapest B-series size with 8 GB
(`Standard_B4als_v2` has the same memory with 4 vCPUs at 0.138 USD an hour against 0.0778, both
Sweden Central list prices, since the price list publishes none for these sizes in Denmark
East on 2026-11-13).

## Design choices

### Versions

| Item | Version | Notes |
|---|---|---|
| OpenTofu | 1.13.9 | releases come with `tofu_<version>_SHA256SUMS`; check the archive against it before unpacking |
| `hashicorp/azurerm` | 5.13.0 | the newest release at least 7 days old on 2026-11-13 |
| `cloudflare/cloudflare` | 5.28.1 | the newest release at least 7 days old on 2026-11-13 |

Provider settings that shape the modules:

- The azurerm provider runs with `resource_provider_registrations = "none"`, so OpenTofu needs
  no subscription-wide registration rights, and with `storage_use_azuread = true`, because the
  storage accounts accept no account keys.
- `azurerm_storage_account` sets version-level immutability through `immutability_policy`,
  which needs versioning and is set when the account is created.
- `azurerm_traffic_manager_profile` uses `traffic_routing_method = "MultiValue"`, `max_return`
  and `monitor_config.expected_status_code_ranges`.
- The Cloudflare zone is looked up with `data "cloudflare_zone"` and is never created or changed
  by OpenTofu: it belongs to the registrar account. CAA values go in
  `data = { flags, tag, value }`.

### Health-checked failover with Traffic Manager

Every node keeps its own public address, and DNS answers with the healthy ones. Three options
were compared on 2026-11-13:

| | Azure Traffic Manager (chosen) | Azure Load Balancer + DNS | Cloudflare Load Balancing |
|---|---|---|---|
| How it fails over | DNS: an endpoint whose probe fails leaves the answer | one regional, zone-redundant frontend address; unhealthy backends stop receiving new flows | DNS-only mode (no proxy, so the gateway keeps the client address): unhealthy origins leave the answer |
| Probe interval | 30 s, or 10 s with fast probing | 5 s and up | 60 s or 15 s minimum depending on the plan, 10 s on Enterprise |
| Accepts the 308 health answer | yes, `expected_status_code_ranges` | HTTP probes count only 200; a TCP probe would miss the composite health | configurable expected codes |
| Answer | MultiValue: all healthy addresses in every answer, so a client retries another without a new lookup | one address | one or more addresses depending on steering |
| Price (2026-11-13) | 0.54 USD per external endpoint a month, 2.00 more with fast probing, 0.54 per million queries | 18.25 USD a month for the first five rules, 0.01 per hour for each further rule, plus data processed | not published as a price list |

Traffic Manager costs about 18.48 USD a month for the twelve endpoints (four names over three
nodes), with 10 s probing on the two ingestion names and 30 s on the dashboard names. Microsoft's
service level agreement for Traffic Manager states 99.99 %. Cloudflare points each hostname at its
profile with a DNS-only CNAME (`<domain>-<env>-<label>.trafficmanager.net`).

Failover time per name: an endpoint leaves the answer after three failed probes in a row
(`tolerated_number_of_failures = 2`). For `in` and `otlp` that is 3 x 10 s, plus the 5 s timeout,
plus up to 10 s of cached answers. For `app` and `web` it is 3 x 30 s, plus the 10 s timeout,
plus up to 30 s. When every endpoint of a profile fails, Traffic Manager answers with all of them
rather than none. The measured times on production are in
[../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md).

CAA: `in.<domain>` and the other names are CNAMEs to `trafficmanager.net`. A certificate
authority looks up CAA at the name, follows the CNAME, finds none at the profile name, and
continues at the parent of the original name, `<domain>`, whose CAA allows `letsencrypt.org`
only (RFC 8659). DNS-01 writes `_acme-challenge.<name>` TXT records, which are separate names and
unaffected by the CNAMEs. Cloudflare publishes its own CAA records beside the two of this module
for its Universal SSL issuers, so `dig CAA <BASE_DOMAIN>` also lists other issuers.

### State backend

The state of each environment is one blob, `<env>/tofu.tfstate`, in container `tfstate` of a
storage account that `make state-bootstrap` creates outside the environment's resource group
(zone-redundant, account keys off, blob versioning, 35 days of soft delete), so a destroy never
removes the state it runs on. The backend signs in with Entra ID (`use_azuread_auth = true`,
from the az CLI login) and locks the state with a blob lease. The signed-in user needs Storage
Blob Data Contributor on the state account, which the bootstrap script grants.

### Tests

`make tofu-check` runs `tofu fmt -recursive -check`, `tofu init -backend=false`, `tofu validate`
and `tofu test` for both environments. The tests (`tests/infra`, mocked providers,
`command = apply`) render both roots from `tests/infra/sample.tfvars` and check the groups, host
names, every host var, zones, the private address scheme, the group vars, the default VM size,
the Traffic Manager endpoints (the three node addresses for every name, `/` for `in` and `otlp`,
`/core` for `app` and `web`), MultiValue routing, the HTTP probe on port 80 accepting 308, 10 s
probing for ingestion and 30 s for dashboards, one DNS-only CNAME per hostname, CAA
(`issue letsencrypt.org`, `issuewild ;`), the restore node, and that `0.0.0.0/0` for SSH, a short
passphrase and a floating image version are refused.
