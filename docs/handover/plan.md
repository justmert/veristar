# Handover plan (draft)

> **Status: draft.** Written in the design. It is rehearsed and finalised in this stage, which
> has not started. The migration tooling, the credential rotation playbook and the domain
> cutover playbooks named below as "to be built" do not exist yet. Every step marked
> "rehearsal" must have run end to end, timed, before this plan is final.

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
variables, environments, the npm trusted publisher setting, branch protection, and container
registry paths that include the operator's name. The exact list is a this stage research item and
is confirmed by a transfer rehearsal with a throwaway repository an operator input.

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
2. **Data migration** (to be built in this stage): both PostgreSQL databases (`umami` and
   `platform`) are restored into the new environment from a pgBackRest backup, or with
   `pg_dump` and `pg_restore` when the providers differ. Queue contents are not migrated:
   ingestion is drained before the copy, and `jobs run reconcile` rebuilds the NATS
   registries from the `cp` tables afterwards. A `verify-migration` command compares
   per-tenant daily web and API aggregates, row counts per table, and Umami `/stats` for a
   sample of sites and days between source and target.
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

Every secret the original operator could read is replaced. Today, after a value changes in
the encrypted file, `site.yml` applies on its own: the five platform role passwords, the NATS
passwords and hashes, and every service env file ([../deploy/secrets.md](../deploy/secrets.md)
section 4). Not yet automated, and to be built as `rotate-all.yml` in this stage: the Umami
database and admin passwords, the PostgreSQL superuser, replication and Patroni REST
passwords, `UMAMI_USER_PASSWORD_KEY` and the OAuth secrets.

Effects that tenants notice:

- Rotating `UMAMI_USER_PASSWORD_KEY` changes every derived Umami password at once, so every
  open web dashboard session ends and users re-enter through the console.
- Rotating Umami's `APP_SECRET` invalidates existing Umami tokens and changes session hashing
  for the current day. Whether the new operator keeps or rotates it, and the exact effect, is
  a this stage research item.
- OTLP tokens and tracker snippets do not change, so tenants change nothing in their code.
- Operator API keys of machine consumers are revoked and re-issued only if the original
  operator could have read them; keys are stored only as hashes, so a key the original
  operator never saw stays valid.

## 5. Domain: transfer or redirect

Every tenant page references the tracker by a pinned URL with an integrity hash and posts to
`/v1/e` on `in.<BASE_DOMAIN>`. Tenant applications export to `otlp.<BASE_DOMAIN>`.

- **Transfer (preferred).** The registrar transfer moves the domain; the zone is recreated from
  OpenTofu in the new DNS account. Every existing integration keeps working, because the names
  do not change. If SCF registered the domain from day one, this is a permission change.
- **Permanent redirect.** The old hostnames answer with permanent redirects to the new ones
  and keep serving the tracker files. Whether a browser still accepts a script loaded through
  a cross-origin redirect with its integrity check, and whether a `fetch` POST with
  `keepalive` follows a 308 redirect across origins with its body, is **not yet tested**. It
  is a this stage research item in Chromium, Firefox and WebKit. If either fails in any engine,
  a redirect cannot preserve existing integrations, and transfer is the only safe option.
  OTLP exporters following redirects is part of the same research.

## 6. If no operator is appointed

If SCF has not appointed an operator by the end of the 12-month window:

- The repository stays public under its open-source licence, with this plan, the IaC and the
  runbooks, so anyone can rebuild the system from it.
- Ahead of the end of the window, tenants are notified that the hosted service ends, with
  the date. The service has no data export feature today beyond what the dashboards and the
  metrics API show; whether one is needed before shutdown is a question for SCF.
- At the end of the window the original operator stops ingestion, deletes all data including
  backups at the end of their retention, closes the cloud, DNS and probe accounts, and hands
  the domain to SCF (or lets it lapse if SCF declines it). Each step is announced on the
  repository.
- What the notice period is, whether SCF wants a data export for itself, and whether SCF takes
  over the domain are questions for SCF (the design, "Who is the community operator, and
  what happens if none is appointed?").

## 7. Done when

All of these are true and recorded in `docs/handover/rehearsal-<date>.md` (this stage) and,
for the real handover, in a closing issue on the repository:

- The new operator runs the system from its own accounts, built from the repository.
- `verify-migration` reports identical per-tenant aggregates and matching row counts, and
  tenants see their history.
- Existing tenant snippets deliver events to the new environment in three browser engines.
- Every old credential is refused.
- The repository is owned by the new operator.
- The domain is transferred, or the redirect path was proven safe and is in place.
