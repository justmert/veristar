---
title: "Secret rotation"
description: "Replace each secret of an environment, and what users notice while you do."
---

How to replace each secret of an environment, what changes for users while you do, and what
Ansible applies by itself. The secrets live in `infra/secrets/<env>/secrets.enc.yaml` (platform)
and `infra/secrets/<env>/tofu.enc.env` (OpenTofu), both SOPS files encrypted to the environment's
age recipients in `infra/secrets/recipients/<env>.txt` ([secrets](../deploy/secrets.md)). The
conventions shared by every runbook are in the [runbooks overview](overview.md).

Rotate when a value may have been exposed (a node compromised, a file or identity leaked, a
person with access left), when a provider forces it, or on a schedule the operators set.
Rotating everything in a file is the answer to a leaked file or identity.

## Everything at once

To replace every value of a file (a leaked file or identity, a person with access leaving, a
handover), use `infra/secrets/rotate-secrets.py <env>` and `playbooks/rotate-all.yml`: they apply
every step of the table below in the right order and prove each previous credential is refused
([rebuild and data migration](../handover/rebuild.md), section 7). The rest of this page is for
replacing one value.

## The general pattern

1. **Edit the value** in the SOPS file on the controller (`<env>` is `staging` or `production`):

   ```sh
   sops --config infra/secrets/.sops.yaml edit infra/secrets/<env>/secrets.enc.yaml
   ```

   Generate random values the way `infra/secrets/new-secrets.py` does (24 random bytes as hex
   for passwords, 32 random bytes in standard base64 for the control plane keys):

   ```sh
   python3 -c 'import secrets; print(secrets.token_hex(24))'
   python3 -c 'import base64, secrets; print(base64.b64encode(secrets.token_bytes(32)).decode())'
   ```

2. **Apply** with a converge at the release that runs now:

   ```sh
   make <env>-deploy SA_IMAGE_TAG=<current release> SA_RELEASE_MANIFEST=<release-images.json>
   ```

   `site.yml` rewrites the env files in `/etc/veristar/env/`, the NATS and Patroni
   configs, and runs `platformctl db roles` and `db umami-retention-role`.

   On Azure the backup bucket play of `site.yml` reads the storage account with the `az` CLI on
   the controller. When your `az` login lives on another machine you control, point the
   playbook at it with `storage_az_argv` in a vars file (Make passes `ANSIBLE_EXTRA` to the
   shell unquoted, so inline JSON loses its quotes):

   ```sh
   printf 'storage_az_argv: [ssh, <az host>, az]\n' > /tmp/az-argv.yml
   make <env>-deploy SA_IMAGE_TAG=<current release> SA_RELEASE_MANIFEST=<release-images.json> \
     ANSIBLE_EXTRA="-e @/tmp/az-argv.yml"
   ```

   Without it the run stops at `Read the storage account` with `No such file or directory: b'az'`,
   after the first play has already written the new env files to every node. Running services
   keep their old values until they are recreated, but a `retain-web` run that starts in between
   reads the new retention password before the role has it and fails, so fix the call and run
   the converge again at once.

3. **Make the services use it.** Compose recreates a service whose configuration changed; check
   that each affected container was recreated after the change (`sudo docker compose ps`, column
   `CREATED`) and recreate it if not:

   ```sh
   cd /opt/veristar/core && sudo docker compose up -d --force-recreate <service>
   ```

4. **Commit** the changed SOPS file.

## Per secret

| Key in `secrets.enc.yaml` | Used by | Applied by `site.yml` | Extra steps and effect |
|---|---|---|---|
| `platform_db_cp_system_password`, `platform_db_cp_tenant_password`, `platform_db_cp_operator_password`, `platform_db_metrics_writer_password`, `platform_db_jobs_password` | control plane, writer, jobs | yes: `platformctl db roles` sets each login, then the env files | the old password stops working at once, so the services fail to connect until they run with the new env file: do it in a quiet hour and recreate `controlplane`, `metricswriter`, `jobs` on every core node right after |
| `umami_db_jobs_retention_password` | `jobs retain-web` timer | yes: `platformctl db umami-retention-role` | none |
| `nats_passwords.<user>` with `nats_password_hashes.<user>` | that service's NATS login | yes: NATS config and env files, NATS reload | change the password and its bcrypt hash together (below). The service is refused by NATS between the NATS reload and its own restart in the same run; rotate one user at a time. For `gateway` and `collector` that is an ingestion gap on each edge node in turn |
| `nats_route_password` with `nats_route_password_hash` | NATS cluster routes | yes | routes reconnect after every server has reloaded; rotate when all three edge nodes are healthy |
| `oauth_state_key` | control plane, sign-in state | yes (env file) | sign-ins in progress fail once and must be started again |
| `umami_user_password_key` | control plane, jobs: every Umami user's daily password | env files only | after the converge, make Umami follow the new key at once (below); until then SSO into Umami fails |
| `umami_admin_password` | control plane, jobs, platformctl: the Umami admin service account | env files only | change it in Umami first (below), then converge |
| `umami_db_password` | Umami | env file only | change the role's password on the leader first (below), then converge and recreate `umami` on every core node |
| `umami_app_secret` | Umami (`APP_SECRET`) | env file only | Umami derives session ids from it: visits in progress split and returning visitors of the day count again; Umami tokens are invalidated. Recreate `umami` on every core node |
| `postgres_superuser_password`, `postgres_replication_password`, `patroni_restapi_password` | Patroni, bootstrap, observability | `patroni.yml` only | change the role in PostgreSQL first (below), converge, then reload Patroni |
| `pgbackrest_cipher_pass` | pgBackRest repository encryption | n/a | **cannot be rotated in place**: the repository is encrypted with it. A new value needs a new, empty repository (new bucket or path), a new full backup, and keeping the old repository and value until its backups expire |
| `github_client_id`, `github_client_secret` | control plane sign-in (the GitHub OAuth App) | yes (env file) | create a new client secret in the GitHub OAuth App settings, converge, recreate `controlplane`, then delete the old secret in GitHub |
| `cloudflare_api_token` | Caddy DNS-01 (the Cloudflare API token) | yes (`caddy.env`) | create a new token with the same permissions (`Zone:Zone:Read`, `Zone:DNS:Edit` on the zone), converge, recreate `caddy` on every node, then delete the old token in Cloudflare; update `CLOUDFLARE_API_TOKEN` in `tofu.enc.env` too if the same token serves OpenTofu |
| `object_storage_access_key`, `object_storage_secret_key` | the local converge's S3 stand-in only | n/a | Azure backups use the core nodes' managed identity: there is no storage key to rotate. Revoking access is removing the role assignment (`infra/tofu/modules/backup-storage`) |
| `alertmanager_discord_webhook_url`, `gatus_discord_webhook_url` | Alertmanager, Gatus (optional Discord receiver) | Alertmanager: yes (observability play of `site.yml`); Gatus: render the probe bundle again and restart it ([../deploy/probe.md](../deploy/probe.md)) | create a new webhook in Discord, delete the old one. Without the key there is no receiver ([alerts.md](alerts.md), "Adding a receiver") |
| `ghcr_pull_token` | every node and the controller sign in to `ghcr.io` with it to pull the platform images when `sa_registry_login` is set in the inventory's `group_vars/all.yml` | yes: the `secrets` role signs each node in again when the stored credential differs (`roles/secrets/tasks/registry.yml`), and the controller writes it into a temporary docker config for the signature check | a classic GitHub token of the account named in `sa_registry_login_user`, with the single scope `read:packages`. GitHub shows its expiry date on the token's settings page; create the new token before that date, put it in with `sops edit`, run `site.yml` (or `deploy.yml`), then delete the old token in GitHub. When the image packages are public, no token is needed: set `sa_registry_login: ""`, delete the token in GitHub and remove the key; `docker logout ghcr.io` on each node removes the stored credential |
| `cosign_private_key`, `cosign_password` | recovery copy of the release key pair; the release workflow reads the GitHub Actions secrets `COSIGN_PRIVATE_KEY` and `COSIGN_PASSWORD` | n/a | a new pair: `cosign generate-key-pair` with a new password, set both Actions secrets (`gh secret set COSIGN_PRIVATE_KEY` and `gh secret set COSIGN_PASSWORD`), put both into the sops file, commit the new public key as `infra/ansible/vars/release-cosign.pub`, cut a new release and deploy it. Releases signed with the old key no longer verify, so roll back only to releases signed after the change |
| `grafana_admin_password`, `postgres_exporter_password` | Grafana, postgres_exporter on the core nodes | yes (observability play; the exporter's `sa_monitor` role password is set through HAProxy at each run) | recreate `grafana` and the exporter if they did not restart. Grafana reads `GF_SECURITY_ADMIN_PASSWORD` only when it creates its database, and its database is on the persistent `grafana` volume, so on every core node also set the stored password (below, "The Grafana admin password") |
| `probe_metrics_token`, `gatus_external_token` | the probe node's Caddy and Gatus, Prometheus scraping the prober | core side yes; probe side: render the probe bundle again and restart it | |
| `probe_otlp_token`, `probe_machine_api_key`, `probe_site_id` | the prober | render the probe bundle again and restart it | create a new OTLP token for the internal probe tenant and a new scoped operator key, write them to the file, apply, then revoke the old ones |

The certificates of the private network are not in `secrets.enc.yaml`: they live in
`ca.enc.yaml` and `tls.enc.yaml` beside it and have their own section,
[Internal CA and node certificates](#internal-ca-and-node-certificates).

Keys of `tofu.enc.env` (`CLOUDFLARE_API_TOKEN`; Azure uses the operator's `az login`, which
`az logout` ends) are only used on the controller: edit with
`sops --config infra/secrets/.sops.yaml edit infra/secrets/<env>/tofu.enc.env`, run
`make <env>-plan` to prove the new value works (expected: `No changes.`), and revoke the old
one at the provider.

`TF_VAR_state_passphrase` encrypts the state and every plan file through the `pbkdf2` key
provider and the `aes_gcm` method in the `encryption` block of
`infra/tofu/envs/<env>/versions.tf`, with `enforced = true`. The `pbkdf2` provider takes one
passphrase, so a new value needs a temporary rollover in that block, as described in
[OpenTofu's key and method rollover](https://opentofu.org/docs/language/state/encryption/):

1. Add a second variable for the old passphrase, a second `pbkdf2` key provider and a second
   `aes_gcm` method built from it. Keep the names of the existing provider and method: the
   state metadata refers to them.
2. Point `state` and `plan` at the method of the new passphrase, and give each a
   `fallback { method = <the old method> }` block. OpenTofu reads with the fallback when the new
   method fails and always writes with the new one.
3. Put the new value in `tofu.enc.env`, pass the old one to the new variable for this run only,
   and run `make <env>-apply`. Expected: the apply writes the state encrypted with the new
   passphrase.
4. Remove the fallback, the old provider, method and variable, delete any plan file saved
   before the change, and run `make <env>-plan`. Expected: `No changes.` with no fallback in
   the configuration.

Earlier state versions kept by blob versioning stay encrypted with the old passphrase
([state backend](../deploy/opentofu.md#state-backend)); keep the old value until those versions
expire.

## Procedures for the extra steps

### A NATS user password and its hash

```sh
~/.local/share/veristar/ansible-venv/bin/python -c '
import bcrypt, secrets
p = secrets.token_hex(24)
print(p)
print(bcrypt.hashpw(p.encode(), bcrypt.gensalt(rounds=11, prefix=b"2a")).decode())'
```

The first line goes to `nats_passwords.<user>`, the second to `nats_password_hashes.<user>`
(the same cost and prefix `new-secrets.py` uses).

### A PostgreSQL role password (Umami, superuser, replication)

On the database leader, inside psql, `\password` sends only a SCRAM verifier, so the password
never appears in logs or history:

```sh
cd /opt/veristar/core && sudo docker compose exec postgres psql -U postgres -d postgres
```

```
\password umami_app
```

(`postgres` for the superuser, `replicator` for replication.) Then put the same value in the SOPS
file and converge. For the superuser and replication passwords also reload Patroni on every core
node after the converge, which is what the core role's handler does:

```sh
sudo docker compose kill --signal HUP postgres
```

and check with `patronictl list` that the replica is still `streaming`.

### The Umami admin password

On a core node, with the current and the new password typed in (not echoed):

```sh
read -rs OLD_PW; read -rs NEW_PW; export OLD_PW NEW_PW
cd /opt/veristar/core
sudo --preserve-env=OLD_PW,NEW_PW docker compose exec -T -e OLD_PW -e NEW_PW umami node -e '
const b = "http://127.0.0.1:3000";
(async () => {
  const l = await fetch(b + "/api/auth/login", {method: "POST", headers: {"content-type": "application/json"},
    body: JSON.stringify({username: "admin", password: process.env.OLD_PW})});
  if (!l.ok) throw new Error("login " + l.status);
  const {token} = await l.json();
  const c = await fetch(b + "/api/me/password", {method: "POST",
    headers: {"content-type": "application/json", authorization: "Bearer " + token},
    body: JSON.stringify({currentPassword: process.env.OLD_PW, newPassword: process.env.NEW_PW})});
  console.log("change password", c.status);
  if (!c.ok) process.exit(1);
})().catch((e) => { console.error(String(e)); process.exit(1); });'
unset OLD_PW NEW_PW
```

Expected: `change password 200`. Then write the new value to `umami_admin_password`, converge,
and recreate `controlplane` and `jobs` on every core node.

### The Grafana admin password

After the converge, set the stored password from the SOPS file on every core node. The value
travels over SSH standard input and never appears on a command line:

```sh
sops --config infra/secrets/.sops.yaml decrypt --extract '["grafana_admin_password"]' \
  infra/secrets/<env>/secrets.enc.yaml |
  ssh ops@<node> 'cd /opt/veristar/observability && sudo docker compose exec -T grafana \
    grafana cli admin reset-admin-password --password-from-stdin'
```

Expected: `Admin password changed successfully`. Check it with a login through Grafana's API on
the node (`curl -u admin:<new password> http://127.0.0.1:3000/api/user` answers 200, the old
password 401).

### The Umami user password key

After the converge and the recreation of `controlplane` and `jobs`, Umami still holds today's
passwords derived from the old key, and `umami-rotate` skips a day it already recorded. Clear the
record and run the task once (the [runbooks overview](overview.md), "Running a jobs task by hand"):

```sql
delete from cp.settings where key = 'umami_password_day';
```

```sh
cd /opt/veristar/core && sudo docker compose run --rm --no-deps jobs run umami-rotate
```

Every Umami session token is invalidated by the password change; users re-enter through the
console's SSO.

## Internal CA and node certificates

Every link between nodes is TLS with certificates of one internal CA per environment. The files:

| File | Holds | Read by |
|---|---|---|
| `infra/secrets/<env>/ca.enc.yaml` | `ca_key`, `ca_cert`, `previous_ca_certs` (retired CAs still trusted) | `playbooks/internal-ca.yml` on the controller only. No role loads it, so the CA key never reaches a node |
| `infra/secrets/<env>/tls.enc.yaml` | `ca_bundle` (current and still-trusted CAs), `nodes.<name>.cert` and `.key` for every edge, core, backup and restore node, `operator.cert` and `.key` (client certificate for operators and the drills) | the `secrets` role, which installs each node's own pair under `/etc/veristar/tls/<container user>/` |

`make internal-ca ENV=<env>` (`playbooks/internal-ca.yml`, community.crypto 3.5.1 from the pinned
Ansible 14.6.0) issues only what is missing or due: a node certificate is issued again when the
node is new, its names changed (node name, private address), or it is valid for less than 30
more days. Keys are generated in the playbook's memory and written only through SOPS. A second
run reports `0 certificate(s) issued`. Node certificates last 365 days, the CA 10 years.

Each container that reads a certificate carries the label `sa.tls` (a hash of the node's
certificate and the CA bundle), so `site.yml` recreates exactly those containers when either
changes. A converge after a reissue therefore restarts NATS, etcd, PostgreSQL (Patroni), HAProxy,
the relay, the exporters, Prometheus, Alertmanager and the services on each node; deploy with
`deploy.yml` (one node at a time, drained, the Patroni leader switched over first) rather than
`site.yml` in production.

**Renew node certificates** (yearly, or when the `make internal-ca` report shows certificates
due; also after adding a node or changing a private address):

```sh
make internal-ca ENV=<env>
make <env>-deploy ROLLING=1 SA_IMAGE_TAG=<running release> SA_RELEASE_MANIFEST=<its release-images.json>
```

Expected: `N certificate(s) issued, ca unchanged`, then every node healthy in turn. Peers keep
working through the rollout because the CA is unchanged.

**Rotate the CA** (the CA key may have been exposed, or before it expires). Two converges, so no
link ever meets a certificate its peer does not trust:

1. `make internal-ca ENV=<env> CA_ROTATE=new`: a new CA signs every node certificate again;
   the old CA certificate moves to `previous_ca_certs` and stays in `ca_bundle`. Expected:
   `N certificate(s) issued, ca new, 1 retired ca(s) still trusted`.
2. Roll it out (`make <env>-deploy ROLLING=1 ...`). During the rollout, nodes with old
   certificates and nodes with new ones trust each other through the bundle.
3. `make internal-ca ENV=<env> CA_ROTATE=retire`: drops the retired CAs from the bundle
   (`ca unchanged, 0 retired ca(s) still trusted`), and roll out again.

If the old CA key was exposed, do step 3 straight after step 2: until then a certificate the
attacker signed with the old key is still trusted.

**Remove a node**: delete it from the inventory and run `make internal-ca`; its certificate
leaves `tls.enc.yaml`. Its certificate is still valid until it expires, and nothing revokes it
(there is no CRL), so after a node was compromised rotate the CA as above.

**The operators' certificate** (`operator.cert`, CN `sa-<env>-operator`) is renewed with the node
certificates. Extract it only into a private directory on your own machine, for example to read
Patroni or NATS monitoring through an SSH tunnel, and delete it afterwards:

```sh
umask 077; mkdir -p ~/sa-tls
for k in ca_bundle:ca.crt operator.cert:client.crt operator.key:client.key; do
  sops decrypt --extract "$(echo "${k%%:*}" | sed 's/\./"]["/; s/^/["/; s/$/"]/')" \
    infra/secrets/<env>/tls.enc.yaml > ~/sa-tls/"${k#*:}"
done
```

## Exercised

| Date | What ran | Result |
|---|---|---|
| 2026-11-13 | The platform role rotation path on a scratch PostgreSQL 18.6 (`platformctl db migrate`, `db roles` with one password, then `db roles` with a new one, logins tried over TCP) | `application role logins set and verified` both times; after rotation the old password was refused (`password authentication failed for user "cp_system"`), the new one logged in |
| 2026-11-13 | The Umami admin password command above, on a throwaway Umami 3.5.0 | seeded to first: `change password 200`; first to second: `change password 200`; old password `login 401`, new password `login 200` |
| 2026-11-13 | `make internal-ca ENV=local` on the local converge's inventory, first run then second run | first run: CA and 8 certificates issued; second run `0 certificate(s) issued, ca unchanged`, `changed=0` |
