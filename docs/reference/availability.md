---
title: "Availability statement"
description: "The availability targets, the failover behaviour they rest on, and what has been measured."
---

Targets (RFP 3.4): ingestion 99.99%, dashboards 99.90%. This page shows how the Azure design
meets them: the arithmetic, the failover windows the configuration sets, and what each failure
costs. Status on 2026-11-13: the windows below follow from the configuration
(`infra/tofu/modules/failover`, Patroni, NATS); they are measured on staging by the this stage
drills before the targets are claimed. The local drill record is
`docs/reference/drills/2026-11-13.md`.

## Budgets

A 30-day month has 30 x 24 x 60 = 43,200 minutes.

| Target | Allowed downtime per month | Per year (525,600 min) |
|---|---|---|
| 99.99% (ingestion) | 43,200 x 0.0001 = **4.32 min** (259 s) | 52.6 min |
| 99.90% (dashboards) | 43,200 x 0.001 = **43.2 min** | 8.76 h |

## How the design meets the targets

- **Three zones for ingestion.** The three edge nodes run in availability zones 1, 2 and 3 of
  Azure Sweden Central, each with its own public address. Each accepts web events and OTLP
  metrics into NATS JetStream (one cluster, three replicas, one member per zone) and answers
  without touching PostgreSQL or Umami. Any one edge node, or a whole zone, can fail while the
  other two keep accepting and the queue keeps quorum.
- **Health-checked DNS in front.** Azure Traffic Manager probes every node's port 80 health
  answer (308 healthy, 503 unhealthy, `deploy/caddy/assemble.sh`) and answers DNS with every
  healthy address (MultiValue). For `in` and `otlp` it probes every 10 s; for `app` and `web`
  every 30 s. A browser that cannot connect to one address tries the next one from the same
  answer.
- **The core tier cannot stop ingestion.** The queue holds web events for 72 hours, so a full
  core outage costs the dashboards only, and the backlog drains when the core returns.
- **Two database nodes in two zones.** PostgreSQL under Patroni with synchronous replication;
  etcd for leader election runs on the three edge nodes, one per zone, so losing one zone keeps
  the election quorum.
- **Backups in three zones.** pgBackRest writes to zone-redundant Blob Storage
  ([../deploy/backups.md](../deploy/backups.md)).
- **Measured from outside.** The prober runs outside Azure and checks ingestion and the
  dashboards every 30 s ([../deploy/probe.md](../deploy/probe.md)).

## Windows per failure

"Configured" means the limit that follows from a setting.

| Failure | Ingestion impact | Dashboard impact | Source |
|---|---|---|---|
| One edge node lost | requests to that address fail until Traffic Manager drops it: three failed probes 10 s apart plus the 5 s timeout, then up to 10 s of cached answers, so **up to about 45 s** for a client holding the old answer. A browser that gets a refused or timed-out connection tries the next address of the same answer at once | none | configured (`infra/tofu/modules/failover`) |
| One core node lost | none | requests to that address fail until Traffic Manager drops it: 3 x 30 s plus the 10 s timeout and 30 s of cached answers, **up to about 130 s**; if it held the Patroni leader, promotion runs in parallel (below) | configured |
| One NATS member lost | none (R3, quorum 2 of 3) | none | configured |
| Patroni leader lost | none: the edge does not touch PostgreSQL | writes and reads that need the leader stop until promotion: leader key TTL 30 s, plus up to one 10 s loop: **at most about 40 s** | configured (`core_patroni_dcs`) |
| One availability zone lost | as one edge node lost; NATS and etcd keep quorum with the other two zones | as one core node lost if the zone holds a core node (zones 1 and 2) | configured |
| Whole core tier down | none: the edge queues for 72 h (`WEB_EVENTS` max age) | down for the whole outage | configured |
| Rolling deploy | none: `deploy.yml` drains each node and waits 120 s (edge) or 150 s (core) before touching it | one switchover per core node with a changed postgres image, a few seconds each | configured |
| etcd quorum lost (two edge nodes down, or the edge tier cut off from the core tier) | none: the edge does not touch etcd for ingestion | none while the Patroni leader still reaches every member through the REST API: `failsafe_mode: true` keeps it primary without the DCS | configured |
| Full NATS restart | ingest answers 503 until JetStream has quorum again; visitors seen earlier that day are counted again | none | configured |

## What the budgets absorb

Ingestion at 99.99% allows 259 s a month. At up to 45 s per edge node loss for a client that
holds the old answer, and with browsers moving to the next address at once, the budget absorbs
(259 / 45) = **5 whole edge losses a month**. Database and core failures cost ingestion nothing,
because the edge never waits on them.

Dashboards at 99.90% allow 2,592 s a month. At up to 130 s per core node loss, that is
(2,592 / 130) = **19 core node losses a month**, or (2,592 / 40) = 64 leader failovers, or a
single core-tier outage of **43 minutes**.

These counts use configured windows. Resolver caching beyond the TTL, cold starts and host
contention add time, which the staging drills measure.

## Dependencies the targets rest on

- **One Azure region.** The three availability zones of Sweden Central are separate
  datacenters with their own power, cooling and network; the design tolerates the loss of any
  one of them. A region-wide event would stop the service; the backups stay readable in the
  region's zone-redundant storage, and [../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md)
  rebuilds from them.
- **Traffic Manager and Cloudflare DNS.** Failover depends on Traffic Manager's health checks
  and answers (Microsoft's service level agreement states 99.99 %) and on Cloudflare
  answering the CNAMEs. When every endpoint of a profile fails, Traffic Manager answers with
  all of them rather than none.
- **Resolver caching.** Resolvers and browsers can keep an address past the TTL. MultiValue
  answers carry every healthy address, so a client with a stale answer still holds working
  addresses beside the failed one.
- **The port-80 health check sees no TLS failure.** A broken certificate on port 443 leaves the
  node in DNS. The prober checks each node's certificate by its own address, and the
  `CertificateExpiringSoon` alert fires 14 days before expiry.
- **Web event drain rate.** Each core node runs `core_umami_processes` (2) Umami processes, one
  per vCPU; the replayer spreads visitors over them. Scaled from the local measurements, two
  processes drain about 420 events a second, far above the expected peak (50 a second) and
  below the 10x burst profile (500 a second): a burst queues in JetStream and drains after it,
  so ingestion stays up and the dashboards catch up. The rate on the real node size is measured
  by the staging load drill.
- **Two database nodes.** While the replica is down the leader runs alone; a second failure in
  that time loses what only the leader had, back to the last archived WAL (`archive_timeout`
  60 s).
- **Billing.** A suspended subscription stops everything; [../deploy/prerequisites.md](../deploy/prerequisites.md)
  ("Account holder, billing and renewal") sets the payment requirements.
