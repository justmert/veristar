# OpenTofu: infrastructure, state and the Ansible inventory contract

Status: first version (this stage, stream a). Everything here was validated locally with
`tofu fmt`, `tofu validate` and `tofu test` against mocked providers. Nothing has been
applied to a real account yet. The section "Blocked on inputs" lists exactly what still
needs the operator's accounts.

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
  the directory (`-i infra/ansible/inventories/staging/`) so `group_vars/` and
  `host_vars/` beside `hosts.json` are picked up.

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
        "endpoint": "https://<location>.your-objectstorage.com",
        "region": "<location>",
        "backup_bucket": "<bucket name>"
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
      "probe": {
        "hosts": { "staging-probe-1": {} }
      },
      "restore": {
        "hosts": {}
      }
    }
  }
}
```

- Groups are always `edge` (3 hosts), `core` (2 hosts), `probe` (0 or 1 host; the
  probe is applied in this stage, so `probe.hosts` is `{}` until then) and `restore` (0 or 1
  host, present only while `restore_enabled`, section 4). The group keys are always present.
- Inventory host names equal the cloud server names: `<env>-<tier>-<index>`, index from 1.
  Index 1 of `core` is the node that runs PostgreSQL in this stage.
- Values written as `<I4>` and `<I13>` above are the real inputs once supplied; the
  file never contains anything else in their place.

### Host vars (every host)

| Var | Type | Meaning |
|---|---|---|
| `ansible_host` | string | public IPv4, used for SSH |
| `ansible_user` | string | always `ops`, created by cloud-init with the admin SSH keys and passwordless sudo; root login is left to the `base` role to disable |
| `sa_tier` | string | `edge`, `core` or `probe` |
| `sa_index` | number | 1-based index inside the tier |
| `sa_provider` | string | `hetzner` or `scaleway` |
| `sa_location` | string | `fsn1`, `nbg1`, `hel1` (Hetzner) or the Scaleway zone, e.g. `fr-par-1` |
| `sa_public_ipv4` | string | same as `ansible_host` |
| `sa_public_ipv6` | string or null | the server's public IPv6 address (`...::1` of its /64); null on the probe if it has none |
| `sa_private_ip` | string or null | address on the private network; null on the probe (different provider, no private network) |

Peers are derived by Ansible from the groups (for example NATS routes and etcd members
from `groups['edge'] | map('extract', hostvars, 'sa_private_ip')`). OpenTofu does not
emit peer lists, so there is one source of truth.

### Things the Ansible and Caddy side can rely on

- Hetzner cloud firewalls filter only the public interfaces. Traffic on the private
  network is not filtered by the cloud firewall, so "everything else private network
  only" depends on the host firewall from the `base` role matching section 3.2 below.
- Public firewall (edge and core): TCP 80, TCP 443, UDP 443 (HTTP/3) and ICMP from
  anywhere; TCP 22 only from the admin CIDRs. Nothing else is open publicly.
- The bunny.net HTTP monitor calls each address on port 80 and counts any non-5xx answer
  within 10 seconds as healthy. The Host header it sends is not documented, so the
  port 80 health answer must not depend on the Host header.
- Cloud-init only creates `ops` and installs SSH keys. `user_data` and `ssh_keys`
  changes are ignored after creation so that key rotation through Ansible never
  replaces a server.

### Secrets handed to OpenTofu

OpenTofu reads every credential from environment variables. The make targets load them
with `sops exec-env infra/secrets/<env>/tofu.enc.env '<tofu command>'`. That SOPS dotenv
file must be covered by the `creation_rules` in `infra/secrets/.sops.yaml` (owned by the
secrets stream) and holds:

| Variable | Used by | Input |
|---|---|---|
| `HCLOUD_TOKEN` | hcloud provider | I5 |
| `BUNNYNET_API_KEY` | bunnynet provider (team member keys are not supported) | I6 |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | S3 state backend and `make state-bootstrap` (Hetzner Object Storage keys) | I5 |
| `TF_VAR_state_passphrase` | state and plan encryption, at least 16 characters, generated, never reused across environments | generated, encrypted to I14 |
| `SCW_ACCESS_KEY`, `SCW_SECRET_KEY`, `SCW_DEFAULT_PROJECT_ID` | scaleway provider, only once the probe is enabled | I7 |

Non-secret inputs live in `infra/tofu/envs/<env>/terraform.tfvars` (committed once the
inputs exist). `tests/infra/sample.tfvars` shows every variable with documentation-only
values (RFC 2606 domain, RFC 5737 and RFC 3849 ranges); it is a test fixture, not a
template with real values.

## 2. Layout

```
infra/tofu/
  modules/network          private network, one subnet per location, public firewall
  modules/node             primary IPv4 and IPv6, server, private address, cloud-init
  modules/dns              bunny.net zone, health-monitored A/AAAA record sets, CAA
  modules/object-storage   endpoint, region and bucket names for Hetzner Object Storage (no resources, see 3.4)
  modules/probe            Scaleway instance, reserved IPv4, security group
  modules/inventory        pure function: node data in, inventory object out
  envs/staging             root module, S3 backend, state encryption
  envs/production          same modules, production network range and delete protection
tests/infra/               tofu test files and the sample tfvars
```

| Env | Private network | Edge | Core | Protection |
|---|---|---|---|---|
| staging | 10.20.0.0/16 | fsn1, nbg1, hel1 | fsn1, nbg1 | off (rebuilt in the drill) |
| production | 10.10.0.0/16 | fsn1, nbg1, hel1 | fsn1, nbg1 | delete and rebuild protection on servers and primary IPs |

Subnets: `10.x.1.0/24` fsn1, `10.x.2.0/24` nbg1, `10.x.3.0/24` hel1, all in network zone
`eu-central`. Node addresses in their location's subnet are `.11` to `.13` for edge nodes 1 to 3 and
`.21`, `.22` for core nodes 1 and 2 (staging: edge-1 `10.20.1.11`, edge-2 `10.20.2.12`,
edge-3 `10.20.3.13`, core-1 `10.20.1.21`, core-2 `10.20.2.22`), so a private address
tells the tier and location.

## 3. Research findings (pinned versions, read 2026-11-12)

### 3.1 OpenTofu 1.13.9

Installed into `~/.local/bin/tofu` from the GitHub release
`tofu_1.13.9_linux_amd64.zip`, SHA-256 checked against `tofu_1.13.9_SHA256SUMS`
(`OK`). gitleaks 8.30.1 likewise from `gitleaks_8.30.1_linux_x64.tar.gz` against
`gitleaks_8.30.1_checksums.txt`.

### 3.2 `hetznercloud/hcloud` 1.70.2

- `hcloud_network` (`ip_range`), `hcloud_network_subnet` (`type` = `cloud`, `server` or
  `vswitch`; `network_zone` required). Subnets belong to a network zone, not a location:
  all three locations share `eu-central`, so one network spans them.
- `hcloud_server`: `location` (the `datacenter` argument is deprecated), `public_net`
  links primary IPs, `network { subnet_id, ip, alias_ips }`. The docs say to set
  `alias_ips = []` to avoid detach and attach on every apply. `ssh_keys` changes force
  replacement unless ignored. `firewall_ids` attaches firewalls at creation, so a server
  is never public without its firewall.
- `hcloud_primary_ip`: `location` (datacenter deprecated since 1.67.0), `type`
  `ipv4`/`ipv6`, `auto_delete` (the docs advise against `true`), `delete_protection`.
  Separate primary IPs keep a node's public address across a server rebuild, so DNS and
  firewall allowlists elsewhere do not change.
- `hcloud_firewall`: `rule { direction, protocol (tcp, udp, icmp, gre, esp), port,
  source_ips }`, `apply_to`. Cloud firewalls act on public interfaces only.
- `hcloud_zone`, `hcloud_zone_rrset`, `hcloud_zone_record` exist (Hetzner DNS moved into
  the Cloud API). They have no health checks, so bunny.net stays the DNS provider.
- Object Storage: the provider's own guide (`docs/guides/s3-object-storage.md`) states
  that there is no Hetzner Cloud API for buckets or for S3 credentials. **Correction to
  the plan:** the `object-storage` module cannot issue credentials. S3 keys are created
  once by hand in the Hetzner Console and are part of an operator input. The module only derives
  endpoint, region and bucket names. Buckets stay with Ansible (backup bucket) and
  `make state-bootstrap` (state bucket), as planned.
- Server types and images per location need an authenticated API call
  (`GET /server_types`, `GET /images`). Not checked: blocked on I5. Server types and the
  image name are therefore required variables without defaults.

### 3.3 `BunnyWay/bunnynet` 0.20.1

- `bunnynet_dns_zone`: `domain` required; `dnssec_enabled`, `log_enabled`,
  `log_anonymized`, `log_anonymized_style` optional. Query logging is left off (it would
  record resolver addresses).
- `bunnynet_dns_record`: `zone`, `name` (`""` for apex), `type`, `value`; `ttl`
  (provider default 300, validator only `>= 0`), `weight` (A and AAAA 0 to 100, SRV up to
  65535, rejected on other types by the provider's plan check), `monitor_type`
  (`None`, `Ping`, `Http`, `Monitor`; API values 0 to 3, `Monitor` is undocumented),
  `flags` and `tag` (used for CAA).
- bunny.net docs (DNS Records page): monitoring tests each IP every 30 seconds from 3
  regions; `Http` sends a request to port 80, must complete within 10 s and return a
  non-5xx status. Single-value records are always returned; in a record set (same name
  and type) offline records are removed; if all are offline, all are returned. With
  equal weights up to 3 records are returned. No alerting on outages.
- Minimum TTL: not documented. The provider accepts any value `>= 0`; the dashboard's
  quick edit offers 15m as its smallest choice. The modules use 60 s, which is unverified
  until the first apply: blocked on I6. If bunny.net rejects or rewrites it, the value
  in `envs/*/main.tf` changes and this stage measures failover with the real value.
- Team member API keys are not supported by the provider.

### 3.4 OpenTofu S3 backend on Hetzner Object Storage

- Endpoints: `https://fsn1.your-objectstorage.com`, `nbg1`, `hel1`. Region is the location.
- Backend settings for a non-AWS S3: `endpoints.s3`, `skip_credentials_validation`,
  `skip_region_validation`, `skip_requesting_account_id`, `skip_metadata_api_check`,
  `skip_s3_checksum` (the docs name it for non-AWS S3 APIs).
- Locking: `use_lockfile = true` uses conditional writes (`If-None-Match`). Hetzner's
  "List of supported actions" says conditional PUT and DELETE are **not supported on
  versioned buckets**. The state bucket is therefore created unversioned, so locking
  works; state history is not kept by the bucket. Whether conditional PUT works on an
  unversioned Hetzner bucket is not stated and is checked on the first `tofu init`:
  blocked on I5.
- `backend` and `encryption` blocks read variables (early evaluation), so bucket,
  location and passphrase come from `terraform.tfvars` and the environment, and the
  bootstrap script reads the same `state_bucket` and `state_location` lines.

### 3.5 Native state encryption

- Key provider `pbkdf2` (passphrase of at least 16 characters, default 600,000
  iterations, sha512) feeding method `aes_gcm`, used for both `state` and `plan`, each
  with `enforced = true`, so OpenTofu refuses to write unencrypted state or plan files.
- The passphrase comes from `TF_VAR_state_passphrase` (SOPS). Changing it later uses a
  `fallback` block with the old key provider for one apply.
- Losing the passphrase makes the state unreadable. It is stored only in the SOPS file,
  encrypted to the I14 recipients.

### 3.6 `scaleway/scaleway` 2.91.0 (probe)

- `scaleway_instance_server`: `type`, `image` (label or UUID), `zone`, `ip_ids`,
  `security_group_id` (without one, an untracked default group is created),
  `root_volume { size_in_gb, volume_type }`, `user_data` with key `cloud-init`.
- `scaleway_instance_ip`: `type` `routed_ipv4` or `routed_ipv6`.
- Credentials: `SCW_ACCESS_KEY`, `SCW_SECRET_KEY`, `SCW_DEFAULT_PROJECT_ID`.
- Instance types and the Debian 13 image label need an authenticated call: blocked on
  I7, so both are variables without defaults.
- With `probe_enabled = false` the scaleway provider needs no credentials: a local
  `tofu plan` with the provider required, a count-0 resource and no `SCW_*` variables
  or config file reported "No changes". Staging and production can therefore be
  planned before I7 exists.

### 3.7 Local validation (2026-11-12)

- `tofu fmt -recursive -check`, `tofu init -backend=false` and `tofu validate` pass for
  both envs with the pinned providers (validate checks every argument against the
  providers' schemas). Providers sit in one shared plugin cache (about 90 MB).
- `tofu test` (tests/infra, mocked providers, `command = apply`) renders the inventory
  from `tests/infra/sample.tfvars` and checks: the three groups, host names, every host
  var, locations, the private address scheme, the group vars, the DNS record sets (three
  edge addresses per family for `in` and `otlp`, two core for `app` and `web`, all
  `Http` monitored), CAA (`issue letsencrypt.org`, `issuewild ;`), and that
  `0.0.0.0/0` for SSH and a short passphrase are refused. 4 of 4 runs pass per env, and a
  changed input makes the contract assertion fail.
- The env's `encryption` block, run with a local backend and a random passphrase: the
  state file holds no plaintext (a marker value was absent), the saved plan is not a
  readable plan archive, and a wrong passphrase fails with "decryption failed".
- gitleaks 8.30.1 (`gitleaks dir`) finds nothing in `infra/`, `tests/infra/`, `docs/`,
  `scripts/`, `Makefile`. Elsewhere it flags only ignored files (`deploy/compose/.env.dev`,
  `examples/astro-ssr/dist/`) and known false positives in tracked files: Stellar public
  keys (G...), test fixture tokens and passwords, Umami migration checksums and Stripe's
  published documentation key in the route normalization corpus.

## 4. Make targets

| Target | Does |
|---|---|
| `state-bootstrap ENV=staging` | creates the env's state bucket through the S3 API (curl SigV4), unversioned, idempotent |
| `staging-plan` | `init` then `plan -out=tfplan` (plan file is encrypted) |
| `staging-apply` | applies `tfplan`, writes the inventory, then copies the encrypted state into the backup bucket (`scripts/tofu-state-backup.sh`) |
| `staging-state-backup` | the same copy alone; `STATE_ARGS=list` lists the copies, `STATE_ARGS="restore <key>"` puts one back as the live state |
| `staging-inventory` | rewrites `hosts.json` from state |
| `staging-destroy` | `destroy` |
| `production-*` | same; `production-destroy` also needs `CONFIRM=production`, and fails while delete protection is on |
| `tofu-check` | `fmt -check`, `init -backend=false`, `validate`, `tofu test` for both envs |

`staging-deploy` belongs to the Ansible stream.

### State history

The state bucket stays unversioned (section 3.4), so it keeps no history. After every apply,
`scripts/tofu-state-backup.sh <env>` reads the state object `<env>/tofu.tfstate` and writes it
unchanged, still encrypted with the state passphrase, to
`tofu-state/<env>/<UTC timestamp>.tfstate` in the backup bucket, which is versioned and
object-locked for 35 days ([backups.md](backups.md)). Nothing is decrypted on the way.

To recover from an overwritten or deleted state, with no `tofu` command running:

```sh
make staging-state-backup STATE_ARGS=list
make staging-state-backup STATE_ARGS="restore tofu-state/staging/20261120T080000Z.tfstate"
make staging-plan        # must show no unexpected changes
```

`make ops-local` (step `state-backup`) checks copy, list, deletion of the live object, restore and
byte equality against the S3 stand-in; that step has not completed yet (the run of 2026-11-13
was stopped earlier). Never run against Hetzner an operator input.

### Restore node

`restore_enabled = true` adds `<env>-restore-1` (tier `restore`, sized like a core node, private
address `.41` in `restore_location`) to the servers and to the inventory group `restore`, which
`playbooks/restore.yml` targets ([../runbooks/point-in-time-restore.md](../runbooks/point-in-time-restore.md),
[../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md)). Set it back to `false` after
the restore. The inventory always has the `restore` group, empty without the node
(`tests/infra/*.tftest.hcl`, runs `inventory_without_restore` and `inventory_with_restore`).

## 5. Blocked on inputs

| What | Input |
|---|---|
| `tofu plan` and `apply` for either env, server types and image availability in fsn1, nbg1, hel1, private network creation | I5 |
| State bucket creation, S3 backend `init`, conditional PUT locking on Hetzner | I5 |
| DNS zone, monitored record sets, minimum TTL, CAA acceptance | I6 and I4 (zone delegation) |
| Probe instance type, image label, apply | I7 |
| SSH firewall rule values | I13 |
| SOPS file with the passphrase and credentials | I14 (recipients), I5, I6 |
| Admin SSH public keys for cloud-init (`admin_ssh_public_keys`) | not in the input table; needed from the operator with I13 |

## 6. Procedure, step by step

Staging is shown; production is the same with `production` (and `CONFIRM=production` only for
`production-destroy`). Needs [prerequisites.md](prerequisites.md) done, `sops` and `tofu` on
`PATH`, and `infra/secrets/staging/tofu.enc.env` from [secrets.md](secrets.md) section 5.
None of these steps has run against a real account: the expected outputs below are the
messages the Makefile, `scripts/tofu-state-bootstrap.sh` and OpenTofu print, not a recorded run.

1. **Write `infra/tofu/envs/staging/terraform.tfvars`** with the non-secret inputs. Its shape
   is `tests/infra/sample.tfvars`, with real values in place of the documentation-only ones:

   ```hcl
   base_domain           = "<an operator input>"
   admin_cidrs           = ["<an operator input>"]
   admin_ssh_public_keys = ["<an operator input>"]
   state_bucket          = "<state bucket name, an operator input>"
   state_location        = "fsn1"
   backup_bucket         = "<backup bucket name, an operator input>"
   backup_location       = "hel1"
   edge_server_type      = "<server type available in fsn1, nbg1 and hel1, an operator input>"
   core_server_type      = "<server type available in fsn1 and nbg1, an operator input>"
   server_image          = "<Debian 13 image name, an operator input>"
   ```

   `state_bucket` and `state_location` must each be on one plain `key = "value"` line (the
   bootstrap script reads them with `sed`). The planning defaults for the server types are
   `cpx22` (edge) and `cpx42` (core); check they can be ordered in those locations first.
   Without this file every target stops with
   `infra/tofu/envs/staging/terraform.tfvars is missing (inputs I4, I5, I13)` (checked
   2026-11-13).

2. **Create the state bucket** (once per environment):

   ```sh
   make state-bootstrap ENV=staging
   ```

   Expected: `created state bucket <name> in fsn1 (private, unversioned)`, or on a rerun
   `state bucket <name> already exists in fsn1`. Any other HTTP answer stops with
   `HEAD <url> answered <status>; not creating anything`. Without `ENV` the target prints
   `usage: make state-bootstrap ENV=staging|production`.

3. **Plan:**

   ```sh
   make staging-plan
   ```

   It runs `tofu init` against the S3 backend, then `plan -out=tfplan` (the plan file is
   encrypted). Expected: OpenTofu's `Plan: <n> to add, 0 to change, 0 to destroy.` on a new
   environment, creating the network, one subnet per location, the public firewall, the admin
   SSH keys, five servers with their primary IPv4 and IPv6 addresses, and the bunny.net zone
   with its monitored record sets and CAA. Read it; nothing should be destroyed on a first run.
   The first `init` is also the first test of conditional-PUT locking on Hetzner (section 3.4).

4. **Apply:**

   ```sh
   make staging-apply
   ```

   Expected: `Apply complete! Resources: <n> added, 0 changed, 0 destroyed.`, then the target
   writes `infra/ansible/inventories/staging/hosts.json`. Without a plan file it stops with
   `run make staging-plan first`.

5. **Check the inventory** matches section 1:

   ```sh
   python3 -m json.tool infra/ansible/inventories/staging/hosts.json | head -40
   ```

   Expected: `sa_environment` `staging`, the five hostnames under `sa_hostnames`, groups
   `edge` (three hosts), `core` (two) and `probe` (empty until the probe is enabled), each host
   with `ansible_user` `ops` and its private address in the scheme of section 2.

6. **Check SSH** to one node (cloud-init made `ops` with the keys of `<an operator input>`):

   ```sh
   ssh ops@<ansible_host of staging-edge-1> true && echo ok
   ```

7. **Rewrite the inventory** later without changing anything: `make staging-inventory`.

Locally verified on 2026-11-13: `make tofu-check` (formatting, `validate`, and the inventory
contract tests with mocked providers, 4 of 4 runs passing for each environment) and the message
of step 1.
