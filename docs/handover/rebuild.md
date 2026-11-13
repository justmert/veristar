---
title: "Rebuild and data migration"
sidebarTitle: "Rebuild and data migration"
description: "How a new operator builds its own environment, moves every tenant's data, takes over the domain and rotates credentials."
---

How a new operator builds its own environment from this repository, moves every tenant's data
into it, takes over the domain and rotates every credential. This is the technical half of the
[handover plan](plan.md); the [checklist](checklist.md) lists every asset that has to change
hands.

## Status on 2026-11-13

| Part | State |
|---|---|
| Migration tooling (`make migrate-export`, `make migrate-import`, `platformctl verify-migration`) | built and run locally between two separate environments, by both paths (section 8) |
| `rotate-all.yml` | built and run on the minimal local converge: login checks passed, Patroni reload fault found and fixed, the confirming re-run not completed (plan.md section 8) |
| Domain cutover playbooks | built; the redirect Caddyfile is tested live, the playbooks have never run, because they need a public domain (inputs I4, I6, I12) |
| Independent rebuild on a second cloud account and domain, by someone with only the repository | **not done**: blocked on an operator input (a second cloud project, a spare domain, a second GitHub owner). Nothing here simulates it |
| Repository transfer rehearsal | **not done**: needs the second GitHub owner of I12 |

## 1. Build the new environment

Follow [../deploy/overview.md](../deploy/overview.md) steps 1 to 6 with the new operator's own
accounts and secrets. The new environment gets its own values for everything: database
passwords, NATS users, the Umami admin password, `UMAMI_USER_PASSWORD_KEY`, `APP_SECRET`, the
OAuth app, the pgBackRest cipher passphrase. Nothing is copied from the old environment's
secrets file except what section 3 names for reading the old backup repository.

Build it with the **same release** (`SA_IMAGE_TAG`) the old environment runs. The import refuses
a target whose platform migration version or Umami migrations differ from the source's, because
restoring would silently change the target's schema.

`site.yml` bootstraps the new environment: both databases exist, the platform migrations and
role logins are applied, Umami has migrated its own database and the admin password is set.
That bootstrapped state is what the import replaces.

## 2. Freeze the source

During the cutover. The old edge nodes keep accepting events
the whole time; what they accept from now on waits in the old streams and reaches the new
environment through `platformctl queue forward`.

1. Stop `replayer` and `metricswriter` on every old core node, so nothing more is written from
   the queues into the old databases. What the edges accept from here on stays in
   `WEB_EVENTS`, `API_METRICS` and `EDGE_STATS`.
2. Run `jobs run api-rollup` and `jobs run aggregate-web` once more, or wait for the daemon's
   next pass, so hourly and daily rows include the last minutes.
3. Stop `jobs`, `controlplane` and every Umami process on every old core node.
   `scripts/migrate.sh` refuses to export while any connection to `umami` or `platform` is
   open, and names the connections it found. Do **not** stop the old edge services: the plan
   drains them only after the delegation moved.

## 3. Export

Both paths produce the same thing: a directory with `platform.dump`, `umami.dump` (PostgreSQL
custom format), `SHA256SUMS` and `manifest.env` (schema versions, the roles the dumps reference,
times).

Every command reads an env file naming one server and an admin login:

```sh
PLATFORM_DB_HOST=<address>
PLATFORM_DB_PORT=5432
PLATFORM_DB_NAME=platform
# a converged environment admits tls only: verify-full with
# that environment's ca bundle (ca_bundle of infra/secrets/<env>/tls.enc.yaml)
PLATFORM_DB_SSLMODE=verify-full
PLATFORM_DB_SSLROOTCERT=<path to the ca bundle>
PLATFORM_DB_ADMIN_USER=postgres
PLATFORM_DB_ADMIN_PASSWORD=<superuser password of that server>
UMAMI_DB_NAME=umami
# verify-migration also reads these two
UMAMI_URL=http://<umami>:3000
UMAMI_ADMIN_PASSWORD=<umami admin password of that environment>
```

The PostgreSQL client tools run from the pinned server image (`MIGRATE_PG_IMAGE`), so `pg_dump`
is never older than the server. `MIGRATE_DOCKER_NETWORK` (default `host`) is the network that
container joins; on a core node, the compose network `veristar-core_default` reaches
`haproxy:5000`.

### 3.1 The pgBackRest path (same provider, the default)

The old environment has backed up continuously to its repository (roles/backup). After the
freeze, take one more full backup on the old Patroni leader and note its label:

```sh
sudo docker compose -f /opt/veristar/core/compose.yml exec postgres \
  pgbackrest --stanza=sa --type=full backup
sudo docker compose -f /opt/veristar/core/compose.yml exec postgres \
  pgbackrest --stanza=sa --output=json info      # backup[-1].label, e.g. 20261113-060059F
```

Then, in the **new** environment, restore that backup set onto its restore node, reading the old
repository, and export from the restored standalone server:

```sh
make migrate-export METHOD=pgbackrest ENV=<new env> RESTORE_SET=<label> \
  RESTORE_EXTRA='-e {"restore_source": {"environment": "<old env>",
    "object_storage": {"endpoint": "https://...", "region": "...", "backup_bucket": "..."},
    "secrets_file": "<path to the old secrets.enc.yaml>"}}' \
  MIGRATE_ENV=<env file of the restore node> MIGRATE_DIR=<new directory>
```

`restore.yml` takes only the object storage keys and the repository cipher passphrase from the
old secrets file; the operator's age identity must be one of its recipients during the shadow
period. The restored server is a physical copy, so its logins are the old environment's: the
env file of the restore node carries the old superuser password. It is never a Patroni member
and has WAL archiving off, so it cannot write into either repository.

`restore_source` needs object storage an operator input and has **never run**: the local converge has a
POSIX repository on a backup node, not object storage. What did run is the same chain with
pgBackRest commands in containers (section 8).

### 3.2 The pg_dump path (to a different provider)

When the new environment cannot read the old repository, export straight from the frozen old
leader:

```sh
make migrate-export METHOD=dump MIGRATE_ENV=<env file of the old leader> MIGRATE_DIR=<new directory>
```

Copy the directory to the controller of the new environment. It holds every tenant's data:
move it only over an encrypted channel, keep it on encrypted storage, and delete it after
section 6 passed.

## 4. Import

On the new environment, stop `umami`, `controlplane`, `jobs`, `replayer` and `metricswriter` on
every core node, then:

```sh
make migrate-import MIGRATE_ENV=<env file of the new leader> MIGRATE_DIR=<directory>
```

It checks the checksums, the schema versions and that every role the dumps reference exists,
drops `platform` and `umami`, restores each with `pg_restore --create` (owners, privileges and
database settings exactly as in the source), then runs `platformctl db migrate` (no migration to
apply, and the row-level security coverage check) and `jobs run reconcile`, which rebuilds the
NATS registries (`registry_sites`, `registry_otlp`) from the `cp` tables. The env file must
therefore also hold what `platformctl db migrate` and `jobs` read (the `PLATFORM_DB_*_PASSWORD`
keys, `NATS_*`, `UMAMI_URL`, `UMAMI_ADMIN_PASSWORD`, `UMAMI_USER_PASSWORD_KEY`, the API retention
keys); start `umami` before the reconcile.

### 4.1 Where the import runs

`make migrate-import` builds `platformctl` and `jobs` on the machine that runs `make` (the
controller, with Go 1.27.4 and Docker) and connects them, and the `pg_restore` container of
`scripts/migrate.sh`, straight to PostgreSQL, NATS and Umami. The controller is outside the
private network, and the cloud and host firewalls close 5432, 4222 and 3000 to it on purpose
([../deploy/verification.md](../deploy/verification.md) step 7). Do not open them. Reach the
three services through one SSH session to the new environment's Patroni leader instead:

```sh
# on the leader: the umami container's address on the core compose network
# (umami is not published on any host port)
ssh ops@<leader public address> \
  "sudo docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' veristar-core-umami-1"

# on the controller, kept open for the whole import
ssh -N -L 15432:<leader private address>:5432 \
       -L 14222:<edge-1 private address>:4222 \
       -L 13000:<umami container address>:3000 \
       ops@<leader public address>
```

PostgreSQL is reached on the leader's private address, port 5432, not through HAProxy: HAProxy's
port 5000 exists only inside each core node's compose network. The leader's `pg_hba` admits the
private network, which is where the forwarded connection comes from. The SSH session encrypts
the traffic, but the leader's `pg_hba` admits TLS only (`hostssl`), so the
connection still uses `verify-full` with the new environment's CA bundle; its certificate names
`127.0.0.1`, which is the tunnel's end. `scripts/migrate.sh` runs its
client container on the host network by default, so `127.0.0.1` is the tunnel.

The env file for the import, with every value taken from the new environment (the passwords
are those in `/etc/veristar/env/platformctl.env` and `jobs.env` on the leader, or in
its SOPS file):

```sh
PLATFORM_DB_HOST=127.0.0.1
PLATFORM_DB_PORT=15432
PLATFORM_DB_NAME=platform
PLATFORM_DB_SSLMODE=verify-full
PLATFORM_DB_SSLROOTCERT=<path to the new environment's ca bundle>
PLATFORM_DB_ADMIN_USER=postgres
PLATFORM_DB_ADMIN_PASSWORD=<postgres superuser password>
UMAMI_DB_NAME=umami
PLATFORM_DB_JOBS_PASSWORD=<jobs role password>
PLATFORM_DB_CP_SYSTEM_PASSWORD=<cp_system role password>
NATS_URL=nats://127.0.0.1:14222
NATS_REPLICAS=3
NATS_USER=jobs
NATS_PASSWORD=<jobs nats password>
UMAMI_URL=http://127.0.0.1:13000
UMAMI_ADMIN_PASSWORD=<umami admin password>
UMAMI_USER_PASSWORD_KEY=<umami user password key>
API_MINUTE_RETENTION_DAYS=30
API_HOURLY_RETENTION_MONTHS=36
```

The retention values are the core role defaults (`core_api_minute_retention_days`,
`core_api_hourly_retention_months`); use the inventory's values if it overrides them. NATS
answers the client with the other edge members' private addresses, which the controller cannot
reach; the one forwarded member is enough for `jobs run reconcile`.

Because Umami must be running for the reconcile and stopped for the restore, run the three
steps of `make migrate-import` one by one rather than the target itself:

```sh
make build
scripts/migrate.sh import <env file> <directory>
# on the leader: start umami (process 1 only), then read its address again,
# because a recreated container can get a new one
set -a && . ./<env file> && set +a
dist/bin/platformctl db migrate && dist/bin/jobs run reconcile
```

Keep the env file on encrypted storage and delete it with the dump directory. This tunnel path
has **not been rehearsed**: the local rehearsal (section 8) ran with every service on one host.
Rehearsing it on staging is blocked on an operator input (and I4, I6 for the environment itself).

The imported Umami database still holds the **old** Umami admin password and user passwords.
Re-key it to the new environment's values before anything else uses Umami:

```sh
# on the new leader, with the new admin password and the old one
platformctl umami rotate-admin        # UMAMI_ADMIN_PASSWORD_PREVIOUS -> UMAMI_ADMIN_PASSWORD
jobs run umami-rotate --force         # every console user's password for today, new key
```

`rotate-admin` proves the previous password is refused afterwards. `umami-rotate --force` sets
today's passwords even though the imported database records today's rotation as done.

### What is not carried over, on purpose

- **Memory buckets.** Queue contents are not exported with the databases; they move through
  `platformctl queue forward` with their message ids. `edge_secrets`,
  `visit_state` and `locks` are memory state of the old NATS cluster: the new cluster creates
  new daily secrets, so a visitor active across the cutover is counted again on that UTC day,
  and an open visit splits.
- **Umami's `APP_SECRET`.** The new environment keeps its own. Umami signs its cache tokens with
  it and derives session and visit ids from it ([../reference/umami-contract.md](../reference/umami-contract.md)
  item 14). Stored rows do not depend on it: the local run imported with a different
  `APP_SECRET` and every `/stats` answer was equal. Keeping the old value would leave a value
  the original operator knows in the new system and would save nothing, because new daily
  visitor secrets already change every session id on the cutover day. Old Umami tokens stop
  working, which the daily password change does anyway.
- **Leader lock fencing.** `cp.leader_fence` holds revisions of the old cluster's lock bucket;
  the new jobs leader writes its own. `verify-migration` shows it and does not compare it.

## 5. Verify

With the old Umami running again read-only (it writes nothing without traffic), compare:

```sh
make verify-migration SOURCE_ENV=<old leader env file> TARGET_ENV=<new leader env file>
```

It refuses environments with different schema versions, then compares row counts of every table
in both databases, per-tenant daily web aggregates (`agg.web_daily` and `agg.web_daily_dim` by
tenant, day and band), per-tenant daily API aggregates (`api.metrics_hourly` and
`api.metrics_daily` by tenant, day and source: rows, requests, latency sum and every histogram
bucket), and Umami's own `/stats` for up to 10 sites (`--sites`) and their 7 most recent days with
events (`--days`), primary and quarantine websites both. It prints every difference and exits 1
on any. Run it right after section 4's re-keying: `umami-rotate --force` and the reconcile write
audit rows on purpose, so a run after them reports those rows.

Then sign in to the new console and open a project's API traffic page and its web dashboard: the
history must be there.

## 6. Domain

Transfer the domain ([plan.md](plan.md) section 5, [checklist.md](checklist.md) section 3). It is
the only path that keeps existing snippets working
([redirect-research.md](redirect-research.md)):

```sh
cd infra/ansible
ansible-playbook -i inventories/<new env>/ playbooks/cutover-transfer.yml \
  -e cutover_stage=precheck -e '{"cutover_nameservers": ["<ns1>", "<ns2>"]}'
# registrar transfer
ansible-playbook -i inventories/<new env>/ playbooks/cutover-transfer.yml \
  -e cutover_stage=verify -e '{"cutover_nameservers": ["<ns1>", "<ns2>"]}'
```

`precheck` asks the new zone's nameservers directly: every name must answer with exactly the new
nodes. `verify` asks public resolvers for the delegation and every name, fetches every pinned
tracker release over the real name with certificate validation and compares the bytes with the
release files, and checks the console, the web dashboards and the OTLP endpoint.

If the domain cannot be transferred, `cutover-redirect.yml` turns the old edge nodes into
redirects to a new domain while they keep serving the tracker files. It refuses to run until
`cutover_redirect_breaks_snippets=true` is passed, because on that path existing snippets stop
delivering events and OTLP exporters lose their token; every tenant has to change its snippet
and exporter endpoint.

## 7. Rotate every credential

After the cutover the original operator still knows every value of the old environment, and for
a handover done in place (no rebuild) of the running one. In the new environment rotate anyway
if the original operator ever held its secrets file:

```sh
infra/secrets/rotate-secrets.py <env>             # new values, previous ones kept aside
cd infra/ansible
ansible-playbook -i inventories/<env>/ playbooks/rotate-all.yml -e sa_image_tag=<release> \
  -e rotate_previous_age_identity=<the removed operator's age identity, if available>
```

Remove the original operator's age key from `infra/secrets/recipients/<env>.txt` and run
`infra/secrets/sync-recipients.sh` before `rotate-secrets.py`, so the new file is encrypted to the
new list only. `rotate-all.yml` refuses a file in which a generated value did not change, applies
every value in the order a running system needs (PostgreSQL roles before the services that log
in with them, the Umami admin through Umami, the NATS config before the clients), recreates every
service, resets Grafana's stored admin password and re-keys every Umami user. Then it proves
every previous credential is refused and every new one accepted: all ten PostgreSQL logins
including replication, every NATS user, the NATS route password, the Umami admin and a sample of
console users' Umami passwords, the Patroni REST API, Grafana, and, where a GitHub app exists,
GitHub itself (`incorrect_client_credentials` for the previous OAuth client secret). A refusal
counts only when the system said the credential was wrong; an unreachable server is a failure,
never a pass.

Two kinds of value it does not generate:

- `pgbackrest_cipher_pass` stays: the repository is encrypted with it. A rebuilt environment has
  its own repository and value already; a handover in place needs a new repository
  ([../runbooks/secret-rotation.md](../runbooks/secret-rotation.md)).
- Owner inputs (GitHub client secret, Cloudflare token, Discord webhooks) come
  from their providers. Issue new ones, pass them to `rotate-secrets.py` in the `SA_*` variables
  `new-secrets.py` reads, and revoke the old ones at the provider. `rotate-all.yml` stops on any
  that kept its value unless it is named in `rotate_allow_kept_inputs`.

Operator API keys and OTLP tokens are stored only as hashes. Revoke the operator keys the
original operator created ([../runbooks/operator-key-revocation.md](../runbooks/operator-key-revocation.md));
OTLP tokens and snippets of tenants do not change.

## 8. Local rehearsal record

### Data migration (`make handover-it`, tests/handover)

Two separate environments on one machine, each its own Docker network, PostgreSQL, NATS, Umami,
control plane, jobs, replayer, gateway, collector, metrics writer and Caddies, with their own
secrets. Environment a archives WAL into an encrypted pgBackRest repository.

1. Two GitHub users (TEST-ONLY OAuth fixture) sign in to a, enroll a project each, add three
   sites, paste the console's snippet into the tenant site and load pages in Chromium through
   the edge Caddy and gateway; a Go example app per tenant sends API requests with a token made
   in the console.
2. The pageviews reach Umami, the requests reach `api.metrics_minute`, the hourly rollup and the
   daily web aggregates are written; then a is drained and stopped.
3. pgBackRest path: full backup of a, restore of that set in b's network, `migrate.sh export`
   from the restored server, `migrate.sh import` into b, `platformctl db migrate`,
   `umami rotate-admin`, then `verify-migration` against a.
4. pg_dump path: `migrate.sh export` from the stopped a, import over b's first copy, re-keying,
   `verify-migration` again.
5. a is removed. `jobs run umami-rotate --force` and `jobs run reconcile` on b, b's services
   start, the names move to b (the transfer path).
6. The same users sign in to b: their project, sites, API requests and Umami pageviews per day
   are there. The snippet pasted before the migration delivers a pageview into b from Chromium,
   Firefox and WebKit.

The run's output, timings and the `verify-migration` reports are in
`.scratch/scratch/handover/` on the machine that ran it; the outcome is recorded in
[plan.md](plan.md) section 8.

### Credential rotation (`make rotate-local`, tests/handover/rotate-local.sh)

The minimal local converge (3 edge, 1 core, 1 backup node, `LC_TOPOLOGY=minimal`), converged and
checked end to end; a second age identity for the new operator; `rotate-secrets.py` encrypting
the new and previous values to that identity only; `rotate-all.yml`; the end-to-end check again;
teardown. Outcome in [plan.md](plan.md) section 8: the login proofs passed, a Patroni config
reload fault was found and fixed, and the confirming re-run did not complete on the overloaded
machine.
