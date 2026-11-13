---
title: "Prerequisites and accounts"
sidebarTitle: "Prerequisites"
description: "The accounts, inputs and controller tools an environment needs before the first command."
---

Everything an environment needs before the first command, mapped to the plan's required inputs
. An input that is missing blocks the steps named in its row; nothing in the
repository substitutes a made-up value for one.

## Owner inputs

| Input | What | Where it goes | Needed by | Status 2026-11-13 |
|---|---|---|---|---|
| I1 | Git repository `github.com/justmert/veristar` | Go module path; `sa_signing_repository` in `infra/ansible/vars/signing.yml` (the only identity whose signed images `deploy.yml` accepts); images under `ghcr.io/justmert/veristar/` | release images, deploy | supplied; private, must be public before phases 18 and 20 publish |
| I2 | GitHub OAuth App per environment: client id and secret | `github_client_id`, `github_client_secret` in `infra/secrets/<env>/secrets.enc.yaml` (from `SA_GITHUB_CLIENT_ID`, `SA_GITHUB_CLIENT_SECRET`) | core nodes (the `secrets` role refuses a core node without it) | production supplied 2026-11-13: OAuth App "Veristar", callback `https://app.veristar.app/api/auth/github/callback`, in `infra/secrets/production/secrets.enc.yaml` |
| I3 | Numeric GitHub ids of the SCF operators | `sa_operator_github_ids` in `infra/ansible/inventories/<env>/group_vars/all.yml` | `bootstrap.yml`, core role | supplied 2026-11-13: `37740842` (justmert), in `inventories/production/group_vars/all.yml` |
| I4 | `BASE_DOMAIN`, registered, its zone active in Cloudflare DNS | `base_domain` in `infra/tofu/envs/<env>/terraform.tfvars`; `STATUS_BASE_URL` repository variable (`https://status.<an operator input>`) for the SLO workflow | DNS, certificates, every hostname | supplied 2026-11-13: `veristar.app`, registered at Cloudflare Registrar, zone active in Cloudflare |
| I5 | Azure subscription, with an `az login` of a user who may create resources in it | `subscription_id`, `state_resource_group`, `state_storage_account`, `backup_storage_account`, `server_image_version` in `terraform.tfvars` (the VM sizes default to `Standard_B2als_v2` and `Standard_B2as_v2`) | every Azure step; backups use the core nodes' managed identity, so there is no storage key | supplied 2026-11-13: one subscription, signed in with `az login` on an operator workstation; vCPU quota checked (section "Azure subscription" below) |
| I6 | Cloudflare API token limited to the zone of `<an operator input>`, permissions `Zone:Zone:Read` and `Zone:DNS:Edit` | `CLOUDFLARE_API_TOKEN` in `infra/secrets/<env>/tofu.enc.env` (OpenTofu's records); `cloudflare_api_token` in `secrets.enc.yaml` (from `SA_CLOUDFLARE_API_TOKEN`, Caddy DNS-01) | DNS records, Caddy DNS-01 certificates | supplied 2026-11-13: one token, active, scoped to `veristar.app` |
| I7 | no longer an input: the probe runs as a container on a host the operator already runs ([probe.md](probe.md)) | | | not needed |
| I8 | Discord webhook URL for alerts | `alertmanager_discord_webhook_url`, `gatus_discord_webhook_url` in `secrets.enc.yaml`, added with `sops edit` | Alertmanager, Gatus | missing |
| I9 | npm scope with trusted publishing for the repository | `release-tracker.yml` | publishing the tracker package; not needed to deploy | missing |
| I10 | Legal review of the compliance statements | `docs/compliance/` | publishing the statements as final (production launch); not needed to deploy staging | pending |
| I11 | Three SCF-awarded web projects and one API project | none in the repository | this stage adoption proof; not needed to deploy | missing |
| I12 | A second cloud project, a spare domain and a second GitHub owner | none yet | this stage handover rehearsal; not needed to deploy | missing |
| I13 | Admin source address ranges (CIDRs) allowed to reach SSH | `admin_cidrs` in `terraform.tfvars` (it refuses `0.0.0.0/0`); its IPv4 ranges also reach the backup storage account | network security group, host firewall, storage network rules | supplied 2026-11-13 for production: `203.0.113.10/32`, the operator's admin address |
| I14 | age public keys of everyone who may decrypt an environment's secrets (at least two per environment) | `infra/secrets/recipients/<env>.txt` | every SOPS file, so everything | production supplied 2026-11-13: one owner identity (`infra/secrets/recipients/production.txt`); a second recipient is still to be added |
| I15 | Admin SSH public keys for the `ops` user (Ed25519 or RSA; Azure accepts both) | `admin_ssh_public_keys` in `terraform.tfvars` | cloud-init on every server | supplied 2026-11-13 for production: the controller's key and an operator workstation key |

Besides the inputs, the environment's Ansible variables that are not secret go in
`infra/ansible/inventories/<env>/group_vars/all.yml` ([bootstrap.md](bootstrap.md) step 1):
`sa_image_registry` and `sa_operator_github_ids`. The file already holds the settings every
Azure node shares (`sa_tls: cloudflare`, `sa_public_bind_ipv4`).

**PostgreSQL.** Every environment runs PostgreSQL 18.7 (`postgres:18.7-trixie`, PGDG
`18.7-1.pgdg13+2`, pinned on 2026-11-13 in `infra/ansible/vars/pins.yml` and
`deploy/images/postgres-ha.Dockerfile`).

## Hosting: providers, cost and accounts for the 12-month service window

RFP section 3.4 makes the proposer host the service for 12 months. This section states what that
costs and who holds what. Prices were read on 2026-11-13 from the Azure retail prices API
(`https://prices.azure.com/api/retail/prices`, region `swedencentral`, pay as you go, USD
excluding VAT) and change: check them again before ordering.

### Providers

| Provider | Used for | Account input |
|---|---|---|
| Microsoft Azure, region Sweden Central (`swedencentral`, three availability zones) | 3 edge and 2 core VMs, the virtual network, network security groups, static public IPv4 addresses, Traffic Manager (health-checked DNS failover), Blob Storage (OpenTofu state and pgBackRest backups) | I5 |
| Cloudflare | registrar and DNS for `<an operator input>`, DNS only (no proxy) | I4, I6 |

The probe runs on a host outside Azure that the operator already operates ([probe.md](probe.md));
it adds no provider.

### Monthly cost estimate per environment

| Item | Unit price (USD) | Quantity | Per month (USD) |
|---|---|---|---|
| `Standard_B2als_v2` edge VM (2 vCPU, 4 GB) | 0.0389 per hour | 3 x 730 h | 85.19 |
| `Standard_B2as_v2` core VM (2 vCPU, 8 GB) | 0.0778 per hour | 2 x 730 h | 113.59 |
| Standard SSD E4 (32 GiB, LRS), edge OS disk | 2.40 | 3 | 7.20 |
| Standard SSD E10 (128 GiB, LRS), core OS disk | 9.60 | 2 | 19.20 |
| Standard static public IPv4 | 0.005 per hour | 5 x 730 h | 18.25 |
| Traffic Manager health checks, external endpoints | 0.54 per endpoint | 10 | 5.40 |
| Traffic Manager 10 s fast probing (`in`, `otlp`) | 2.00 per endpoint | 6 | 12.00 |
| Virtual network, network security groups, managed identity | no charge | | 0.00 |
| Cloudflare DNS (zone on the free plan) | no charge | 1 | 0.00 |
| **Fixed monthly total, pay as you go** | | | **260.83** |

Usage-based items come on top and have not been measured on a real environment yet:
Traffic Manager DNS queries (0.54 per million), Blob Storage ZRS hot (0.023 per GB-month, writes
0.0625 per 10,000), Standard SSD transactions (0.002 per 10,000), internet egress (the first
100 GB a month free, then 0.087 per GB), and inter-zone traffic, which the price list meters at
0.01 per GB in each direction.

With 1-year reserved instances for the five VMs (201.00 per year for `Standard_B2als_v2` and
402.00 per year for `Standard_B2as_v2`, from the same API), the VMs cost 117.25 a month instead
of 198.78 and the fixed total is 179.30. The 12-month service window matches the 1-year term. A
staging environment that runs all month costs the same as production without the reservation;
Azure bills VMs by the second, so staging destroyed between drills (`make staging-destroy`)
costs only the time it ran, plus its public addresses while they exist.

### Azure subscription

Checked on 2026-11-13 with read-only `az` commands against the operator's subscription:

| Check | Result |
|---|---|
| `az vm list-usage --location swedencentral` | Standard Basv2 Family vCPUs 0 of 65, Total Regional vCPUs 0 of 65. The design needs 10 (12 with the restore node). |
| `az vm list-skus --location swedencentral` | `Standard_B2als_v2` and `Standard_B2as_v2` offered in zones 1, 2 and 3, no restrictions |
| `az network list-usages --location swedencentral` | public IP addresses 0 of 100 |
| `az provider list` | `Microsoft.Compute`, `Microsoft.Network`, `Microsoft.Storage`, `Microsoft.ManagedIdentity`, `Microsoft.Authorization` registered |
| `az vm image list -l swedencentral -p Debian -f debian-13 -s 13-gen2 --all` | `-f` matches offers by prefix, so the list mixes `debian-13` and `debian-13-daily`. In offer `debian-13` the newest version on 2026-11-13 is `0.20261111.2659`; `0.20261104.2652` and `0.20261112.2660` exist only in `debian-13-daily`. Check a version with `az vm image show -l swedencentral --urn Debian:debian-13:13-gen2:<version>` |

OpenTofu registers no resource providers itself (`resource_provider_registrations = "none"`).
A new subscription registers the five above once:
`az provider register --namespace Microsoft.Network` (and the others), then checks
`az provider show -n Microsoft.Network --query registrationState`. If a quota is lower in
another subscription, raise it under Quotas in the Azure portal (Compute, region Sweden Central,
family "Standard Basv2 Family vCPUs").

### Account holder, billing and renewal

**Not decided: blocked on SCF.** Who holds the domain, the Azure subscription, the Cloudflare
account and the repository from day one is an open question to SCF (the design, "Who owns
the domain, cloud accounts and repository from day one?"). The answer sets who pays, who
receives the providers' billing and abuse mail, and what the handover moves
([../handover/checklist.md](../handover/checklist.md)). Until it is answered, this document
states only the requirements any arrangement must meet:

- Each provider is billed by card or invoice to the account holder monthly, for at least the
  12-month window; both suspend unpaid accounts. The availability statement lists a billing
  failure as a single point of failure ([../reference/availability.md](../reference/availability.md)),
  so the payment method needs an owner who is alerted to failed charges and a second person who
  can pay.
- The domain must be renewed for the whole window plus the handover an operator input, with automatic
  renewal on.
- Every provider account holds at least two administrators, so one person's absence cannot lock
  the operator out.

## Controller (the machine you deploy from)

A Linux or macOS machine with the repository checked out. Versions are the the design pins.

| Tool | Version | How to get it | Check |
|---|---|---|---|
| OpenTofu | 1.13.9 | release `tofu_1.13.9_<os>_<arch>.zip`, checked against `tofu_1.13.9_SHA256SUMS` ([opentofu.md](opentofu.md) section 3.1) | `tofu version` prints `OpenTofu v1.13.9` |
| Azure CLI | 2.91.0 (the version used on 2026-11-13) | learn.microsoft.com/cli/azure/install-azure-cli; then `az login` and `az account set --subscription <an operator input>` | `az account show --query id -o tsv` prints the subscription of an operator input |
| uv | any recent | astral.sh | `uv --version` |
| Python | 3.13 (installed by uv for the venv) | `make ansible-venv` | |
| Ansible 14.6.0 (ansible-core 2.21.6), ansible-lint, yamllint, bcrypt, PyYAML | pinned in `infra/ansible/requirements.txt` | `make ansible-venv` (venv at `~/.local/share/veristar/ansible-venv`) | `~/.local/share/veristar/ansible-venv/bin/ansible --version` prints `ansible [core 2.21.6]` |
| SOPS, age | 3.13.3, 1.3.2 | `make ansible-venv` runs `playbooks/controller-tools.yml`, which downloads both and checks their SHA-256 into `~/.local/share/veristar/tools/` | `~/.local/share/veristar/tools/sops-3.13.3 --version` prints `sops 3.13.3` |
| cosign | 3.1.4 | release binary, on `PATH` (`deploy.yml` calls `cosign verify`) | `cosign version` shows `GitVersion: v3.1.4` |
| Go | 1.27.4 | go.dev, on `PATH` (the Makefile calls `go`) | `go version` |
| Docker Engine and Compose | 29.9.3, 5.6.1 | only for local checks, builds and the local converge | `docker version` |
| curl, openssl, ssh | system | | |

The Makefile calls `sops` and `tofu` from `PATH`. The controller tools install SOPS under a
versioned name, so either link it (`ln -s ~/.local/share/veristar/tools/sops-3.13.3 ~/.local/bin/sops`)
or pass it per command (`make SOPS=~/.local/share/veristar/tools/sops-3.13.3 staging-plan`).
Put `age` and `age-keygen` from `~/.local/share/veristar/tools/age/` on `PATH` the same way.

## Steps

1. **Install the controller tools:**

   ```sh
   make ansible-venv
   ```

   Expected: uv creates the venv, installs the requirements, then the controller-tools play ends
   with `failed=0`.

2. **Check the local tooling works without any account:**

   ```sh
   make tofu-check
   make ansible-lint
   ```

   Expected (run 2026-11-13): for each of staging and production `Success! The configuration is
   valid.` and `Success! 8 passed, 0 failed.`; ansible-lint ends with
   `Passed: 0 failure(s), 0 warning(s) ... Profile 'production' was required, and it passed.`

3. **Collect the inputs** in the table above that the environment needs: I2 to I6, I8 and I13 to
   I15 for a full staging, plus I1. Each person who will decrypt secrets makes an age identity
   ([secrets.md](secrets.md) section 4) and sends only the public key.

4. **Sign in to Azure** on the controller and select the subscription:

   ```sh
   az login
   az account set --subscription <an operator input>
   az vm list-usage --location swedencentral -o table | grep -E 'Basv2|Total Regional vCPUs'
   ```

   Expected: both lines show a limit of at least 12.

5. **Create the Cloudflare API token** an operator input in the Cloudflare dashboard under My Profile,
   API Tokens, Create Token, Custom token: permissions `Zone` `Zone` `Read` and `Zone` `DNS`
   `Edit`; zone resources `Include` `Specific zone` `<an operator input>`; no account permissions; client
   IP filtering optional. Caddy's DNS module looks the zone up by name, which needs `Zone:Read`
   besides `Zone:DNS:Edit`; OpenTofu's zone lookup needs the same two. Store it only in the SOPS
   files ([secrets.md](secrets.md)). One token per environment keeps a rotation local.

Steps 1 and 2 were run on 2026-11-13 with the output quoted above. The quota check of step 4 was
run on an operator workstation on 2026-11-13 (section "Azure subscription"), and the token of step 5
exists for `veristar.app`.
