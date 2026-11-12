# 0002: Separate services beside Umami, not a fork

- Status: accepted
- Date: 2026-11-12

## Context

The RFP asks for things Umami does not do on its own: a tracker and endpoints
that ad blockers leave alone, bot and inflation checks that tell real browser
JavaScript from server-side posts, tenant self-service enrollment and operator
removal, an operator view across tenants, OpenTelemetry ingest for API metrics,
and 99.99% ingestion availability.

Umami's collect route needs the database for every event and accepts whatever
the caller sends, including `ip`, `userAgent`, `timestamp` and `salt`, with no
`Origin` check (see ADR 0001). Ingestion that goes straight into Umami is
therefore neither trustworthy nor available while the database or Umami is down.

There are two ways to add the missing parts: fork Umami and change it, or run
the official Umami image unmodified and put separate services next to it.

## Decision

Run the official image `ghcr.io/umami-software/umami:3.5.0`, pinned by digest and
not patched. Everything else is a separate service:

- `gateway` on the edge receives browser events, checks them (schema, site
  status, `Origin`, browser proof, rate limits, same-IP clustering), handles the
  IP in memory only and queues accepted events in NATS JetStream.
- `replayer` posts queued events to Umami's `/api/send` on the private network,
  with a constant placeholder IP and a daily visitor token as the salt.
- `controlplane` and the console handle sign-in, tenants, sites and Umami
  provisioning through Umami's REST API.
- `collector`, `metricswriter` and `jobs` handle API metrics, which Umami has no
  part in.

Umami is configured only through its documented environment.
Caddy in front of the Umami UI denies from the internet every route the
platform does not want public, including `/api/send`, `/api/batch`,
`/api/record`, `/api/auth/login`, user and admin routes, share routes,
`/script.js` and `/recorder.js`. Those routes stay reachable on the private
network for `replayer` and `controlplane`.

## Consequences

Good:

- Upgrading Umami is a digest change plus a test run, not a merge. Upstream
  ships roughly monthly (see ADR 0001), and v3.5.0 alone carried a list of
  security fixes. A fork would have to rebase every one of those onto our changes before
  we could ship them, which works against the 24-hour patch target in RFP 3.4.
- Umami security fixes reach us as soon as we move the pin. Our code never sits
  inside Umami's request handling.
- Ingestion keeps working while Umami or PostgreSQL is down, because the gateway
  only depends on NATS. The queue drains when Umami returns, and event
  timestamps keep the data in the right time buckets.
- The services have narrow jobs. Only the gateway ever sees a raw IP.
- A future operator can understand Umami from upstream documentation, because
  our deployment does not change it.

Costs:

- We depend on Umami's HTTP API and payload shape staying compatible. Each
  upgrade has to be checked against the routes the replayer and control plane
  call, and against the deny list. A new public route in a future Umami release
  would be reachable until we add it to the deny list, so the deny list is
  reviewed on every upgrade.
- Some Umami behaviour has to be reproduced outside it. For example, Umami skips
  its 30-minute visit expiry when a timestamp is supplied, so the replayer keeps
  visit state itself.
- More moving parts to deploy and monitor than a single patched application.
- Features Umami has but the platform does not want (session replay, heatmaps,
  error tracking and others) cannot be compiled out. They are disabled by not
  shipping their client code and by blocking their routes.

## Sources

- the design (internal, `.scratch/plan.md`).
- ADR 0001 for the verified facts about Umami's collect route and release cadence.
- RFP sections 3.2 and 3.4.
