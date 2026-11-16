---
title: "0002: Separate services beside Umami, not a fork"
sidebarTitle: "0002: Overlay, not fork"
description: "Why the extra features run as separate services beside an unmodified Umami."
---

- Status: accepted
- Date: 2026-11-12

## Context

The Stellar Community Fund request for proposals (RFP) asks for things Umami does not do on its own: a tracker and endpoints
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

Umami is configured only through its documented environment variables.
No public host serves Umami ([ADR 0005](0005-umami-as-engine-only.md)): it is
reachable on the private network only, for `replayer`, `controlplane` and
`jobs`, and its interface on the node's loopback for platform administrators
over an SSH tunnel. The console's web dashboard reads it through the control
plane.

## Consequences

Good:

- Upgrading Umami is a digest change plus a test run, not a merge. Upstream
  ships roughly monthly (see ADR 0001), and v3.5.0 alone carried a list of
  security fixes. A fork would have to rebase every one of those onto its own changes
  before shipping them, which works against the 24-hour patch target of RFP section 3.4.
- Umami security fixes reach production as soon as the pin moves. Platform code never
  sits inside Umami's request handling.
- Ingestion keeps working while Umami or PostgreSQL is down, because the gateway
  only depends on NATS. The queue drains when Umami returns, and event
  timestamps keep the data in the right time buckets.
- The services have narrow jobs. Only the gateway ever sees a raw IP.
- A future operator can understand Umami from upstream documentation, because
  the deployment does not change it.

Costs:

- The platform depends on Umami's HTTP API and payload shape staying compatible. Each
  upgrade has to be checked against the routes the replayer and control plane
  call. No public host serves Umami ([ADR 0005](0005-umami-as-engine-only.md)), so a
  new route in a future Umami release is not reachable from the internet;
  `make test-caddy-closed` keeps it that way and the
  [Umami upgrade runbook](../runbooks/umami-upgrade.md) checks the API contract.
- Some Umami behaviour has to be reproduced outside it. For example, Umami skips
  its 30-minute visit expiry when a timestamp is supplied, so the replayer keeps
  visit state itself (`visit_state`, [architecture](../architecture.md#queues-and-stores)).
- More moving parts to deploy and monitor than a single patched application.
- Features Umami has but the platform does not want (session replay, heatmaps,
  error tracking and others) cannot be compiled out. They are disabled by not
  shipping their client code and by blocking their routes.

## Sources

- [ADR 0001](0001-base-platform.md) for the verified facts about Umami's collect route and
  release cadence.
- RFP sections 3.2 and 3.4.
