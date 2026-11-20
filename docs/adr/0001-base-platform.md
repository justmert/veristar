---
title: "0001: Umami as the base platform"
sidebarTitle: "0001: Umami as the base"
description: "Why Veristar builds its web analytics on Umami."
icon: "cube"
---

- Status: accepted
- Date: 2026-11-12

## Context

The Stellar Community Fund request for proposals (RFP) behind Veristar requires the
platform to be built on either Umami or GoatCounter, and the choice has to be justified. The web side must report page views, unique visitors,
referrers, session duration, bounce rate, country, and device and browser, for
each tenant and across tenants. The whole solution must be released under an
OSI-approved licence.

Both candidates were checked against their own source and release records on
2026-11-12. The versions compared are the latest release of each:

- Umami v3.5.0, published 2026-11-12
  ([release](https://github.com/umami-software/umami/releases/tag/v3.5.0)).
- GoatCounter v2.7.0, published 2025-12-15, still the latest release on 2026-11-12
  ([release](https://github.com/arp242/goatcounter/releases/tag/v2.7.0)).

### Metrics

Umami computes bounces and visit time natively. In
[`src/queries/sql/getWebsiteStats.ts` at v3.5.0](https://github.com/umami-software/umami/blob/v3.5.0/src/queries/sql/getWebsiteStats.ts)
the PostgreSQL query groups `website_event` rows by `session_id` and `visit_id`
and returns `pageviews`, `visitors` (distinct sessions), `visits` (distinct
visits), `bounces` and `totaltime`:

- A bounce is a visit with exactly one counted event and no custom event. Counted
  events exclude event types 2 and 5, which are custom events and performance
  measurements
  ([`EVENT_TYPE` in `src/lib/constants.ts`](https://github.com/umami-software/umami/blob/v3.5.0/src/lib/constants.ts)).
- `totaltime` is the sum, over all visits, of the time between the first and the
  last counted event of the visit. Dividing it by `visits` gives the average visit
  duration.

GoatCounter v2.7.0 has neither metric. Its statistics tables in
[`db/schema.gotxt`](https://github.com/arp242/goatcounter/blob/v2.7.0/db/schema.gotxt)
are `hit_counts`, `ref_counts`, `hit_stats`, `browser_stats`, `system_stats`,
`location_stats`, `size_stats`, `language_stats` and `campaign_stats`. They hold
counts per path and per hour or day, with no notion of a visit, its length or
whether it bounced. A search of the v2.7.0 source tree for "bounce" finds nothing,
and the only mention of time on page is a changelog note that raw page views are
"still stored for some future purposes (such as "time on page")"
([CHANGELOG.md](https://github.com/arp242/goatcounter/blob/v2.7.0/CHANGELOG.md)).
Building on GoatCounter would mean adding session tracking to it, which is the
hardest part of the metric set.

### Licence

Umami is MIT licensed
([LICENSE at v3.5.0](https://github.com/umami-software/umami/blob/v3.5.0/LICENSE),
`"license": "MIT"` in
[package.json](https://github.com/umami-software/umami/blob/v3.5.0/package.json)).

GoatCounter's
[LICENSE at v2.7.0](https://github.com/arp242/goatcounter/blob/v2.7.0/LICENSE)
states that it "is licensed under a slightly modified version of European Union
Public License (EUPL) 1.2; the only difference is that the "Compatible Licenses"
Appendix removes some licenses". The unmodified EUPL-1.2 is on the
[OSI list](https://opensource.org/licenses). The modified text is not itself an
OSI-approved licence, and it is a copyleft licence, which would constrain how the
rest of this repository could be licensed if GoatCounter code were changed or
embedded. Its `count.js` script is ISC licensed.

### Multi-tenancy model

Umami stores everything in PostgreSQL
([`prisma/schema.prisma`](https://github.com/umami-software/umami/blob/v3.5.0/prisma/schema.prisma),
`provider = "postgresql"`), the same database the RFP requires for API metrics.
It has teams (`Team`, `TeamUser` models) and these roles: global `admin`, `user`,
`view-only`, and team roles `team-owner`, `team-manager`, `team-member`,
`team-view-only`
([`ROLES` in `src/lib/constants.ts`](https://github.com/umami-software/umami/blob/v3.5.0/src/lib/constants.ts)).
That maps directly onto one team per tenant with read-only members.

### Maintenance and security

GoatCounter is effectively maintained by one person. The GitHub contributors list
shows 2137 commits by `arp242` and no more than 4 by anyone else
([contributors](https://github.com/arp242/goatcounter/graphs/contributors)). Of
the 100 most recent commits since 2025-11-12, 90 are by `arp242`.

[CVE-2026-108740](https://www.cve.org/CVERecord?id=CVE-2026-108740), published
2026-10-11 by VulnCheck, affects "GoatCounter through 2.7.0": a mass-assignment
flaw in the `/user/pref` handler lets a logged-in user with read-only access give
themselves admin or superuser rights. The CNA scores it CVSS 4.0 7.2 (high). On
2026-11-12 no GoatCounter release after v2.7.0 exists, so no fixed release is
available. A read-only operator escalating to full access is exactly the failure
a multi-tenant service has to rule out.

Umami is developed by a company (the licence names Umami Software, Inc.) and
ships roughly monthly: v3.2.0 (2026-06-24), v3.3.0 (2026-08-12), v3.3.1
(2026-08-20), v3.4.0 (2026-09-17), v3.4.1 (2026-10-23), v3.5.0 (2026-11-12)
([releases](https://github.com/umami-software/umami/releases)). The v3.5.0 notes
include a security section: the seeded administrator password must be changed,
two-factor enrollment is enforced on the server, and team permission and API key
route checks are tightened.

## Decision

Build on Umami v3.5.0, using its official container image unmodified.

## Consequences

Good:

- Bounce rate and visit duration come from Umami's own queries, so they do not
  have to be invented and defended.
- Umami's teams and view-only roles give the web dashboard's Umami reads a second
  isolation check beside the control plane's own
  ([ADR 0005](0005-umami-as-engine-only.md)).
- MIT allows the Veristar tracker to derive from Umami's while the rest of the
  repository stays under Apache-2.0.
- Web data and API metrics share one PostgreSQL cluster.

Costs:

- Umami is a much larger dependency. v3.5.0 declares 62 runtime and 31
  development npm dependencies, including Next.js 16.4.2 and React 19
  ([package.json](https://github.com/umami-software/umami/blob/v3.5.0/package.json)).
  GoatCounter v2.7.0 is a single Go binary with 38 direct module requirements
  ([go.mod](https://github.com/arp242/goatcounter/blob/v2.7.0/go.mod)).
- Its framework has a serious CVE history. Examples:
  [CVE-2025-29927](https://www.cve.org/CVERecord?id=CVE-2025-29927), an
  authorization bypass in Next.js middleware, and
  [CVE-2025-55182](https://www.cve.org/CVERecord?id=CVE-2025-55182), a
  pre-authentication remote code execution in React Server Components 19.0.0
  to 19.2.0. Running Umami means tracking Next.js and React advisories and
  rebuilding on their fixes.
- The roughly monthly release cadence means regular upgrade work, and each
  release has to be reviewed against the routes and settings the platform depends on.
- Umami's collect route trusts every caller. In
  [`src/app/api/send/route.ts`](https://github.com/umami-software/umami/blob/v3.5.0/src/app/api/send/route.ts)
  the request is parsed with `skipAuth: true`, and the payload schema
  ([`request-schema.ts`](https://github.com/umami-software/umami/blob/v3.5.0/src/app/api/send/request-schema.ts))
  accepts caller-supplied `ip`, `userAgent`, `timestamp` and `salt`. The route does
  not check `Origin`. The only gates are that the website id exists, the
  `isbot` user-agent check and the `IGNORE_IP` list. Exposed to the internet, it
  would let anyone post events for any site with any IP. This is why the platform
  puts its own gateway in front and never exposes Umami's collect routes
  ([ADR 0002](0002-overlay-not-fork.md)).

## Sources

- Umami v3.5.0 release: https://github.com/umami-software/umami/releases/tag/v3.5.0
- Umami release list: https://github.com/umami-software/umami/releases
- Umami LICENSE: https://github.com/umami-software/umami/blob/v3.5.0/LICENSE
- Umami package.json: https://github.com/umami-software/umami/blob/v3.5.0/package.json
- Umami stats query: https://github.com/umami-software/umami/blob/v3.5.0/src/queries/sql/getWebsiteStats.ts
- Umami constants: https://github.com/umami-software/umami/blob/v3.5.0/src/lib/constants.ts
- Umami schema: https://github.com/umami-software/umami/blob/v3.5.0/prisma/schema.prisma
- Umami collect route: https://github.com/umami-software/umami/blob/v3.5.0/src/app/api/send/route.ts
- GoatCounter v2.7.0 release: https://github.com/arp242/goatcounter/releases/tag/v2.7.0
- GoatCounter LICENSE: https://github.com/arp242/goatcounter/blob/v2.7.0/LICENSE
- GoatCounter schema: https://github.com/arp242/goatcounter/blob/v2.7.0/db/schema.gotxt
- GoatCounter contributors: https://github.com/arp242/goatcounter/graphs/contributors
- CVE-2026-108740: https://www.cve.org/CVERecord?id=CVE-2026-108740
- CVE-2025-29927: https://www.cve.org/CVERecord?id=CVE-2025-29927
- CVE-2025-55182: https://www.cve.org/CVERecord?id=CVE-2025-55182
- OSI licence list: https://opensource.org/licenses
