# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub private vulnerability reporting:

1. Open the repository `github.com/justmert/stellar-analytics`.
2. Go to the **Security** tab, then **Advisories**.
3. Press **Report a vulnerability** and fill in the form.

The report is visible only to you and the repository maintainers. Please do not open a public
issue, pull request or discussion for a vulnerability.

Current state, stated plainly:

- **The repository is private for now.** It will be made public before the first npm
  publication and before production launch, as the RFP requires, once the operator confirms.
  Until then only people with access to the repository can reach it.
- **Private vulnerability reporting must be enabled by the repository owner** under
  Settings, "Security and quality", "Advanced Security", "Private vulnerability reporting",
  **Enable**. Until it is enabled the **Report a vulnerability** button does not appear.
  GitHub's documentation describes the feature for public repositories ("Owners and
  administrators of public repositories can allow security researchers to report
  vulnerabilities securely"). On 2026-11-13 the API
  `GET /repos/justmert/stellar-analytics/private-vulnerability-reporting` answered 404 for this
  private repository, so the setting is expected to become available once it is public.
- No separate security email address exists. GitHub private vulnerability reporting is the only
  channel.

Helpful content in a report: the affected component (tracker, gateway, collector, control plane,
console, Caddy configuration, infrastructure code), the version or commit, steps to reproduce,
and what an attacker gains.

## Supported versions

| Component | Supported |
|---|---|
| Hosted service (gateway, collector, replayer, metricswriter, jobs, control plane, console) | The version deployed from the latest commit on `main`. Fixes are deployed, not backported. |
| Tracker `sdk.js` | Every version in `packages/tracker/releases/manifest.json` that is not listed in `packages/tracker/releases/revoked.json`. A fix ships as a new version; a version with a serious flaw is revoked, after which the gateway answers 410 for it. Releases never change in place, so tenants must upgrade intentionally (see [verify-tracker.md](verify-tracker.md)). |
| Umami | Only the version pinned by digest in the deployment (`ghcr.io/umami-software/umami:3.5.0` at the time of writing). It runs unmodified; vulnerabilities in Umami itself should also go to the Umami project. |
| Self-hosted copies run by others | Not supported by this policy beyond fixes landing on `main`. |

## Response targets

| Severity | Target |
|---|---|
| Critical and high | Patched or mitigated in production within 24 hours, as the RFP requires (section 3.4: "Critical and high security vulnerabilities patched within 24 hours"). The steps are in [../runbooks/security-patch.md](../runbooks/security-patch.md). |
| Medium and low | Acknowledged within 5 business days and fixed within 10 business days, the RFP's support targets. |

Severity is assessed with CVSS v3.1 together with the platform's own impact: anything that leaks
one tenant's data to another, exposes visitor IP addresses, bypasses authentication, or lets
someone change the tracker served to tenant pages is treated as critical or high.

A fix can be a mitigation first (for example blocking a route in Caddy, or taking dashboards
offline while ingestion continues) and the full fix after.

After the fix is deployed, a GitHub security advisory is published with credit to the reporter
if they want it.

## Scope

In scope:

- Code and configuration in this repository: `cmd/`, `internal/`, `collector/`, `apps/console/`,
  `packages/tracker/`, `deploy/` (including the Caddyfiles), `infra/`, `db/`.
- The hosted service on `in.`, `otlp.`, `app.`, `web.` and `status.` under the platform's base
  domain, once it exists.
- Tenant isolation, the privacy guarantees in `docs/compliance/`, and the bot and inflation
  checks where a bypass is cheaper than [bot-and-inflation.md](bot-and-inflation.md) states.

Out of scope:

- The limits stated as known in [bot-and-inflation.md](bot-and-inflation.md) section 9, for
  example that a scripted client can solve the challenge and forge headers, or that real
  browsers on residential proxies pass every check.
- Volumetric denial of service, spam, and social engineering of maintainers or operators.
- Vulnerabilities in Umami, Next.js, the OpenTelemetry Collector, NATS, Caddy, PostgreSQL or
  other upstream projects that do not affect this deployment. Report those upstream.
- Findings that need a compromised device, browser or GitHub account of the victim.
- Missing hardening headers on pages that carry no data, without a working attack.

## Safe harbour

Research done in good faith under this policy is welcome. We will not pursue or support legal
action against you for it, provided you:

- test only against your own tenant, your own sites and your own accounts, or a local copy built
  from this repository (`make dev-up`);
- do not access, change or delete data of other tenants or visitors beyond the minimum needed to
  show the issue, and stop and report as soon as you reach such data;
- do not degrade the service for others (no load tests or denial of service against the hosted
  service);
- report privately as above and give reasonable time for a fix before disclosure.

If in doubt whether something is allowed, ask in the private report first.

## No bounty

There is no bug bounty and no payment of any kind. The platform charges no fees to anyone, and
it pays none for reports. Credit in the published advisory is offered instead.

## Related documents

- [threat-model.md](threat-model.md)
- [bot-and-inflation.md](bot-and-inflation.md)
- [supply-chain.md](supply-chain.md)
- [verify-tracker.md](verify-tracker.md)
- [branch-protection.md](branch-protection.md)
