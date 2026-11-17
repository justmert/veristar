---
title: "Availability statement"
description: "The availability targets, the failover behaviour they rest on, and what has been measured."
---

Veristar targets 99.99% availability for ingestion and 99.90% for the dashboards
([../sla.md](../sla.md)). This page shows how the Azure design meets them: the arithmetic, the failover windows the configuration sets, and what each failure
costs. The windows below follow from the configuration (`infra/tofu/modules/failover`,
Patroni, NATS). The production drills measured the node loss and the leader loss:
[drills/2026-11-13-azure.md](drills/2026-11-13-azure.md) (one node's address left the `in`
answer 41.9 s after a power-off, writes resumed 30.3 s after the leader's power-off with no
committed row lost). The local drill record is [drills/2026-11-13.md](drills/2026-11-13.md).

## Budgets

A 30-day month has 30 x 24 x 60 = 43,200 minutes.

| Target | Allowed downtime per month | Per year (525,600 min) |
|---|---|---|
| 99.99% (ingestion) | 43,200 x 0.0001 = **4.32 min** (259 s) | 52.6 min |
| 99.90% (dashboards) | 43,200 x 0.001 = **43.2 min** | 8.76 h |

## How the design meets the targets

Production runs on three converged nodes, one in each
availability zone of Azure Denmark East. Each node runs every service: Caddy, the collect
gateway and the OTLP collector, a NATS JetStream server, an etcd member, a Patroni member with
PostgreSQL, HAProxy, Umami, the control plane, the replayer, the metrics writer, the jobs
runner, and node monitoring (Prometheus, Alertmanager, Grafana). Every service therefore has a
member in every zone.

- **Three zones for every path.** Each node accepts web events and OTLP metrics into NATS
  JetStream (one cluster, three replicas, one member per zone) and answers without touching
  PostgreSQL or Umami, and each node serves the console and the dashboards. Any one node, or a
  whole zone, can fail while the other two keep accepting and keep serving.
- **Health-checked DNS in front.** Azure Traffic Manager probes every node's port 80 (308
  healthy, 503 unhealthy, `deploy/caddy/assemble.sh`, tier `node`) and answers DNS with every
  healthy address (MultiValue). For `in` and `otlp` it asks `/`, the node's ingestion health,
  every 10 s; for `app` it asks `/core`, the node's dashboard health, every 30 s, so
  a node whose console path is unhealthy still takes ingestion. A browser that cannot connect
  to one address tries the next one from the same answer.
- **Dashboards cannot stop ingestion.** The queue holds web events for 72 hours, so an outage
  of the database or of Umami costs the dashboards only, and the backlog drains when they
  return. An event still queued after 72 hours is dropped, and a backlog large enough to fill
  the NATS file store stops ingestion, so the alerts on the queue's age and growth page well
  before either ([capacity.md](capacity.md), "Web events end to end").
- **A database quorum of three.** PostgreSQL under Patroni with three members, one per zone:
  one leader, one synchronous standby and one asynchronous replica. Patroni's DCS is the etcd
  cluster with three members, one per zone, so losing one zone keeps the election quorum (two
  of three) and a synchronous copy of every commit.
- **Backups in three zones.** pgBackRest writes to zone-redundant Blob Storage
  ([../deploy/backups.md](../deploy/backups.md)).
- **Measured from outside.** The prober runs on a separate virtual machine in Azure East US,
  outside the production region, and checks ingestion and the
  dashboards every 30 s ([../deploy/probe.md](../deploy/probe.md)).

## Windows per failure

"Configured" means the limit that follows from a setting.

| Failure | Ingestion impact | Dashboard impact | Source |
|---|---|---|---|
| One node lost | requests to that address fail until Traffic Manager drops it from `in` and `otlp`: three failed probes 10 s apart plus the 5 s timeout, then up to 10 s of cached answers, so **up to about 45 s** for a client holding the old answer. A browser that gets a refused or timed-out connection tries the next address of the same answer at once | requests to that address fail until it leaves `app`: 3 x 30 s plus the 10 s timeout and 30 s of cached answers, **up to about 130 s**; if it held the Patroni leader, promotion runs in parallel (below) | configured (`infra/tofu/modules/failover`) |
| One NATS member lost | none (R3, quorum 2 of 3) | none | configured |
| Patroni leader lost | none: ingestion does not touch PostgreSQL | writes and reads that need the leader stop until promotion: leader key TTL 30 s, plus up to one 10 s loop: **at most about 40 s** | configured (`core_patroni_dcs`) |
| One availability zone lost | as one node lost; NATS, etcd and Patroni keep quorum with the other two zones | as one node lost | configured |
| Database or Umami down on every node | none: the queue holds web events for 72 h (`WEB_EVENTS` max age) | down for the whole outage | configured |
| Rolling deploy | none: `deploy.yml` drains each node and waits 120 s before touching its edge services and 150 s before its core services | one switchover per run when the postgres image changes, a few seconds: the leader's node goes last | configured |
| etcd quorum lost (two nodes down) | ingestion continues on the remaining node only while its NATS server still has a JetStream quorum, which two lost nodes take away: ingest answers 503 | `failsafe_mode: true` keeps a leader primary only while it reaches every member, which two lost nodes take away | configured |
| Full NATS restart | ingest answers 503 until JetStream has quorum again; visitors seen earlier that day are counted again | none | configured |

## What the budgets absorb

Ingestion at 99.99% allows 259 s a month. At up to 45 s per node loss for a client that holds
the old answer, and with browsers moving to the next address at once, the budget absorbs
(259 / 45) = **5 whole node losses a month**. Database and Umami failures cost ingestion
nothing, because ingestion never waits on them.

Dashboards at 99.90% allow 2,592 s a month. At up to 130 s per node loss, that is
(2,592 / 130) = **19 node losses a month**, or (2,592 / 40) = 64 leader failovers, or a single
database outage of **43 minutes**.

These counts use configured windows. Resolver caching beyond the TTL, cold starts and host
contention add time, which the production drills measure.

## Dependencies the targets rest on

- **One Azure region.** The three availability zones of Denmark East are separate
  datacenters with their own power, cooling and network; the design tolerates the loss of any
  one of them. A region-wide event would stop the service; the backups stay readable in the
  region's zone-redundant storage, and [../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md)
  rebuilds from them.
- **Traffic Manager and Cloudflare DNS.** Failover depends on Traffic Manager's health checks
  and answers and on Cloudflare
  answering the CNAMEs. When every endpoint of a profile fails, Traffic Manager answers with
  all of them rather than none.
- **Resolver caching.** Resolvers and browsers can keep an address past the TTL. MultiValue
  answers carry every healthy address, so a client with a stale answer still holds working
  addresses beside the failed one.
- **The port-80 health check sees no TLS failure.** A broken certificate on port 443 leaves the
  node in DNS. The prober checks each node's certificate by its own address, and the
  `CertificateExpiringSoon` alert fires 14 days before expiry.
- **Web event drain rate.** Ingestion and delivery are two rates. Every node accepts events,
  but one replayer, the holder of the `replayer` lock, delivers them for the whole cluster to
  the `core_umami_processes` (2) Umami processes of its own node, so delivery does not grow with
  the node count ([capacity.md](capacity.md), "Web events end to end", says why and what
  delivery from every node would need). The delivery rate of a production node has not been
  measured. The local drill gave 280 events a second for one Umami process
  ([drills/2026-11-13.md](drills/2026-11-13.md)); scaled by the drill's 1.5x for two processes
  that is about 420 a second, an estimate. Local runs on 2026-11-17 on a contended host drained
  282 to 696 a second. Against the expected peak (50 a second) that leaves room; above the
  delivery rate, as in a long 10x burst (500 a second), events queue in `WEB_EVENTS` and the
  dashboards fall behind. The queue keeps an event at most 72 hours and then drops it, and a
  large backlog can fill the NATS file store first, which stops ingestion;
  `WebEventsDelayed`, `WebEventsBacklogGrowing` and `WebEventsNearDiscard` announce both
  ([../runbooks/alerts.md](../runbooks/alerts.md)).
- **Three database members.** With one member down the leader still has a synchronous
  standby; only with two members down does the leader run alone, and a further failure in that
  time loses what only the leader had, back to the last archived WAL (`archive_timeout` 60 s).
- **Shared nodes.** Every service of a node shares its 2 vCPUs and 8 GB, so a node that is
  overloaded by one service is slow for all of them. Traffic Manager takes a node whose health
  fails out of both profiles; [../deploy/opentofu.md](../deploy/opentofu.md) ("Why `Standard_B2as_v2`") states
  the memory budget the size rests on.
- **Billing.** A suspended subscription stops everything; [../deploy/prerequisites.md](../deploy/prerequisites.md)
  ("Account holders") sets the payment requirements.
