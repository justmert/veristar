# Handover plan

> **Status (2026-11-13): tooling built and rehearsed locally; the independent rehearsal is
> blocked.** The migration tooling, `rotate-all.yml` and the domain cutover playbooks exist
> ([rebuild.md](rebuild.md)), and the data migration, the cutover by transfer and the credential
> rotation ran end to end on this repository's local stand-ins (section 8). The rehearsal the
> plan requires, in which someone with only this repository rebuilds the system in a second cloud
> account and domain and transfers a repository to a second GitHub owner, needs an operator input and has
> not happened. Until it has, this plan is final in its decisions but not proven on real
> infrastructure. The asset-by-asset list is [checklist.md](checklist.md).

How stewardship moves from the original operator (the proposer) to a community-appointed
operator. The RFP sets the frame (RFP 3.4):

- Handover happens between month 6 and month 12 of the 12-month service window, at SCF's
  discretion. After 12 months the original operator has no further obligations.
- Handover is complete only when the new operator is independently running a fully working
  system. Mandatory: transfer of repository ownership, full data migration, and either transfer
  of domain ownership or a permanent redirect from the old domain.
- Infrastructure can be rebuilt from the open-source monorepo.

## 1. What is handed over

| Asset | Today | Moves by |
|---|---|---|
| Repository `github.com/justmert/stellar-analytics` (code, IaC, docs, issues, releases) | personal account of the proposer | GitHub repository transfer to the new operator's owner |
| Domain `BASE_DOMAIN` an operator input, not yet chosen) | ideally registered to SCF from day one, which makes this step a permission change | registrar transfer, or permanent redirect (section 5) |
| Cloud project (Hetzner: servers, private network, object storage with backups) | an operator input | rebuild in the new operator's account from `infra/tofu`, then data migration |
| DNS zone (bunny.net) | an operator input | recreated from `infra/tofu/modules/dns` in the new account |
| Probe server and status page (Scaleway) | an operator input | rebuilt from `infra/tofu/modules/probe` |
| Secrets (`infra/secrets/<env>/secrets.enc.yaml`, SOPS with age) | encrypted to the age keys in `infra/secrets/recipients/<env>.txt` an operator input | new operator's keys added, then every value rotated, then the old keys removed (section 4) |
| GitHub OAuth apps for console sign-in an operator input | owned by the proposer | new apps under the new owner, new client ids and secrets |
| Operator list `OPERATOR_GITHUB_IDS` an operator input | SCF's operators | unchanged unless SCF changes it |
| npm scope for the tracker package an operator input | not yet set up | scope ownership transfer, trusted publishing reconfigured for the new repository |
| Alert receiver an operator input | not yet set up | new webhook owned by the new operator |
| Container images and tracker release files | built by `release-images.yml` and `release-tracker.yml` | rebuilt from the repository; image paths follow the new owner |
| Support (public issue tracker) | answered by the original operator ([../support.md](../support.md)) | moves with the repository |
| Tenant agreements (`docs/compliance/terms.md`, `dpa-template.md`) | the operator is the tenants' processor | processor change notice to every tenant (section 3, step 1) |

What a repository transfer does not carry, and must be set up again: Actions secrets and
variables, environments, the npm trusted publisher setting, branch protection, Dependabot
settings, and container registry paths that include the operator's name
([checklist.md](checklist.md) section 6). The list is from GitHub's documentation; the transfer
rehearsal with a throwaway repository that confirms it needs the second GitHub owner of input
I12 and has not run.

## 2. Phases of the handover

### 2.1 Appointment and preparation (before the shadow period)

1. SCF names the community operator and the date the handover starts.
2. The new operator reads the repository's documentation: [../architecture.md](../architecture.md),
   [../deploy/](../deploy/), [../runbooks/](../runbooks/), [../sla.md](../sla.md),
   [../support.md](../support.md) and this plan.
3. The new operator creates its own accounts: a GitHub owner, a cloud project, a DNS account, a
   probe account, an alert receiver, and age identities for at least two people.

### 2.2 Shadow period (at least 4 weeks, proposed)

The original operator still runs production. The new operator works alongside with read access
and no write access to production, and builds its own copy.

- The new operator's people are added as repository collaborators and as age recipients of
  **staging** only.
- The new operator answers support issues, with the original operator reviewing, so the
  5 and 10 business day targets are kept by the people who will own them.
- The new operator builds a complete environment in its own cloud account from the repository
  alone (independent rebuild), following [../deploy/](../deploy/). Every point where it needed
  knowledge not in the repository is fixed in the docs.
- The new operator runs the runbooks that can run without production write access on its own
  environment: edge node loss, database failover, point-in-time restore, Umami upgrade.
- The 4-week length is a proposal, long enough to see one monthly SLO report and one daily
  secret rotation cycle in the new environment; SCF decides the length.

Exit condition: the new operator's environment passes the end-to-end suites and the
readiness checklist, and SCF agrees that the cutover can start.

### 2.3 Cutover (one announced maintenance window)

1. **Tenant notice.** Ahead of the cutover every project is told the date, the new
   processor, and that nothing changes in its snippet if the domain moves (section 5). The
   processing terms require advance notice of a processor or sub-processor change; the length
   of that notice is open for the legal review (`docs/compliance/dpa-template.md`, an operator input.
2. **Data migration** ([rebuild.md](rebuild.md) sections 2 to 5): ingestion is drained and the
   source stopped; both PostgreSQL databases (`umami` and `platform`) are restored in the new
   environment from the source's pgBackRest backup set and copied into the new cluster
   (`make migrate-export METHOD=pgbackrest`, `make migrate-import`), or dumped with `pg_dump`
   when the providers differ (`METHOD=dump`). Queue contents are not migrated; `jobs run
   reconcile` rebuilds the NATS registries from the `cp` tables. `platformctl verify-migration`
   compares row counts of every table, per-tenant daily web and API aggregates, and Umami
   `/stats` for a sample of sites and days, and exits non-zero on any difference.
3. **Permission transfer** (section 3).
4. **Domain**: transfer or redirect (section 5).
5. **Credential rotation** (section 4).
6. The new operator's probe and status page measure the service; the first SLO report under
   the new operator covers the month after cutover.

### 2.4 Removal of the original operator

After the cutover is confirmed working:

- The original operator's age keys are removed from every environment's recipients, and
  every value they could read is rotated (section 4).
- Their GitHub access to the repository, their cloud, DNS, probe and registrar access, their
  alert receiver and any operator API keys they created are removed or revoked.
- Their GitHub id is removed from `OPERATOR_GITHUB_IDS` if it is there, and the control plane
  is restarted so the change takes effect at once ([../operators/README.md](../operators/README.md)).
- The old environment is torn down after the advisory tail (section 6) or when SCF says so,
  and its backups are deleted at the end of their retention. Tenants are told when.

Rehearsal: after removal, every old credential is tried and must be refused, and every
service must be healthy.

### 2.5 Advisory tail

Until the end of the 12-month window, the original operator answers the new operator's
questions through the repository's issue tracker, with no access to production. After the
window ends there is no obligation (RFP 3.4).

## 3. Permission transfer

In this order, so that at every step someone can still act:

1. Repository: transfer to the new owner. Issues, pull requests and releases move with it,
   and GitHub redirects the old path; set up again what does not move (section 1).
2. Cloud, DNS and probe accounts: the new environment already runs in the new operator's own
   accounts, so nothing moves; the old accounts are closed after teardown.
3. OAuth apps: the new environment uses the new operator's apps from the start.
4. npm scope and trusted publishing: transferred, then a tracker release is published from the
   new repository to prove it.
5. Alerts: the new receiver is live before the old one is removed.

## 4. Credential rotation

Every secret the original operator could read is replaced, with `infra/secrets/rotate-secrets.py`
and `infra/ansible/playbooks/rotate-all.yml` ([rebuild.md](rebuild.md) section 7): every
generated value (database roles including the superuser and replication, NATS users and routes,
the Umami database, admin and user password key, `APP_SECRET`, the OAuth state key, Patroni,
Grafana, exporter and probe tokens), re-encryption to the new age recipients, and proof that
every previous credential is refused. Owner inputs (GitHub OAuth secret, bunny.net, object
storage, Discord) are replaced at their providers and passed in; the playbook stops on any that
kept its value, and asks GitHub whether the previous OAuth client secret is refused. The
pgBackRest cipher passphrase is the exception: a rebuilt environment has its own, an in-place
handover needs a new repository.

Effects that tenants notice:

- Rotating `UMAMI_USER_PASSWORD_KEY` changes every derived Umami password at once, so every
  open web dashboard session ends and users re-enter through the console.
- Umami's `APP_SECRET`: the new environment uses its own and does not take the old one over.
  Umami signs its tokens with it and hashes session and visit ids with it; stored data does not
  depend on it (the local migration imported into an environment with a different value and
  every `/stats` answer was equal). On the cutover day a returning visitor is counted again and
  an open visit splits, which the new environment's new daily visitor secrets cause anyway.
- OTLP tokens and tracker snippets do not change, so tenants change nothing in their code.
- Operator API keys of machine consumers are revoked and re-issued only if the original
  operator could have read them; keys are stored only as hashes, so a key the original
  operator never saw stays valid.

## 5. Domain: transfer, not redirect

Every tenant page references the tracker by a pinned URL with an integrity hash and posts to
`/v1/e` on `in.<BASE_DOMAIN>`. Tenant applications export to `otlp.<BASE_DOMAIN>`.

**Domain transfer is the only safe option.** It was tested
([redirect-research.md](redirect-research.md), 2026-11-13, Chromium 156, Firefox 157, WebKit 27.2):
a script loaded through a cross-origin 308 passes its integrity check, and a `fetch` POST with
`keepalive` follows a cross-origin 308 with its body intact, but only when the redirect response
itself allows the origin (`Access-Control-Allow-Origin`); a plain redirect fails both in every
engine. Even with that header the released tracker delivers nothing: the redirected challenge
and event requests reach the new gateway with `Origin: null` (the Fetch standard's tainted
origin), which the gateway refuses, in all three engines. OTLP exporters built on Go's
`net/http` re-send a POST after a 308 but drop the `Authorization` header when the host changes,
so the tenant token is lost.

- **Transfer.** The zone is recreated from OpenTofu in the new DNS account, then the registrar
  transfer moves the domain; every name, snippet and endpoint stays as it is.
  `cutover-transfer.yml` checks the new zone before the transfer and the public delegation,
  every name, certificates and the tracker bytes after it. If SCF registered the domain from
  day one, the transfer is a permission change.
- **Redirect** (`cutover-redirect.yml`, `deploy/caddy/redirect.Caddyfile`): kept for a domain
  that cannot be transferred. The old edge nodes answer 308 to the new names with the CORS
  header and keep serving the tracker files. Existing snippets and exporters stop delivering;
  every tenant must change its snippet and OTLP endpoint first, and the playbook refuses to run
  until that is acknowledged.

## 6. If no operator is appointed

If SCF has not appointed an operator by the end of the 12-month window, the original operator
winds the service down in the open, on this schedule (the notice lengths are proposals; SCF and
the legal review, an operator input, set the final ones):

1. **Three months before the end of the window**, the original operator opens an issue on the
   repository and asks SCF in writing whether an operator will be appointed. Without an answer
   within 30 days, the wind-down below is announced.
2. **At least 60 days before the end**, every project is notified of the end date (an issue on
   the repository, SCF's own channels to awarded projects, and a mention of each project's
   members by their GitHub logins, which is the only contact the service stores; the console has
   no message feature today), that the
   hosted service stops accepting events and API metrics on that date, and that its data is then
   deleted. Tenants are told how to remove the snippet and the second OTLP exporter; a page that
   keeps the snippet only produces failed requests to a name that stops answering.
3. **Data export.** Until the end date each project can read its numbers through the web
   dashboards and the console. There is no bulk export feature. If SCF wants one, or wants a copy
   of the aggregates for itself, it says so with the answer in step 1 and it is built before the
   notice; what is handed over then follows the processing terms
   (`docs/compliance/dpa-template.md`).
4. **On the end date**: ingestion stops (edge nodes drained and stopped), the dashboards stay
   readable for 14 more days, then every service stops.
5. **Deletion**: both databases and every backup are deleted with the servers, the object
   storage buckets and the repository encryption passphrases, within 30 days of the end date, and
   the deletion is confirmed on the repository issue. Logs and metrics expire with their
   retention (at most 30 days).
6. **Accounts and domain**: cloud, DNS and probe accounts are closed. The domain is offered to
   SCF; if SCF declines it, it is kept registered and parked for at least 12 months, with no DNS
   records for `in.` and `otlp.`, so a third party cannot pick it up and receive the requests of
   pages and applications that were never cleaned up. Then it lapses.
7. **The repository stays public** under its licence with this plan, the IaC, the runbooks and a
   notice in the README that the hosted service ended, so anyone can rebuild the system from it
   ([rebuild.md](rebuild.md)). If SCF appoints an operator later, the handover starts from a
   rebuild with no data to migrate.

## 7. Done when

All of these are true and recorded in `docs/handover/rehearsal-<date>.md` (this stage) and,
for the real handover, in a closing issue on the repository:

- The new operator runs the system from its own accounts, built from the repository.
- `verify-migration` reports identical per-tenant aggregates and matching row counts, and
  tenants see their history.
- Existing tenant snippets deliver events to the new environment in three browser engines.
- Every old credential is refused.
- The repository is owned by the new operator.
- The domain is transferred (section 5: the redirect path was tested and is not safe).

## 8. Local rehearsal record (2026-11-13)

What ran on one machine, with what it proves and what it does not. Commands and the procedure are
in [rebuild.md](rebuild.md) section 8.

### Data migration and cutover: `make handover-it`, passed

Environment a populated through the real pipeline (2 GitHub users through the TEST-ONLY OAuth
fixture, 2 projects, 3 sites with 9 pageviews in Chromium through Caddy, gateway, NATS, replayer
into Umami, 27 API requests from the Go example app through Caddy, collector, NATS, metrics
writer), drained and stopped, then moved into a separate environment b by both paths.
`verify-migration` after each path:

```
compared rows        75 keys
compared web         16 keys
compared web-dim     8 keys
compared api         8 keys
compared umami-stats 3 keys
not compared: platform.cp.leader_fence source 2 target 2 (leader lock fencing revisions ...)
result: source and target are equal
```

Then, on b: both users signed in and saw their projects, sites, API requests and Umami
pageviews; after the names moved to b (the transfer path), the snippet pasted before the
migration delivered pageviews into b from Chromium, Firefox and WebKit (3 before, 6 after).

| Stage | Time |
|---|---|
| pgBackRest full backup of a | 3 s |
| restore of that set in b, export from the restored server | 5 s |
| import into b (restore, schema check, umami admin re-key) | 26 s |
| pg_dump export and import | 29 s |
| `verify-migration` | 1 s each |
| `umami-rotate --force` and `reconcile` | 2 s |

One Chromium attempt of the populate step failed on `ERR_NETWORK_CHANGED` (other Docker work on
the shared machine) and passed on retry; the run fails on any failed attempt without such an
abort. The volumes are tiny, so the times measure the procedure, not a production-sized copy.

Not proven by this run: `restore.yml` reading another environment's object storage repository
(needs I5), the cutover playbooks against a real domain and registrar, any of it
done by someone with only the repository on another account.

### Credential rotation: `rotate-all.yml` on the minimal local converge, partly passed

3 edge, 1 core and 1 backup node, converged and checked end to end (`make local-converge-e2e`
passed), then `rotate-secrets.py` re-encrypted 22 new values and the previous ones to a second
age identity only, and `rotate-all.yml` ran. Its proof plays reported:

- every previous credential refused and every new one accepted for all ten PostgreSQL logins
  (`postgres`, `replicator` over the replication protocol, `umami_app`, `jobs_retention`,
  `sa_monitor` and the five platform roles), all seven NATS users, the Umami admin and the
  console user `gh-1001` (previous key refused, new key accepted);
- the previous NATS route password refused on all three edge nodes;
- the removed age identity opened neither the new nor the previous secrets file.

It also found a real fault, now fixed in the core role but **not yet re-run**: Patroni's config
was a single-file bind mount, so the rewritten `patroni.yml` (an atomic rename) never reached the
container and the sighup reload re-read the old file. After the rotation Patroni still accepted
the previous REST password and kept logging in with the previous superuser password, which the
database already refused. `patroni.yml` now lives in its own directory, mounted whole. The same
pattern remains for `haproxy.cfg`, which holds no secret. Two more fixes the converge needed on
fresh nodes: `cron` installed for the pgBackRest textfile writer, and `/` made a shared mount in
the local node containers, as systemd does on a real server.

The re-run that would prove the Patroni fix, and the end-to-end check after rotation, did not
complete: the shared machine reached a load average above 450 with no free memory, the core node
stopped answering SSH, and the local converge was torn down. `make rotate-local` repeats the
whole run.
