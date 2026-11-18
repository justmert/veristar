---
title: "Handover readiness checklist"
sidebarTitle: "Checklist"
description: "Every item that changes hands so the new operator can run the service on its own, with the evidence for each."
---

This checklist lists every item that changes hands for the new operator to run the service on its
own. Tick an item only with the evidence named next to it. The procedures are in [plan.md](plan.md) and
[rebuild.md](rebuild.md); the order of sections follows the cutover.

Who signs: the new operator for each item, SCF for section 9.

## 1. Before the shadow period

- [ ] SCF has named the community operator and the handover start date ([plan.md](plan.md) section 2.1).
- [ ] The new operator has its own GitHub owner (user or organisation), cloud project, DNS
      account, probe account, alert receiver, and age identities for at least two people.
- [ ] The new operator's people have read [../architecture.md](../architecture.md),
      [../deploy/](../deploy/overview.md), [../runbooks/](../runbooks/overview.md),
      [../sla.md](../sla.md), [../support.md](../support.md) and this directory.
- [ ] The new operator's age public keys are recipients of **staging** only
      (`infra/secrets/recipients/staging.txt`, `sync-recipients.sh`).

## 2. Independent rebuild (shadow period)

- [ ] A new environment built from the repository alone in the new operator's accounts, following
      [../deploy/overview.md](../deploy/overview.md); every gap in the docs found on the way is fixed
      in the repository. Evidence: the issue or pull request per gap.
- [ ] It runs the same release as production (`SA_IMAGE_TAG`).
- [ ] `make tofu-check`, `make ansible-lint`, the second `site.yml` run reports `changed=0`.
- [ ] The end-to-end checks of [../deploy/verification.md](../deploy/verification.md) pass.
- [ ] The runbooks that need no production access were run on it: edge node loss, database
      failover, point-in-time restore, Umami upgrade.
- [ ] Its backups run: `pgbackrest info` lists a full backup, and the backup alerts are quiet.
- [ ] Its probe and status page measure it; one monthly SLO report exists.

## 3. Domain

- [ ] The registrar account that holds `<BASE_DOMAIN>` is known, and so is its transfer lock state
      (many registrars refuse a transfer within 60 days of a registration or a previous transfer,
      and the ICANN 60-day lock after a registrant change applies unless it was waived).
- [ ] The new DNS zone exists in the new account (`make <env>-apply`, `infra/tofu/modules/dns`)
      and `cutover-transfer.yml -e cutover_stage=precheck` passes against its nameservers.
- [ ] The auth code is issued, the transfer is approved by both sides, and the registrar shows the
      new owner.
- [ ] The delegation points at the new nameservers and `cutover-transfer.yml
      -e cutover_stage=verify` passes: public resolvers answer with the new nodes, every pinned
      tracker release is served byte for byte with a valid certificate, the console and OTLP
      answer.
- [ ] An example page that still carries a snippet from before the cutover delivers a pageview in
      Chromium, Firefox and WebKit (the `@cutover` check of `tests/e2e-console/handover/handover.spec.ts`,
      pointed at the real names, or by hand with the browsers' network tools).
- [ ] Only if the domain cannot be transferred: every tenant was told to replace its snippet and
      OTLP endpoint before `cutover-redirect.yml` ran ([plan.md](plan.md) section 5).

## 4. Data

- [ ] Ingestion was drained and the source stopped ([rebuild.md](rebuild.md) section 2).
- [ ] The export ran by the pgBackRest path (or the pg_dump path for another provider), and the
      directory's `SHA256SUMS` was checked on import.
- [ ] `make migrate-import` passed: schema versions equal, row-level security coverage check,
      `jobs run reconcile`.
- [ ] `platformctl umami rotate-admin` and `jobs run umami-rotate --force` ran on the new leader.
- [ ] `make verify-migration` printed `result: source and target are equal`. Evidence: its output.
- [ ] Two tenants confirmed they see their history on the **Web** and **API** pages of the new
      console.
- [ ] The dump directories were deleted after verification.

## 5. Secrets and credentials

- [ ] The original operator's age keys are removed from every
      `infra/secrets/recipients/<env>.txt`, `sync-recipients.sh` ran, and the change is merged.
- [ ] Every value they could read is rotated: `infra/secrets/rotate-secrets.py`, then
      `rotate-all.yml` with `rotate_previous_age_identity` when that identity is available. Its
      last play passed, which proves every previous PostgreSQL, NATS, NATS route, Umami admin,
      Umami user, Patroni REST and Grafana credential is refused and every new one accepted.
- [ ] Values issued by providers are replaced there and the old ones revoked: GitHub OAuth client
      secrets (`rotate-all.yml` asks GitHub), the Cloudflare token, Azure role assignments, Discord
      webhooks, the registry pull token, the OpenTofu state passphrase and the provider tokens in
      `tofu.enc.env`.
- [ ] The usage report signing key: the original operator's account id is in
      `sa_report_signing_retired_keys` of the new environment, or the pair was replaced in place with
      its retirement step ([plan.md](plan.md) section 4). A report moved with the data verifies with
      `platformctl report verify`.
- [ ] `secrets.previous.enc.yaml` deleted after `rotate-all.yml` passed.
- [ ] Operator API keys the original operator created are revoked
      ([../runbooks/operator-key-revocation.md](../runbooks/operator-key-revocation.md)).
- [ ] The original operator's SSH keys are removed from the `ops` user of every server
      (`admin_ssh_public_keys`), and their source addresses from `admin_cidrs`.

## 6. Repository and packages

- [ ] The repository is transferred to the new owner on GitHub; issues, pull requests, releases
      and the old URL redirect are checked after the move.
- [ ] Set up again under the new owner, because a transfer does not move them: Actions secrets
      and variables, environments and their protection rules, branch protection, Dependabot
      settings and alerts, the container registry path (images under `ghcr.io/<owner>/...`), the
      npm trusted publisher bound to the new repository.
- [ ] The npm scope is transferred and one tracker release is published from the new repository
      ([../runbooks/tracker-release.md](../runbooks/tracker-release.md)).
- [ ] Release images are built and signed from the new repository; `deploy.yml` verifies the new
      signing identity.

## 7. Accounts and access

- [ ] Cloud, DNS, probe and registrar accounts of the original operator hold nothing the new
      environment needs, and the original operator has no access to the new ones.
- [ ] GitHub OAuth apps of every environment belong to the new owner, with the
      callback URLs of the new environment and wildcard redirect matching off.
- [ ] The operator roster on the Operators page holds exactly the people SCF names, with SCF's own
      operator admins, and `OPERATOR_GITHUB_IDS` and `OPERATOR_STELLAR_ADDRESSES` list only SCF's
      recovery identities; the control
      plane was restarted after the settings change ([../operators/roster.md](../operators/roster.md)).
- [ ] Alerts reach the new operator: the console's operator Alerts page lists them, and a Discord
      webhook, if used, belongs to the new operator and received a test alert before the old one
      was removed.

## 8. Tenants

- [ ] Every project was told the cutover date, the new processor and that nothing changes in its
      snippet or exporter ([plan.md](plan.md) section 2.3, step 1), announced in the repository's
      changelog and in the console before it took effect.
- [ ] The public issue tracker and [../support.md](../support.md) name the new operator.

## 9. Done

- [ ] SCF confirms the new operator runs the system on its own, and the closing issue on the
      repository links the evidence above ([plan.md](plan.md) section 7).
- [ ] The old environment's teardown date is announced; its backups expire with their retention.
