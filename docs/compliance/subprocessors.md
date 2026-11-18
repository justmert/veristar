---
title: "Sub-processors"
description: "The sub-processors that support the service, and what each one handles."
---

The sub-processors of the production deployment in `infra/tofu/envs/production`
([data-residency.md](data-residency.md)), and the data each one can access.
Changes are announced under `dpa-template.md` clause 6. This page is not legal
advice. Last updated 2026-11-18.

| Sub-processor | Service | Location of processing | Data it can access |
|---|---|---|---|
| Microsoft Azure | virtual machines, virtual network, managed disks, Blob Storage for backups, Traffic Manager health-checked DNS failover | Denmark (Azure Denmark East, availability zones 1, 2 and 3; backups in zone-redundant Blob Storage in the same region); the encrypted OpenTofu state in Sweden (Azure Sweden Central); the external prober and public status page in the United States (Azure East US, [../deploy/probe.md](../deploy/probe.md)); Traffic Manager answers DNS globally | everything the service stores and queues (`privacy-strategy.md` section 2), as an infrastructure provider; Traffic Manager sees DNS queries only; the prober's VM holds only synthetic data of the internal probe tenant and the check results |
| Cloudflare | domain registration and authoritative DNS, DNS only (no proxy) | Cloudflare's DNS network | DNS queries for the service's hostnames, which include the resolver's address; no analytics data |
| GitHub, Inc., United States | sign-in for console users (OAuth); lookup of the numeric id of a GitHub login when a project member or an operator is added | United States | that a console user signs in; the user's GitHub id and login; the logins looked up. No visitor or API client data |

Not sub-processors:

- **DB-IP, ipverse and the Tor Project** publish the open data the gateway
  uses: DB-IP's monthly country and autonomous system files, ipverse's
  autonomous system categories and Tor's list of exit addresses. The files are
  downloaded when the gateway image is built (`deploy/images/gateway.Dockerfile`),
  and the gateway looks addresses up in its local copies; no data is sent to
  these publishers.
- **SDF's Horizon (`horizon.stellar.org`) and a public Stellar RPC
  (`mainnet.sorobanrpc.com`)** answer the control plane's and the jobs'
  reads of public ledger records. The requests carry only the public account
  and contract ids a project declared, so these providers receive no personal
  data (`privacy-strategy.md` section 11).
- **Umami** is open-source software run by the operator. No data is sent to
  Umami Software; telemetry and update checks are switched off and outbound
  traffic is tested (`tests/webpath/egress_test.go`).
- **No alert push service.** Operational alerts are shown on the console's
  operator Alerts page ([../operators/alerts.md](../operators/alerts.md)) and
  are not sent to any outside service. The optional Discord webhook receiver
  is not configured in production. If a push channel is configured and its
  alert texts carry tenant data (for example a site name), that service is
  added to the table above first.

