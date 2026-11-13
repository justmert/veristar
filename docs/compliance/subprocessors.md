---
title: "Sub-processors"
description: "The sub-processors that support the service, and what each one handles."
---

> **Status: draft, pending legal review an operator input.** Not legal advice. Last
> updated 2026-11-13. The list describes the planned production deployment
> (`.scratch/plan.md` section 5, phases 15 to 17). No production deployment
> exists yet; the list becomes binding when it is published with the final
> terms. Changes are announced under `dpa-template.md` clause 6.

| Sub-processor | Service | Location of processing | Data it can access |
|---|---|---|---|
| Microsoft Azure | virtual machines, virtual network, managed disks, Blob Storage for backups, Traffic Manager health-checked DNS failover | Sweden (Azure Sweden Central); Traffic Manager answers DNS globally | everything the service stores and queues (`privacy-strategy.md` section 2), as an infrastructure provider; Traffic Manager sees DNS queries only |
| Cloudflare | domain registration and authoritative DNS, DNS only (no proxy) | Cloudflare's DNS network | DNS queries for the service's hostnames, which include the resolver's address; no analytics data |
| GitHub, Inc., United States | sign-in for console users (OAuth) | United States | that a console user signs in; the user's GitHub id and login. No visitor or API client data |

Not sub-processors:

- **DB-IP** provides the country database as a monthly file. The gateway uses a
  local copy; no data is sent to DB-IP.
- **Umami** is open-source software run by the operator. No data is sent to
  Umami Software; telemetry and update checks are switched off and outbound
  traffic is tested (`tests/webpath/egress_test.go`).
- **Discord** (alert webhook, plan an operator input will receive operational alerts.
  The alert texts are written in this stage. If they carry tenant data (for
  example a site name), Discord is added to the table above at that time.
