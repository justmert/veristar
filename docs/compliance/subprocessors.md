# Sub-processors

> **Status: draft, pending legal review an operator input.** Not legal advice. Last
> updated 2026-11-12. The list describes the planned production deployment
> (`.scratch/plan.md` section 5, phases 15 to 17). No production deployment
> exists yet; the list becomes binding when it is published with the final
> terms. Changes are announced under `dpa-template.md` clause 6.

| Sub-processor | Service | Location of processing | Data it can access |
|---|---|---|---|
| Hetzner Online GmbH, Germany | virtual servers, private network, block storage, object storage for backups | Germany (Falkenstein, Nuremberg), Finland (Helsinki) | everything the service stores and queues (`privacy-strategy.md` section 2), as an infrastructure provider |
| BunnyWay d.o.o. (bunny.net), Slovenia | authoritative DNS with health checks | EU | DNS queries for the service's hostnames, which include the resolver's address; no analytics data |
| Scaleway SAS, France | one virtual server for the status page and the external prober | France (Paris) | none of the tenants' data; the prober sends synthetic traffic to an internal probe project |
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
