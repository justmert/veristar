---
title: "Prerequisites and accounts"
sidebarTitle: "Prerequisites"
description: "The accounts, values and controller tools an environment needs before the first command."
---

This page lists what you need before the first deployment command: the accounts and values you
provide, the hosting costs, and the tools on the machine you deploy from (the controller). The
deployment never substitutes a made-up value for a missing one: a step that needs a value you
have not provided stops and names it.

## Values you provide

| Value | What | Where it goes | Needed by |
|---|---|---|---|
| Repository | the Git repository, `github.com/justmert/veristar` or your fork | Go module path; `sa_signing_repository` in `infra/ansible/vars/signing.yml` (the only identity whose signed images `deploy.yml` accepts); `sa_image_registry` in `group_vars/all.yml` (images under `ghcr.io/<owner>/veristar/`) | release images, deploy |
| GitHub OAuth App | one OAuth App per environment, with its client id and client secret; callback `https://app.<BASE_DOMAIN>/api/auth/github/callback` | `github_client_id`, `github_client_secret` in `infra/secrets/<env>/secrets.enc.yaml` (from `SA_GITHUB_CLIENT_ID`, `SA_GITHUB_CLIENT_SECRET`) | core nodes: the `secrets` role refuses a core node without it |
| Operator ids | the numeric GitHub ids of the SCF operators | `sa_operator_github_ids` in `infra/ansible/inventories/<env>/group_vars/all.yml` | `bootstrap.yml`, core role |
| Wallet and passkey operators (optional) | the Stellar account ids (G-addresses) of operators who sign in with a wallet and the passkey account addresses (C-addresses) of operators who sign in with a passkey; empty for none | `sa_operator_stellar_addresses` in `infra/ansible/inventories/<env>/group_vars/all.yml` | core role |
| `<BASE_DOMAIN>` | a registered domain whose zone is active in Cloudflare DNS (production: `veristar.app`) | `base_domain` in `infra/tofu/envs/<env>/terraform.tfvars`; the repository variable `STATUS_BASE_URL` (`https://status.<BASE_DOMAIN>`) for the SLO workflow | DNS, certificates, every hostname |
| Azure subscription | a `<subscription id>` and an `az login` of a user who may create resources in it | `subscription_id`, `state_resource_group`, `state_storage_account`, `backup_storage_account`, `server_image_version` and `location` in `terraform.tfvars` (`node_vm_size` defaults to `Standard_B2as_v2`) | every Azure step; backups use the nodes' managed identity, so no storage key exists |
| Cloudflare API token | a token limited to the zone of `<BASE_DOMAIN>` with the permissions `Zone:Zone:Read` and `Zone:DNS:Edit` | `CLOUDFLARE_API_TOKEN` in `infra/secrets/<env>/tofu.enc.env` (OpenTofu's records); `cloudflare_api_token` in `secrets.enc.yaml` (from `SA_CLOUDFLARE_API_TOKEN`, Caddy's DNS-01 challenge) | DNS records, certificates |
| `admin_cidrs` | the source address ranges allowed to reach SSH | `admin_cidrs` in `terraform.tfvars` (it refuses `0.0.0.0/0`); its IPv4 ranges also reach the backup storage account | network security group, host firewall, storage network rules |
| Admin SSH public keys | Ed25519 or RSA public keys for the `ops` user | `admin_ssh_public_keys` in `terraform.tfvars` | cloud-init on every server |
| age recipients | the age public keys of everyone who may decrypt the environment's secrets, at least two people | `infra/secrets/recipients/<env>.txt` | every SOPS file |
| Registry pull token (optional) | needed only when the image packages are private: a classic GitHub token with `read:packages` only, with `sa_registry_login` and `sa_registry_login_user` set in `group_vars/all.yml`. Public packages, as in production, need no token and leave `sa_registry_login` empty | `ghcr_pull_token` in `secrets.enc.yaml` (from `SA_GHCR_PULL_TOKEN`) | every node and the controller; rotation in [../runbooks/secret-rotation.md](../runbooks/secret-rotation.md) |
| Discord webhook (optional) | a webhook URL that receives alerts | `alertmanager_discord_webhook_url`, `gatus_discord_webhook_url` in `secrets.enc.yaml`, added with `sops edit` | Alertmanager and Gatus push delivery. Without it, firing alerts are listed on the console's operator Alerts page ([../operators/alerts.md](../operators/alerts.md)); adding it later is described in [../runbooks/alerts.md](../runbooks/alerts.md) |
| npm scope (optional) | an npm scope with trusted publishing for the repository | `release-tracker.yml` | publishing the tracker package to npm; not needed to deploy |

The environment's Ansible variables that are not secret go in
`infra/ansible/inventories/<env>/group_vars/all.yml` ([bootstrap.md](bootstrap.md) step 4):
`sa_image_registry` and `sa_operator_github_ids`. The file already holds the settings every
Azure node shares (`sa_tls: cloudflare`, `sa_public_bind_ipv4`).

**PostgreSQL.** Every environment runs PostgreSQL 18.7 (`postgres:18.7-trixie`, PGDG
`18.7-1.pgdg13+2`), pinned in `infra/ansible/vars/pins.yml` and
`deploy/images/postgres-ha.Dockerfile`.

## Hosting providers and cost

### Providers

| Provider | Used for |
|---|---|
| Microsoft Azure, region Denmark East (`denmarkeast`, three availability zones) | 3 converged VMs, the virtual network, network security groups, static public IPv4 addresses, Traffic Manager (health-checked DNS failover), Blob Storage (pgBackRest backups) |
| Microsoft Azure, any region | the OpenTofu state storage account (production keeps it in Sweden Central) |
| GitHub | the repository, the release workflows, the platform images on `ghcr.io` |
| Cloudflare | registrar and DNS for `<BASE_DOMAIN>`, DNS only (no proxy) |

The external probe runs on a separate virtual machine in Azure East US, outside the production
region and resource group ([probe.md](probe.md)).

**Region.** Production runs in Denmark East because it offered `Standard_B2as_v2` in all three
availability zones to the subscription and allocated a test VM in each zone. Of the EU regions
checked on 2026-11-13 (Sweden Central, Denmark East, North Europe, West Europe, France Central,
Germany West Central, Poland Central, Italy North, Austria East, Belgium Central, Spain Central,
Switzerland North, Norway East), the subscription's SKU list (`Microsoft.Compute/skus`) offered
the size in three zones only in Sweden Central and Denmark East, and Sweden Central answered
`ZonalAllocationFailed` in every zone. Before choosing another region, run the same two checks
(section "Check the Azure subscription").

### Monthly cost estimate per environment

Prices are from the Azure retail prices API (`https://prices.azure.com/api/retail/prices`, pay
as you go, USD excluding VAT), read on 2026-11-13. Prices change: read them again before you
order. On that date the API published no `Standard_B2as_v2` price for Denmark East, so the VM row
uses the Sweden Central price as the reference. Disks, public addresses and Traffic Manager have
the same published prices in Denmark East.

| Item | Unit price (USD) | Quantity | Per month (USD) |
|---|---|---|---|
| `Standard_B2as_v2` node VM (2 vCPU, 8 GB) | 0.0778 per hour (Sweden Central reference) | 3 x 730 h | 170.38 |
| Standard SSD E10 (128 GiB, LRS), node OS disk | 9.60 | 3 | 28.80 |
| Standard static public IPv4 | 0.005 per hour | 3 x 730 h | 10.95 |
| Traffic Manager health checks, external endpoints | 0.54 per endpoint | 12 | 6.48 |
| Traffic Manager 10 s fast probing (`in`, `otlp`) | 2.00 per endpoint | 6 | 12.00 |
| Virtual network, network security groups, managed identity | no charge | | 0.00 |
| Cloudflare DNS (zone on the free plan) | no charge | 1 | 0.00 |
| **Fixed monthly total, pay as you go** | | | **228.61** |

Usage-based items come on top: Traffic Manager DNS queries (0.54 per million), Blob Storage ZRS
hot (0.023 per GB-month, writes 0.0625 per 10,000), Standard SSD transactions (0.002 per
10,000), internet egress (the first 100 GB a month free, then 0.087 per GB), and inter-zone
traffic (0.01 per GB in each direction).

With 1-year reserved instances for the three VMs (402.00 per year each for `Standard_B2as_v2`
in Sweden Central, from the same API), the VMs cost 100.50 a month instead of 170.38 and the
fixed total is 158.73.

### Account holders

Whoever holds the domain, the Azure subscription, the Cloudflare account and the repository,
the arrangement meets these requirements:

- Each provider bills the account holder monthly, by card or invoice, and suspends unpaid
  accounts. A failed payment is a single point of failure
  ([../reference/availability.md](../reference/availability.md)), so the account holder
  receives the providers' billing alerts.
- The domain is renewed for the whole service window plus the handover, with automatic renewal
  on.
- Every provider account has at least two administrators, so one person's absence cannot lock
  the operator out.

The handover moves these accounts as listed in [../handover/checklist.md](../handover/checklist.md).

## Controller tools

The controller is the Linux or macOS machine you deploy from, with the repository checked out.

| Tool | Version | How to get it | Check |
|---|---|---|---|
| OpenTofu | 1.13.9 | release `tofu_1.13.9_<os>_<arch>.zip`, checked against `tofu_1.13.9_SHA256SUMS` | `tofu version` prints `OpenTofu v1.13.9` |
| Azure CLI | 2.91.0 or later | [Install the Azure CLI](https://learn.microsoft.com/cli/azure/install-azure-cli); then `az login` and `az account set --subscription <subscription id>` | `az account show --query id -o tsv` prints your subscription id |
| uv | a current release | [astral.sh/uv](https://docs.astral.sh/uv/) | `uv --version` |
| Python | 3.13 (installed by uv for the venv) | `make ansible-venv` | |
| Ansible 14.6.0 (ansible-core 2.21.6), ansible-lint, yamllint, bcrypt, PyYAML | pinned in `infra/ansible/requirements.txt` | `make ansible-venv` (venv at `~/.local/share/veristar/ansible-venv`) | `~/.local/share/veristar/ansible-venv/bin/ansible --version` prints `ansible [core 2.21.6]` |
| SOPS, age | 3.13.3, 1.3.2 | `make ansible-venv` runs `playbooks/controller-tools.yml`, which downloads both into `~/.local/share/veristar/tools/` and checks their SHA-256 | `~/.local/share/veristar/tools/sops-3.13.3 --version` prints `sops 3.13.3` |
| cosign | 3.1.4 | release binary, on `PATH` (`deploy.yml` calls `cosign verify`) | `cosign version` shows `GitVersion: v3.1.4` |
| Go | 1.27.4 | [go.dev](https://go.dev/dl/), on `PATH` (the Makefile calls `go`) | `go version` |
| Docker Engine and Compose | 29.9.3, 5.6.1 | for local checks, builds and the local converge only | `docker version` |
| curl, openssl, ssh | from the operating system | | |

The Makefile calls `sops` and `tofu` from `PATH`. The controller tools install SOPS under a
versioned name, so either link it
(`ln -s ~/.local/share/veristar/tools/sops-3.13.3 ~/.local/bin/sops`) or pass it per command
(`make SOPS=~/.local/share/veristar/tools/sops-3.13.3 staging-plan`). Put `age` and
`age-keygen` from `~/.local/share/veristar/tools/age/` on `PATH` the same way.

## Steps

1. **Install the controller tools:**

   ```sh
   make ansible-venv
   ```

   Expected: uv creates the venv and installs the requirements, then the controller-tools play
   ends with `failed=0`.

2. **Check the local tooling.** These checks need no account:

   ```sh
   make tofu-check
   make ansible-lint
   ```

   Expected: for each of staging and production, `Success! The configuration is valid.` and
   `Success! 8 passed, 0 failed.`; ansible-lint ends with
   `Passed: 0 failure(s), 0 warning(s) ... Profile 'production' was required, and it passed.`

3. **Collect the values** in the table above. Each person who will decrypt secrets makes an age
   identity ([secrets.md](secrets.md) section 5) and sends only the public key.

4. **Check the Azure subscription** (next section).

5. **Create the Cloudflare API token** in the Cloudflare dashboard under My Profile, API Tokens,
   Create Token, Custom token: permissions `Zone` `Zone` `Read` and `Zone` `DNS` `Edit`; zone
   resources `Include` `Specific zone` `<BASE_DOMAIN>`; no account permissions. Caddy's DNS
   module and OpenTofu both look the zone up by name, which needs `Zone:Read` besides
   `Zone:DNS:Edit`. Store the token only in the SOPS files ([secrets.md](secrets.md)). One token
   per environment keeps a rotation local to that environment.

## Check the Azure subscription

Sign in on the controller and select the subscription:

```sh
az login
az account set --subscription <subscription id>
```

Then run these read-only checks for the region you deploy to:

| Check | Command | Pass |
|---|---|---|
| vCPU quota | `az vm list-usage --location denmarkeast -o table` | "Standard Basv2 Family vCPUs" and "Total Regional vCPUs" both have at least 8 free (6 vCPUs for the three nodes, 2 more for the restore node) |
| VM size in all three zones | `az vm list-skus --location denmarkeast --size Standard_B2as_v2 -o table` | zones 1, 2 and 3 listed, no restriction |
| Resource providers | `az provider show -n Microsoft.Network --query registrationState` | `Registered` for `Microsoft.Compute`, `Microsoft.Network`, `Microsoft.Storage`, `Microsoft.ManagedIdentity` and `Microsoft.Authorization` |
| Debian image version | `az vm image show -l denmarkeast --urn Debian:debian-13:13-gen2:<version>` | the version exists in offer `debian-13` |

OpenTofu registers no resource providers itself (`resource_provider_registrations = "none"`).
Register a missing one once with `az provider register --namespace <name>`. Raise a low quota
under Quotas in the Azure portal (Compute, your region, family "Standard Basv2 Family vCPUs").

`az vm image list -p Debian -f debian-13 --all` matches offers by prefix, so its output mixes
`debian-13` and `debian-13-daily`. Pin `server_image_version` to a version of offer
`debian-13`: a version that exists only in `debian-13-daily` fails with
`PlatformImageNotFound`.
