---
title: "Sub-processors"
description: "The sub-processors that support the service, and what each one handles."
---

> **Status: draft, pending legal review an operator input.** Not legal advice. Last
> updated 2026-11-14. The list describes the production deployment in
> `infra/tofu/envs/production`, running since 2026-11-13
> ([data-residency.md](data-residency.md)). The list becomes binding when it is
> published with the final terms. Changes are announced under
> `dpa-template.md` clause 6.

| Sub-processor | Service | Location of processing | Data it can access |
|---|---|---|---|
| Microsoft Azure | virtual machines, virtual network, managed disks, Blob Storage for backups, Traffic Manager health-checked DNS failover | Denmark (Azure Denmark East, availability zones 1, 2 and 3; backups in zone-redundant Blob Storage in the same region); the encrypted OpenTofu state in Sweden (Azure Sweden Central); Traffic Manager answers DNS globally | everything the service stores and queues (`privacy-strategy.md` section 2), as an infrastructure provider; Traffic Manager sees DNS queries only |
| Cloudflare | domain registration and authoritative DNS, DNS only (no proxy) | Cloudflare's DNS network | DNS queries for the service's hostnames, which include the resolver's address; no analytics data |
| GitHub, Inc., United States | sign-in for console users (OAuth) | United States | that a console user signs in; the user's GitHub id and login. No visitor or API client data |

Not sub-processors:

- **DB-IP** provides the country database as a monthly file. The gateway uses a
  local copy; no data is sent to DB-IP.
- **Umami** is open-source software run by the operator. No data is sent to
  Umami Software; telemetry and update checks are switched off and outbound
  traffic is tested (`tests/webpath/egress_test.go`).
- **No alert push service.** Operational alerts are shown on the console's
  operator Alerts page ([../operators/alerts.md](../operators/alerts.md)) and
  are not sent to any outside service: the Discord webhook (plan an operator input is
  not configured, by operator decision. If a push channel is configured later and
  its alert texts carry tenant data (for example a site name), that service is
  added to the table above first.
