# Prerequisites and accounts

Everything an environment needs before the first command, mapped to the plan's required inputs
. An input that is missing blocks the steps named in its row; nothing in the
repository substitutes a made-up value for one.

## Owner inputs

| Input | What | Where it goes | Needed by | Status 2026-11-13 |
|---|---|---|---|---|
| I1 | Git repository `github.com/justmert/stellar-analytics` | Go module path; `sa_signing_repository` in `infra/ansible/vars/signing.yml` (the only identity whose signed images `deploy.yml` accepts); images under `ghcr.io/justmert/stellar-analytics/` | release images, deploy | supplied; private, must be public before phases 18 and 20 publish |
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

## Controller (the machine you deploy from)

A Linux machine with the repository checked out. Versions are the the design pins.

| Tool | Version | How to get it | Check |
|---|---|---|---|
| OpenTofu | 1.13.9 | release `tofu_1.13.9_linux_amd64.zip`, checked against `tofu_1.13.9_SHA256SUMS` ([opentofu.md](opentofu.md) section 3.1) | `tofu version` prints `OpenTofu v1.13.9` |
| uv | any recent | astral.sh | `uv --version` |
| Python | 3.13 (installed by uv for the venv) | `make ansible-venv` | |
| Ansible 14.6.0 (ansible-core 2.21.6), ansible-lint, yamllint, bcrypt, PyYAML | pinned in `infra/ansible/requirements.txt` | `make ansible-venv` (venv at `~/.local/share/stellar-analytics/ansible-venv`) | `~/.local/share/stellar-analytics/ansible-venv/bin/ansible --version` prints `ansible [core 2.21.6]` |
| SOPS, age | 3.13.3, 1.3.2 | `make ansible-venv` runs `playbooks/controller-tools.yml`, which downloads both and checks their SHA-256 into `~/.local/share/stellar-analytics/tools/` | `~/.local/share/stellar-analytics/tools/sops-3.13.3 --version` prints `sops 3.13.3` |
| cosign | 3.1.4 | release binary, on `PATH` (`deploy.yml` calls `cosign verify`) | `cosign version` shows `GitVersion: v3.1.4` |
| Go | 1.27.4 | go.dev, on `PATH` (the Makefile calls `go`) | `go version` |
| Docker Engine and Compose | 29.9.3, 5.6.1 | only for local checks, builds and the local converge | `docker version` |
| curl, openssl, ssh | system | | |

The Makefile calls `sops` and `tofu` from `PATH`. The controller tools install SOPS under a
versioned name, so either link it (`ln -s ~/.local/share/stellar-analytics/tools/sops-3.13.3 ~/.local/bin/sops`)
or pass it per command (`make SOPS=~/.local/share/stellar-analytics/tools/sops-3.13.3 staging-plan`).
Put `age` and `age-keygen` from `~/.local/share/stellar-analytics/tools/age/` on `PATH` the same way.

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
