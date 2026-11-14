---
title: "Security policy"
description: "How to report a vulnerability privately, the response targets and the scope of the policy."
---

## Reporting a vulnerability

Report vulnerabilities privately through GitHub private vulnerability reporting:

1. Open the repository [github.com/justmert/veristar](https://github.com/justmert/veristar).
2. Go to the **Security** tab, then **Advisories**.
3. Press **Report a vulnerability** and fill in the form.

Only you and the repository maintainers see the report. Do not open a public issue, pull request
or discussion for a vulnerability. GitHub private vulnerability reporting is the only reporting
channel; there is no separate security email address.

A useful report names:

- the affected component (tracker, gateway, collector, control plane, console, Caddy
  configuration or infrastructure code);
- the version or commit;
- the steps to reproduce;
- what an attacker gains.

## Supported versions

| Component | Supported |
|---|---|
| Hosted service (gateway, collector, replayer, metricswriter, jobs, control plane, console) | The version deployed from the latest commit on `main`. Fixes are deployed, not backported. |
| Tracker `sdk.js` | Every version in `packages/tracker/releases/manifest.json` that is not listed in `packages/tracker/releases/revoked.json`. A fix ships as a new version. A version with a serious flaw is revoked, and the gateway then answers 410 for it. Releases never change in place, so you upgrade a tracker on purpose ([verify-tracker.md](verify-tracker.md)). |
| Umami | The version pinned by digest in `infra/ansible/vars/pins.yml` (`ghcr.io/umami-software/umami:3.5.0`). It runs unmodified, so also report vulnerabilities in Umami itself to the Umami project. |
| Copies that others self-host | Covered only by the fixes that land on `main`. |

## Response targets

| Severity | Target |
|---|---|
| Critical and high | Patched or mitigated in production within 24 hours. The steps are in [../runbooks/security-patch.md](../runbooks/security-patch.md). |
| Medium and low | A first response within 5 business days and a fix within 10 business days, the targets of [../support.md](../support.md). |

Severity is CVSS v3.1 together with the impact on the platform. Anything that leaks one tenant's
data to another, exposes visitor IP addresses, bypasses authentication or changes the tracker
served to tenant pages counts as critical or high.

A fix can start as a mitigation, for example blocking a route in Caddy or taking the dashboards
offline while ingestion continues, with the full fix after it.

Once the fix is deployed, the maintainers publish a GitHub security advisory, with credit to the
reporter if the reporter wants it.

## Scope

In scope:

- Code and configuration in this repository: `cmd/`, `internal/`, `collector/`, `apps/console/`,
  `packages/tracker/`, `deploy/` (including the Caddyfiles), `infra/` and `db/`.
- The hosted service on the `in.`, `otlp.`, `app.`, `web.` and `status.` hosts under the
  platform's base domain.
- Tenant isolation, the privacy guarantees in [../compliance/privacy-strategy.md](../compliance/privacy-strategy.md),
  and any bypass of the bot and inflation checks that costs less than
  [bot-and-inflation.md](bot-and-inflation.md) states.

Not covered by this policy:

- The boundaries listed in [bot-and-inflation.md](bot-and-inflation.md) section 9, for example
  that a scripted client can solve the challenge and set browser headers, or that real browsers on
  residential proxies pass every check.
- Volumetric denial of service, spam, and social engineering of maintainers or operators.
- Vulnerabilities in Umami, the OpenTelemetry Collector, NATS, Caddy, PostgreSQL or other
  upstream projects that do not affect this deployment. Report those upstream.
- Findings that need the victim's device, browser or GitHub account to be compromised first.
- Missing hardening headers on pages that carry no data, without a working attack.

## Safe harbour

Good-faith research under this policy is welcome. The maintainers do not pursue or support legal
action against you for it, provided you:

- test only against your own tenant, your own sites and your own accounts, or against a local copy
  built from this repository (`make dev-up`);
- do not access, change or delete data of other tenants or visitors beyond the minimum needed to
  show the issue, and stop and report as soon as you reach such data;
- do not degrade the service for others (no load tests and no denial of service against the
  hosted service);
- report privately as described above and allow reasonable time for a fix before disclosure.

If you are unsure whether something is allowed, ask in the private report first.

## No bounty

There is no bug bounty and no payment of any kind. The platform charges no fees and pays none for
reports. Credit in the published advisory is offered instead.

## Related documents

- [threat-model.md](threat-model.md)
- [bot-and-inflation.md](bot-and-inflation.md)
- [supply-chain.md](supply-chain.md)
- [verify-tracker.md](verify-tracker.md)
- [branch-protection.md](branch-protection.md)
