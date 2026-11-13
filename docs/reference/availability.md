# Availability statement

Targets (RFP 3.4): ingestion 99.99%, dashboards 99.90%. Status on 2026-11-13: **the
failover windows that this statement is meant to rest on have not been measured.** The
this stage drills ran on a local stand-in and only the load profile produced valid numbers
(`docs/reference/drills/2026-11-13.md`). This page gives the arithmetic, the windows the
configuration allows, and the single points of failure. Each configured window must be
replaced by a measured one before the targets are claimed.

## Budgets

A 30-day month has 30 x 24 x 60 = 43,200 minutes.

| Target | Allowed downtime per month | Per year (525,600 min) |
|---|---|---|
| 99.99% (ingestion) | 43,200 x 0.0001 = **4.32 min** (259 s) | 52.6 min |
| 99.90% (dashboards) | 43,200 x 0.001 = **43.2 min** | 8.76 h |

## Windows per failure

"Configured" means the limit that follows from a setting. It is not a measurement.

| Failure | Ingestion impact | Dashboard impact | Source |
|---|---|---|---|
| One edge node lost | requests to that address fail until DNS drops it: check every 30 s, TTL 60 s, so up to about 90 s for a client holding the record. A browser that gets a refused or timed-out connection tries the next address at once | none | configured (bunny.net 30 s check, `infra/tofu` TTL 60). Removal timing blocked on I6, not measured |
| One NATS member lost | none expected (R3, quorum 2 of 3) | none | not measured |
| Patroni leader lost | none: the edge does not touch PostgreSQL | writes and reads that need the leader stop until promotion: leader key TTL 30 s, plus up to one 10 s loop: **at most about 40 s** | configured (`core_patroni_dcs`), not measured |
| Whole core tier down | none: the edge queues for 72 h (`WEB_EVENTS` max age) | down for the whole outage | configured, not measured |
| One location cut off | the cut-off edge answers 503 until DNS drops it (as edge loss) | if the cut-off location holds the Patroni leader: as leader loss | not measured |
| Rolling deploy | none if DNS drains the node first (`deploy.yml` waits 120 s) | one switchover per core node with a changed postgres image, a few seconds each | not measured |
| Full NATS restart | ingest answers 503 until JetStream has quorum again; visitors seen earlier that day are counted again | none | not measured |

## What the targets tolerate

Ingestion at 99.99% allows 259 s a month. If each edge node loss costs up to 90 s of
failed requests for clients pinned to that address, and a browser's move to the next
address hides most of that, the budget absorbs about **two unhandled edge losses a month**.
That is (259 / 90) = 2.9, so 2 whole windows. It absorbs **no** full NATS restart that lasts
longer than the remaining budget. Database and core failures cost ingestion nothing,
because the edge never waits on them.

Dashboards at 99.90% allow 2,592 s a month. At up to 40 s per leader failover, that is
(2,592 / 40) = **64 failovers a month**, or a single core-tier outage of **43 minutes**.

These counts use configured windows. The real windows can only be longer: resolver
caching, cold starts and host contention all add time. The drill record shows that a
saturated host stretched every operation by more than 100 s.

## Single points of failure

- **The DNS provider (bunny.net).** All failover for edge and core depends on its health
  checks and its record sets. If bunny.net is down, so are resolution and failover.
- **The single cloud provider (Hetzner).** All three locations, the private network and the
  object storage share one provider. An account suspension, a billing failure or a
  provider-wide outage takes everything.
- **Resolver caching.** Resolvers and browsers can keep a removed address past the TTL. The
  health check removes the record; it cannot recall copies that clients already hold.
- **The port-80 health check sees no TLS failure.** An expired or broken certificate on port
  443 leaves the node in DNS. this stage adds TLS checks with alerts.
- **One Umami process per core node, one active replayer.** At ten times the expected peak,
  the measured drain rate (about 280 web events/s) is below the load (500/s). Events are
  queued, not lost, but the dashboards lag behind (drill record, "what the load runs
  showed").
- **Two database nodes.** With synchronous mode not strict, the leader runs alone while the
  replica is down. A second failure in that time loses what only the leader had, back to the
  last archived WAL (`archive_timeout` 60 s).
- **Backup repository** in one object storage location (I5, not yet tested).
