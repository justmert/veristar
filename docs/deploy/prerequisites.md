# Prerequisites and accounts

Everything an environment needs before the first command, mapped to the plan's required inputs
. An input that is missing blocks the steps named in its row; nothing in the
repository substitutes a made-up value for one.

## Owner inputs

| Input | What | Where it goes | Needed by | Status 2026-11-13 |
|---|---|---|---|---|
| I1 | Git repository `github.com/justmert/veristar` | Go module path; `sa_signing_repository` in `infra/ansible/vars/signing.yml` (the only identity whose signed images `deploy.yml` accepts); images under `ghcr.io/justmert/veristar/` | release images, deploy | supplied; private, must be public before phases 18 and 20 publish |
| I2 | GitHub OAuth App per environment: client id and secret | `github_client_id`, `github_client_secret` in `infra/secrets/<env>/secrets.enc.yaml` (from `SA_GITHUB_CLIENT_ID`, `SA_GITHUB_CLIENT_SECRET`) | core nodes (the `secrets` role refuses a core node without it) | missing |
| I3 | Numeric GitHub ids of the SCF operators | `sa_operator_github_ids` in `infra/ansible/inventories/<env>/group_vars/all.yml` | `bootstrap.yml`, core role | missing |
| I4 | `BASE_DOMAIN`, registered, zone delegated to bunny.net | `base_domain` in `infra/tofu/envs/<env>/terraform.tfvars`; `STATUS_BASE_URL` repository variable (`https://status.<an operator input>`) for the SLO workflow | DNS, certificates, every hostname | missing |
| I5 | Hetzner Cloud API token; Hetzner Object Storage S3 keys (made by hand in the Hetzner console, there is no API for them) | `HCLOUD_TOKEN`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` in `infra/secrets/<env>/tofu.enc.env`; `object_storage_access_key`, `object_storage_secret_key` in `secrets.enc.yaml` (from `SA_OBJECT_STORAGE_ACCESS_KEY`, `SA_OBJECT_STORAGE_SECRET_KEY`); `state_bucket`, `state_location`, `backup_bucket`, `backup_location`, `edge_server_type`, `core_server_type`, `server_image` in `terraform.tfvars` | every cloud step, backups | missing |
| I6 | bunny.net account API key (team member keys are not supported) | `BUNNYNET_API_KEY` in `tofu.enc.env`; `bunny_api_key` in `secrets.enc.yaml` (from `SA_BUNNY_API_KEY`) | DNS zone, health-checked records, Caddy DNS-01 certificates | missing |
| I7 | Scaleway API credentials | `SCW_ACCESS_KEY`, `SCW_SECRET_KEY`, `SCW_DEFAULT_PROJECT_ID` in `tofu.enc.env`; `probe_enabled = true`, `probe_instance_type`, `probe_image` in `terraform.tfvars` | the probe node (status page, prober) | missing |
| I8 | Discord webhook URL for alerts | `alertmanager_discord_webhook_url`, `gatus_discord_webhook_url` in `secrets.enc.yaml`, added with `sops edit` | Alertmanager, Gatus | missing |
| I9 | npm scope with trusted publishing for the repository | `release-tracker.yml` | publishing the tracker package; not needed to deploy | missing |
| I10 | Legal review of the compliance statements | `docs/compliance/` | publishing the statements as final (production launch); not needed to deploy staging | pending |
| I11 | Three SCF-awarded web projects and one API project | none in the repository | this stage adoption proof; not needed to deploy | missing |
| I12 | A second cloud project, a spare domain and a second GitHub owner | none yet | this stage handover rehearsal; not needed to deploy | missing |
| I13 | Admin source address ranges (CIDRs) allowed to reach SSH | `admin_cidrs` in `terraform.tfvars` (it refuses `0.0.0.0/0`) | cloud firewall, host firewall | missing |
| I14 | age public keys of everyone who may decrypt an environment's secrets (at least two per environment) | `infra/secrets/recipients/<env>.txt` | every SOPS file, so everything | missing |
| I15 | Admin SSH public keys for the `ops` user | `admin_ssh_public_keys` in `terraform.tfvars` | cloud-init on every server | missing |

Besides the inputs, the environment's Ansible variables that are not secret go in
`infra/ansible/inventories/<env>/group_vars/all.yml` ([bootstrap.md](bootstrap.md) step 1):
`sa_image_registry`, `sa_tls`, `sa_operator_github_ids`.

**Upstream blocker.** The core role refuses any environment other than the local converge while
PostgreSQL is below 18.7 (`postgres_version` and `postgres_min_version` in
`infra/ansible/vars/pins.yml`). On 2026-11-12 neither `postgres:18.7-trixie` nor the PGDG trixie
package existed. Until they do, a staging converge stops at the core role.

## Hosting: providers, cost and accounts for the 12-month service window

RFP section 3.4 makes the proposer host the service for 12 months. This section states what that
costs and who holds what. Prices were read on 2026-11-13, in euro excluding VAT unless marked,
and change: check them again before ordering.

### Providers

| Provider | Used for | Account input |
|---|---|---|
| Hetzner Cloud | 3 edge and 2 core servers in `fsn1`, `nbg1`, `hel1`, the private network, primary IPs, Object Storage (OpenTofu state and pgBackRest backups) | I5 |
| bunny.net | the DNS zone with health-checked record sets | I6 |
| Scaleway | the probe server in `fr-par` (status page, prober), outside Hetzner on purpose | I7 |

### Monthly cost estimate per environment

Server types are inputs (`edge_server_type`, `core_server_type`, `probe_instance_type`); the
estimate uses the planning defaults `cpx22` (edge) and `cpx42` (core) from
[opentofu.md](opentofu.md) and, for the probe, `DEV1-S` with the 20 GB root volume of
`infra/tofu/envs/*/main.tf`.

| Item | Unit price | Quantity | Per month |
|---|---|---|---|
| Hetzner `cpx22` (2 vCPU, 4 GB) | 16.99 | 3 | 50.97 |
| Hetzner `cpx42` (8 vCPU, 16 GB) | 59.49 | 2 | 118.98 |
| Hetzner primary IPv4 (IPv6 free) | 0.50 | 5 | 2.50 |
| Hetzner private network | free | 1 | 0.00 |
| Hetzner Object Storage base price (1 TB storage and 1 TB egress included) | 6.49 | 1 per project | 6.49 |
| **Hetzner subtotal** | | | **178.94** |
| Scaleway `DEV1-S` probe | 0.008976 per hour | 730 h | 6.55 |
| Scaleway flexible IPv4 | 0.005 per hour (assumed hourly, see below) | 730 h | 3.65 |
| Scaleway block volume, 5K IOPS, 20 GB | 0.00013 per GB-hour (assumed) | 20 GB, 730 h | 1.90 |
| **Probe subtotal** | | | **12.10** |
| bunny.net DNS, zones and monitoring | free, with the account minimum of USD 1 a month | 1 per account | USD 1.00 |

So production costs about 191 EUR plus USD 1 a month, about 2,300 EUR for the 12-month window.
A staging environment that runs all month costs about another 179 EUR (plus 12 EUR if its probe
is enabled); Hetzner and Scaleway bill by the hour, so staging destroyed between drills
(`make staging-destroy`) costs only the hours it ran. Traffic is within what is included (20 TB
a month per Hetzner server, 1 TB of Object Storage egress). Backup storage beyond the included
1 TB costs 0.0087 per TB-hour, about 6.35 per TB-month; the backup volume has not been measured
on a real environment an operator input.

Sources, read 2026-11-13:
[Hetzner Cloud regular performance](https://www.hetzner.com/cloud/regular-performance/) (cpx22,
cpx42), [Hetzner IP pricing](https://docs.hetzner.com/general/infrastructure-and-availability/ipv4-pricing/)
(cloud primary IPv4 0.50), [Hetzner Object Storage](https://www.hetzner.com/storage/object-storage/)
(base price 6.49, which also lists object locking and versioning),
[bunny.net DNS pricing](https://bunny.net/pricing/dns/) (free DNS and monitoring, USD 1 monthly
account minimum), and Scaleway's public product catalogue API
(`https://api.scaleway.com/product-catalog/v2alpha1/public-catalog/products?zone=fr-par-1`; the
Scaleway pricing pages refused automated reads). The catalogue gives `DEV1-S` per hour, but the
IPv4 and block volume units only as "per ip" and "per GB"; they are read as hourly here, which
is how Scaleway bills them, and should be confirmed in the Scaleway console. On the same day the
catalogue reported `STARDUST1-S` in `fr-par-1` as short of stock, and Hetzner's order page showed
the CPX types as unavailable in `hel1`: availability per location is part of an operator input and must
be checked before `terraform.tfvars` is written.

### Account holder, billing and renewal

**Not decided: blocked on SCF.** Who holds the domain, the Hetzner, bunny.net and Scaleway
accounts and the repository from day one is an open question to SCF (the design, "Who owns
the domain, cloud accounts and repository from day one?"). The answer sets who pays, who
receives the providers' billing and abuse mail, and what the handover moves
([../handover/checklist.md](../handover/checklist.md)). Until it is answered, this document
states only the requirements any arrangement must meet:

- Each provider is billed by card or invoice to the account holder monthly, for at least the
  12-month window; all three suspend unpaid accounts. The availability statement lists a billing
  failure as a single point of failure ([../reference/availability.md](../reference/availability.md)),
  so the payment method needs an owner who is alerted to failed charges and a second person who
  can pay.
- The domain must be renewed for the whole window plus the handover an operator input, with automatic
  renewal on.
- Every provider account holds at least two administrators, so one person's absence cannot lock
  the operator out.

## Controller (the machine you deploy from)

A Linux machine with the repository checked out. Versions are the the design pins.

| Tool | Version | How to get it | Check |
|---|---|---|---|
| OpenTofu | 1.13.9 | release `tofu_1.13.9_linux_amd64.zip`, checked against `tofu_1.13.9_SHA256SUMS` ([opentofu.md](opentofu.md) section 3.1) | `tofu version` prints `OpenTofu v1.13.9` |
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
   valid.` and `Success! 4 passed, 0 failed.`; ansible-lint ends with
   `Passed: 0 failure(s), 0 warning(s) ... Profile 'production' was required, and it passed.`

3. **Collect the inputs** in the table above that the environment needs: I2 to I8 and I13 to I15
   for a full staging, plus I1. Each person who will decrypt secrets makes an age identity
   ([secrets.md](secrets.md) section 4) and sends only the public key.

4. **Delegate the zone** of `<an operator input>` to bunny.net's name servers at the registrar. OpenTofu
   creates the zone; the delegation is a registrar setting.

5. **Create the Hetzner Object Storage keys** by hand in the Hetzner console an operator input. There is
   no API for them in hcloud 1.70.2.

Step 1 was run in this stage on the development host (the venv and tools there come from it); step 2
was run again on 2026-11-13 with the output quoted above. Steps 3 to 5 have not been done: the
inputs do not exist yet.
