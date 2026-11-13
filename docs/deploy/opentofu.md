---
title: "OpenTofu: infrastructure, state and the Ansible inventory contract"
sidebarTitle: "OpenTofu"
description: "The OpenTofu infrastructure, its state, and the inventory it hands to Ansible."
---

Status: Azure version (2026-11-13). Everything here was validated locally with `tofu fmt`,
`tofu validate` and `tofu test` against mocked providers, and `tofu plan` ran against the
owner's Azure subscription and the Cloudflare zone of `veristar.app` (section 3.6). Nothing has
been applied yet. Section 5 lists what still needs an operator decision or input.

## 1. Inventory contract (OpenTofu to Ansible)

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
      "sa_base_domain": "<I4>",
      "sa_hostnames": {
        "in": "in.<I4>", "otlp": "otlp.<I4>", "app": "app.<I4>",
        "web": "web.<I4>", "status": "status.<I4>"
      },
      "sa_private_network_cidr": "10.20.0.0/16",
      "sa_admin_cidrs": ["<I13>", "..."],
      "sa_object_storage": {
        "endpoint": "https://<backup storage account>.blob.core.windows.net",
        "region": "swedencentral",
        "backup_bucket": "pgbackrest"
      }
    },
    "children": {
      "edge": {
        "hosts": {
          "staging-edge-1": { "...host vars, see below..." },
          "staging-edge-2": {},
          "staging-edge-3": {}
        }
      },
      "core": {
        "hosts": { "staging-core-1": {}, "staging-core-2": {} }
      },
      "probe": { "hosts": {} },
      "restore": { "hosts": {} }
    }
  }
}
```

- Groups are always `edge` (3 hosts), `core` (2 hosts), `probe` and `restore` (0 or 1 host,
  present only while `restore_enabled`, section 4). The group keys are always present. The
  probe runs outside Azure as a container ([probe.md](probe.md)), so OpenTofu leaves `probe`
  empty; the key stays so playbooks that name the group keep working.
- Inventory host names equal the VM names: `<env>-<tier>-<index>`, index from 1.
  Index 1 of `core` is the node that runs PostgreSQL first.
- Values written as `<I4>` and `<I13>` above are the real inputs once supplied; the
  file never contains anything else in their place.
- `sa_object_storage` keeps the shape it had for S3: `endpoint` is the storage account's blob
  endpoint, `backup_bucket` the container. The `backup` role derives the account name from the
  endpoint.

### Host vars (every host)

| Var | Type | Meaning |
|---|---|---|
| `ansible_host` | string | public IPv4, used for SSH |
| `ansible_user` | string | always `ops`, created by cloud-init with the admin SSH keys and passwordless sudo; root login is left to the `base` role to disable |
| `sa_tier` | string | `edge`, `core` or `restore` |
| `sa_index` | number | 1-based index inside the tier |
| `sa_provider` | string | `azure` |
| `sa_location` | string | region and availability zone, `swedencentral-1` to `swedencentral-3` |
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
  the internet; TCP 22 only from the admin CIDRs; everything else from the internet is
  denied. Azure's default rules allow traffic inside the virtual network; the host firewall of
  the `base` role and the internal mTLS cover it, as on every private link.
- Traffic Manager probes every node over HTTP on port 80, path `/`, and counts 200 to 299 and
  308 as healthy (it does not follow redirects). The Host header it sends is the endpoint's
  address, so the port 80 health answer must not depend on the Host header (it does not).
- Cloud-init only creates `ops` and installs SSH keys. Changes to `custom_data`, the admin keys
  and the image version are ignored after creation, so key rotation through Ansible never
  replaces a VM.
- The core and restore nodes carry the user-assigned managed identity `<env>-core`, which holds
  Storage Blob Data Contributor on the backup container and nothing else. Edge nodes have no
  identity.

### Secrets handed to OpenTofu

Azure is reached through the operator's `az login` (the azurerm provider and backend use the
Azure CLI credential), so no Azure secret is stored. The make targets load the rest with
`sops exec-env infra/secrets/<env>/tofu.enc.env '<tofu command>'`. That SOPS dotenv file must be
covered by the `creation_rules` in `infra/secrets/.sops.yaml` and holds:

| Variable | Used by | Input |
|---|---|---|
| `CLOUDFLARE_API_TOKEN` | cloudflare provider: zone lookup and records (`Zone:Zone:Read`, `Zone:DNS:Edit` on the zone only) | I6 |
| `TF_VAR_state_passphrase` | state and plan encryption, at least 16 characters, generated, never reused across environments | generated, encrypted to I14 |

Non-secret inputs live in `infra/tofu/envs/<env>/terraform.tfvars` (committed once the
inputs exist). `tests/infra/sample.tfvars` shows every variable with documentation-only
values; it is a test fixture, not a template with real values.

## 2. Layout

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

| Env | Resource group | Virtual network | Edge | Core | Protection |
|---|---|---|---|---|---|
| staging | `veristar-staging` | 10.20.0.0/16 | zones 1, 2, 3 | zones 1, 2 | off (rebuilt in the drill) |
| production | `veristar-production` | 10.10.0.0/16 | zones 1, 2, 3 | zones 1, 2 | `CanNotDelete` locks on VMs, public addresses and the backup account |

Subnets: `10.x.1.0/24` zone 1, `10.x.2.0/24` zone 2, `10.x.3.0/24` zone 3. Node addresses in
their zone's subnet are `.11` to `.13` for edge nodes 1 to 3, `.21`, `.22` for core nodes 1 and
2 and `.41` for the restore node (staging: edge-1 `10.20.1.11`, edge-2 `10.20.2.12`, edge-3
`10.20.3.13`, core-1 `10.20.1.21`, core-2 `10.20.2.22`), so a private address tells the tier and
zone. Each subnet has the `Microsoft.Storage` service endpoint and no default outbound access
(every node has its own public address).

| Node | Size | OS disk | Image |
|---|---|---|---|
| edge (3) | `Standard_B2als_v2`, 2 vCPU, 4 GB | Standard SSD, 32 GiB | `Debian` `debian-13` `13-gen2`, version pinned by `server_image_version` |
| core (2), restore (0 or 1) | `Standard_B2as_v2`, 2 vCPU, 8 GB | Standard SSD, 128 GiB | same |

## 3. Research findings (pinned versions, read 2026-11-13)

### 3.1 OpenTofu 1.13.9

Unchanged pin. Releases come with `tofu_<version>_SHA256SUMS`; on 2026-11-13
`tofu_1.13.9_darwin_arm64.zip` matched its line in that file before it was unpacked.

### 3.2 `hashicorp/azurerm` 5.13.0

Newest release on registry.opentofu.org on 2026-11-13 (published 2026-11-05). azurerm cuts a
minor release every week, so the pin follows the plan's rule for fast-moving packages: the
newest release at least 7 days old. Arguments were checked against the provider's schema
(`tofu providers schema -json`) and by `tofu validate` and `tofu plan`. Points that shaped the
modules:

- `azurerm_subnet` takes `service_endpoint` blocks and `default_outbound_access_enabled`.
- The provider is configured with `resource_provider_registrations = "none"`, so OpenTofu needs
  no subscription-wide registration rights, and `storage_use_azuread = true`, because the
  storage accounts accept no account keys.
- `azurerm_storage_account` sets account-level version-level immutability through
  `immutability_policy` (`period_since_creation_in_days`, `state`), which needs versioning and is
  set when the account is created.
- `azurerm_traffic_manager_profile` supports `traffic_routing_method = "MultiValue"`,
  `max_return` and `monitor_config.expected_status_code_ranges`.

### 3.3 `cloudflare/cloudflare` 5.28.1

Newest release at least 7 days old on 2026-11-13 (5.29.0 was 2 days old). `cloudflare_dns_record`
takes the full record name; CAA values go in `data = { flags, tag, value }`. The zone is looked
up with `data "cloudflare_zone"` and `filter = { name = ... }`, and is never created or changed
by OpenTofu: it belongs to the registrar account.

### 3.4 Health-checked failover: Traffic Manager, compared with the alternatives

The edge and core nodes each keep their own public address, as before; what was needed is DNS
that answers with the healthy ones. Three options were compared on 2026-11-13:

| | Azure Traffic Manager (chosen) | Azure Load Balancer + DNS | Cloudflare Load Balancing |
|---|---|---|---|
| How it fails over | DNS: an endpoint whose probe fails leaves the answer | one regional, zone-redundant frontend address; unhealthy backends stop receiving new flows | DNS-only mode (no proxy, so the gateway keeps the client address): unhealthy origins leave the answer |
| Probe interval | 30 s, or 10 s with fast probing | 5 s and up | 60 s or 15 s minimum depending on the plan, 10 s on Enterprise (Cloudflare limits page) |
| Accepts the 308 health answer | yes, `expected_status_code_ranges` | HTTP probes count only 200; a TCP probe would miss the composite health | configurable expected codes |
| Answer | MultiValue: all healthy addresses in every answer, so a client retries another without a new lookup | one address | one or more addresses depending on steering |
| Price (2026-11-13) | 0.54 USD per external endpoint a month, 2.00 more with fast probing, 0.54 per million queries | 18.25 USD a month (0.025 per hour) for the first five rules, 0.01 per hour for each further rule, plus data processed; edge and core need separate rule sets for TCP 80, TCP 443 and UDP 443 | shown in the dashboard only; no public price list to compare against |

Traffic Manager meets the need at the lowest published price: about 17.40 USD a month for the
ten endpoints, with 10 s probing on the two ingestion names and 30 s on the dashboard names. It
keeps the shape the design was built on (three independent edge addresses, any of which serves
a client) and Microsoft's service level agreement for Traffic Manager states 99.99 %. Cloudflare points each
hostname at its profile with a DNS-only CNAME (`<domain>-<env>-<label>.trafficmanager.net`).

Failover time per name (Traffic Manager monitoring docs): an endpoint leaves after three failed
probes in a row (`tolerated_number_of_failures = 2`). For `in` and `otlp`: 3 x 10 s, plus the 5 s
timeout, plus up to 10 s of cached answers. For `app` and `web`: 3 x 30 s, plus the 10 s timeout,
plus up to 30 s. When every endpoint of a profile fails, Traffic Manager answers with all of them
rather than none.

CAA: `in.<domain>` and the others are CNAMEs to `trafficmanager.net`. A CA looks up CAA at the
name, follows the CNAME, finds none at the profile name, and continues at the parent of the
original name, `<domain>`, whose CAA allows `letsencrypt.org` only (RFC 8659). DNS-01 writes
`_acme-challenge.<name>` TXT records, which are separate names and unaffected by the CNAMEs.

### 3.5 State backend: azurerm

The state of each environment is one blob, `<env>/tofu.tfstate`, in container `tfstate` of a
storage account that `make state-bootstrap` creates outside the environment's resource group
(zone-redundant, account keys off, blob versioning, 35 days of soft delete). The backend signs in
with Entra ID (`use_azuread_auth = true`, from the az CLI login) and locks the state with a blob
lease. The signed-in user needs Storage Blob Data Contributor on the state account, which the
bootstrap script grants.

### 3.6 Plan against the real subscription (2026-11-13)

Run on an operator workstation (the only machine with the az login): the configuration was copied to a
scratch directory, OpenTofu 1.13.9 installed there after the checksum check, and a local backend
override used in place of the not yet existing state account. Inputs: `base_domain =
"veristar.app"`, a documentation-only admin range (`192.0.2.0/24`), a throwaway SSH key,
`server_image_version = "0.20261104.2652"`, backup accounts `veristarprodbackups` and
`veristarstgbackups` (both reported available by `az storage account check-name`). The
Cloudflare provider used the zone-scoped token of an operator input read-only; the zone lookup returned
the `veristar.app` zone, which held no records.

| Environment | Result |
|---|---|
| production | `Plan: 66 to add, 0 to change, 0 to destroy.` |
| staging | `Plan: 55 to add, 0 to change, 0 to destroy.` (no delete locks) |

Production, by kind: 1 resource group, 1 managed identity, 1 virtual network, 3 subnets with 3
NSG associations, 1 NSG with 6 rules, 5 public IPv4 addresses, 5 network interfaces, 5 VMs,
10 delete locks on VMs and addresses, 1 storage account with 1 container, 1 lifecycle policy,
1 role assignment and 1 delete lock, 4 Traffic Manager profiles with 10 endpoints, and in
Cloudflare 4 CNAMEs and 2 CAA records. Nothing was applied.

### 3.7 Local validation (2026-11-13)

- `tofu fmt -recursive -check`, `tofu init -backend=false` and `tofu validate` pass for both
  envs with the pinned providers.
- `tofu test` (tests/infra, mocked providers, `command = apply`) renders both roots from
  `tests/infra/sample.tfvars` and checks: the groups, host names, every host var, zones, the
  private address scheme, the group vars, the default VM sizes, the Traffic Manager endpoints
  (three edge addresses for `in` and `otlp`, two core for `app` and `web`), MultiValue routing,
  the HTTP probe on port 80 accepting 308, 10 s probing for ingestion and 30 s for dashboards,
  one DNS-only CNAME per hostname to its profile, CAA (`issue letsencrypt.org`, `issuewild ;`),
  the restore node, and that `0.0.0.0/0` for SSH, a short passphrase and a floating image
  version are refused. 8 of 8 runs pass per env.

## 4. Make targets

| Target | Does |
|---|---|
| `state-bootstrap ENV=staging` | creates the env's state resource group, storage account and container with the az CLI, idempotent |
| `staging-plan` | `init` then `plan -out=tfplan` (plan file is encrypted) |
| `staging-apply` | applies `tfplan`, then writes the inventory |
| `staging-state-backup` | lists the earlier versions of the state blob; `STATE_ARGS="restore <version-id>"` puts one back |
| `staging-inventory` | rewrites `hosts.json` from state |
| `staging-destroy` | `destroy` |
| `production-*` | same; `production-destroy` also needs `CONFIRM=production`, and fails while the delete locks exist |
| `tofu-check` | `fmt -check`, `init -backend=false`, `validate`, `tofu test` for both envs |

### State history

Blob versioning on the state account keeps every earlier state as a version, still encrypted
with the state passphrase, and soft delete keeps a deleted blob for 35 days. To recover from an
overwritten or deleted state, with no `tofu` command running:

```sh
make staging-state-backup                   # lists version ids, current version last
make staging-state-backup STATE_ARGS="restore 2026-11-20T08:00:00.1234567Z"
make staging-plan                           # must show no unexpected changes
```

### Restore node

`restore_enabled = true` adds `<env>-restore-1` (tier `restore`, sized like a core node, with the
core identity, private address `.41` in `restore_zone`) to the VMs and to the inventory group
`restore`, which `playbooks/restore.yml` targets
([../runbooks/point-in-time-restore.md](../runbooks/point-in-time-restore.md),
[../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md)). Set it back to `false`
after the restore.

## 5. Open items

| What | Needs |
|---|---|
| Admin SSH ranges and keys | I13, I15 |
| SOPS file with the passphrase and the Cloudflare token | I14 (recipients) |
| `backup_lock_state = "Locked"` for production (irreversible; [backups.md](backups.md) section 1) | operator decision after the first verified backup week |
| The first apply | the lead, after review |

## 6. Procedure, step by step

Staging is shown; production is the same with `production` (and `CONFIRM=production` only for
`production-destroy`). Needs [prerequisites.md](prerequisites.md) done (including `az login`),
`sops` and `tofu` on `PATH`, and `infra/secrets/staging/tofu.enc.env` from
[secrets.md](secrets.md) section 5.

1. **Write `infra/tofu/envs/staging/terraform.tfvars`** with the non-secret inputs. Its shape
   is `tests/infra/sample.tfvars`, with real values:

   ```hcl
   base_domain            = "veristar.app"
   admin_cidrs            = ["<an operator input>"]
   admin_ssh_public_keys  = ["<an operator input>"]
   subscription_id        = "<an operator input>"
   state_resource_group   = "veristar-tfstate"
   state_storage_account  = "<globally unique, 3 to 24 lowercase letters and digits>"
   backup_storage_account = "<globally unique, 3 to 24 lowercase letters and digits>"
   server_image_version   = "0.20261104.2652"
   ```

   `subscription_id`, `state_resource_group` and `state_storage_account` must each be on one
   plain `key = "value"` line (the scripts read them with `sed`). Check the account names with
   `az storage account check-name -n <name>`. Without this file every target stops with
   `infra/tofu/envs/staging/terraform.tfvars is missing (inputs I4, I5, I13)`.

2. **Create the state storage** (once per environment):

   ```sh
   make state-bootstrap ENV=staging
   ```

   Expected: the created resources, then
   `ok: state storage <account> in veristar-tfstate (zrs, versioned, entra id only)`.

3. **Plan:**

   ```sh
   make staging-plan
   ```

   Expected on a new environment: `Plan: 55 to add, 0 to change, 0 to destroy.` (section 3.6).
   Nothing should be destroyed on a first run.

4. **Apply:**

   ```sh
   make staging-apply
   ```

   Expected: `Apply complete! Resources: 55 added, 0 changed, 0 destroyed.`, then the target
   writes `infra/ansible/inventories/staging/hosts.json`. Without a plan file it stops with
   `run make staging-plan first`.

5. **Check the inventory** matches section 1:

   ```sh
   python3 -m json.tool infra/ansible/inventories/staging/hosts.json | head -40
   ```

6. **Check SSH** to one node (cloud-init made `ops` with the keys of `<an operator input>`):

   ```sh
   ssh ops@<ansible_host of staging-edge-1> true && echo ok
   ```

7. **Check Traffic Manager** sees the nodes once Ansible has converged them (before that, port
   80 does not answer and every endpoint shows `Degraded`):

   ```sh
   az network traffic-manager endpoint list -g veristar-staging \
     --profile-name veristar-app-staging-in --type externalEndpoints \
     --query "[].{name:name,target:target,status:endpointMonitorStatus}" -o table
   ```

   Expected after the converge: every endpoint `Online`.

8. **Rewrite the inventory** later without changing anything: `make staging-inventory`.
