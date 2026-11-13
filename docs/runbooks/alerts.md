# Alerts runbook

Every alert of the platform, its threshold, what it means and what to do.
The rules live in `infra/ansible/roles/observability/files/rules/` and are
tested with `promtool test rules` (`rules/tests/`). Alertmanager sends them to
Discord an operator input as one message per group of alert name and node, with a
resolved message when the condition ends. Gatus sends its own messages for
the status page checks and the prober's results.

Severity `page` means act now. Severity `ticket` means act within a working
day.

To reach the tools: `ssh -L 9090:127.0.0.1:9090 -L 3000:127.0.0.1:3000
ops@<core node>`, then Prometheus on `http://127.0.0.1:9090` and Grafana on
`http://127.0.0.1:3000` (dashboards in the folder Stellar Analytics).

## Ingestion and edge

### IngestWebErrorRatioHigh
More than 1% of web events answered `rejected_unavailable` (the platform's
fault: queue publish not acknowledged, secrets or registry not loaded) for
5 minutes. Visitor-side refusals do not count.
Action: open the Ingestion dashboard and find the node. Check the gateway log
for `event not queued`; check NATS (NATSQuorumLost, NATSServerDown). If one
node is affected, drain it (`/drain/draining` on the node, see the deploy
runbook) and restart its gateway.

### IngestOTLPErrorRatioHigh
More than 1% of OTLP points refused or failed by the collector for 5 minutes
(memory limiter, rate limits, unacknowledged JetStream publish).
Action: as above for the collector; `docker compose logs collector` on the
node. Rate-limit refusals of one tenant show as `rejected` in its token page.

### EdgeNodeNotReady
The gateway of an edge node does not answer scrapes, one of its readiness
checks fails, or its collector does not answer, for 2 minutes. Caddy's port
80 health already takes the node out of DNS.
Action: `ssh` to the node, `docker compose ps` in
`/opt/stellar-analytics/edge`, read the failing check in `ops_ready_check` or
`curl -s 127.0.0.1` on the gateway's `/readyz` from inside the compose
network. Restart the failing service; if NATS on the node is the cause, see
NATSServerDown.

### VisitorSecretMissing
A gateway's `daily_secrets` readiness check fails for 1 minute: today's
visitor and ip-cluster secrets are not in the memory-only `edge_secrets`
bucket. Events are refused as unavailable meanwhile.
Action: check NATS quorum first (inhibited while NATSQuorumLost fires). The
gateways recreate the bucket themselves; if it stays, restart one gateway
and read its log for `visitor secret`.

### DBIPFileOld
The DB-IP file the gateway uses was built more than 45 days ago.
Action: run the monthly update (`make geo-fetch`, rebuild and deploy the
gateway image, or replace the file and send SIGHUP). Attribution stays as
required by CC BY 4.0.

### CertificateExpiringSoon
A node serves a certificate for one of its hosts that expires within 14
days, read by the prober over a connection to the node's own address.
Action: check Caddy's log on that node for ACME errors (DNS-01 through
bunny.net, an operator input. `docker compose exec caddy caddy reload` after fixing
the cause; a node that cannot renew is drained.

### CardinalityCapHit
Points went to the overflow route or service of a tenant in the last 15
minutes: the tenant has more than 300 routes or 50 services in a UTC day.
Action: none urgent. Find the tenant in `metricswriter` logs and tell them
their routes are not normalised (docs/onboarding). The overflow keeps the
totals right.

## Queues

### QueueOldestMessageTooOld
`WEB_EVENTS` or `API_METRICS` held messages for 10 minutes while its first
sequence did not move: the oldest unprocessed message is at least 10 minutes
old, which is the freshness budget.
Action: check ReplayerNotConsuming / MetricsWriterNotConsuming and the
consumer's log. A replayer stuck on Umami errors shows in
`replayer_send_duration_seconds{result!="ok"}`.

### ReplayerLagHigh, MetricsWriterLagHigh
More than 5,000 messages pending or unacknowledged for the replayer or the
metrics writer for 10 minutes.
Action: check that the leader consumes and how fast (Queues dashboard). If
Umami or the database is slow, fix that first; the consumers catch up by
themselves.

### ReplayerNotConsuming, MetricsWriterNotConsuming
No instance holds the consumer's lock for 2 minutes, or none is scraped.
Action: `docker compose ps` on both core nodes; start the service. If both
run, check the `locks` bucket and NATS.

### DLQNotEmpty
`WEB_DLQ` or `API_DLQ` holds messages: events or batches were rejected
permanently downstream.
Action: read the messages' `Sa-Dlq-Reason` header (`nats stream view
WEB_DLQ` as platformctl). Fix the cause, then replay or purge them; the alert
stays until the stream is empty.

### NATSQuorumLost
Fewer than two NATS servers report JetStream, or no meta leader exists, for
1 minute. Ingestion stops (events are refused as unavailable).
Action: start the stopped servers first (`docker compose start nats` on the
edge nodes). Do not wipe a server's store while two are down.

### NATSServerDown
One NATS server is unhealthy or its exporter cannot read it for 2 minutes.
Action: restart it on that node; it rejoins and catches up.

## Database

### EtcdNoLeader, EtcdMembersDown
An etcd member sees no leader, or fewer than two of three answer, for 1
minute. Patroni cannot renew its leader key; the primary demotes itself
after its TTL.
Action: start the stopped members on the edge nodes. Check time sync.

### PatroniNoLeader
No PostgreSQL member is primary for 1 minute. The control plane, the writer
and jobs fail.
Action: `patronictl list` in the postgres container; see the HA runbook
(this stage). Check etcd first.

### ReplicationLagHigh
The replica replays more than 60 seconds behind for 5 minutes.
Action: check the replica's disk and network; a long-running query on the
replica can hold replay.

### BackupTooOld
The newest pgBackRest backup finished more than 26 hours ago, or no backup
is known (the textfile written hourly by `sa-pgbackrest-textfile`).
Action: `pgbackrest info` in the postgres container; run the backup by hand
and fix the schedule. Until this stage configures backups this alert fires.

### WALArchivingFailing
WAL archiving failed in the last 15 minutes, or the primary archived nothing
for 15 minutes.
Action: `pg_stat_archiver` shows the last failed WAL; check object storage
credentials and reachability an operator input. Unarchived WAL piles up on disk.

## Platform

### DiskUsageHigh
A filesystem is more than 80% full for 10 minutes.
Action: find what grew (`du -xh / | sort -h | tail`): Docker images, logs,
NATS store, PostgreSQL. Prune old images; grow the volume if data grew.

### ServiceDown
A control plane, replayer, writer, jobs, prober, node, postgres or patroni
target does not answer scrapes for 2 minutes.
Action: `docker compose ps` on that node and start it; read its log.

### UmamiHeartbeatFailing
The control plane's Umami readiness check fails for 2 minutes. Tenant web
dashboards and provisioning are down; events wait in `WEB_EVENTS`.
Action: `docker compose logs umami` on the core node; check its database.

### WebAggregationStale
`aggregate-web` has not succeeded for over two hours, or never in the
running jobs leader for 15 minutes. Operator web views go stale.
Action: jobs logs (`task failed`); usually Umami or the database.

### UmamiPasswordRotationNotRun
After 01:00 UTC today's Umami password rotation is not recorded, or no jobs
leader reports it for 15 minutes. Yesterday's Umami tokens stay valid.
Action: jobs logs for `umami-rotate`; run `jobs run umami-rotate` once the
cause is fixed.

## Probe

### ProbeIngestionFailing
The probe's web event (gateway 202) or OTLP metric (collector 200) failed for
2 minutes. This is what the ingestion objective (99.99%) counts.
Action: the Gatus page shows which part; go to the ingestion alerts above.

### ProbeFreshnessFailing
Probe events accepted more than 10 minutes ago are not visible through the
machine API (`/web/hourly` or `/api/hourly`). Counts against the dashboard
and pipeline objective, not ingestion.
Action: check the replayer or writer alerts and Umami.

### ProbeDashboardFailing
The console, the Umami host or the authenticated machine API call fails from
the probe for 2 minutes (dashboard objective, 99.90%).
Action: check the core nodes' Caddy and the control plane.

### ProbeNodeCheckFailing
A node fails by its own address: TLS handshake, certificate for the host, or
HTTP below 500. The hostname may still work through the other nodes.
Action: as EdgeNodeNotReady or CertificateExpiringSoon for that node.

## Fire-drills

Run on the local test stack (`tests/observability/up.sh`, then
`tests/observability/drill.sh <drill>`), with the test receiver standing in
for Discord. Each drill causes the condition for real and waits for the
firing and the resolved message. Results of 2026-11-13 below; "fired" and "resolved" are the times the
test receiver got the Alertmanager or Gatus message. The drills were cut
short when the shared host became overloaded, so the last group was not run.
The staging drill with real Discord delivery waits for inputs I7 and I8.

| Alert | Drill | Result |
|---|---|---|
| EdgeNodeNotReady, ProbeNodeCheckFailing, Gatus `edge-2 in.p17.test` | stop the gateway of edge-2 | fired 01:28:46, resolved 01:33:00 to 01:33:36; the `in` hostname check and ingestion stayed up |
| CertificateExpiringSoon, Gatus `edge-2 in.p17.test` | serve a 7-day certificate on edge-2 | fired 01:34:48, resolved 01:39:48 |
| DLQNotEmpty | publish one message to `dlq.web.drill`, then purge | fired 01:40:29, resolved 01:45:25 |
| EtcdMembersDown, EtcdNoLeader | stop two of three etcd members | fired 01:47:20, resolved 01:52:20 |
| NATSServerDown | stop one NATS server | fired 02:13:44, resolved 02:18:40 (first run timed out: the rule read `gnatsd_healthz_status`, which the exporter drops when the server is unreachable; fixed) |
| ProbeFreshnessFailing, Gatus `freshness web` | stop replayer, metrics writer and jobs | web freshness failed after 62 s and API freshness after 92 s (limit 45 s locally) while ingestion, console, Umami host and machine API checks stayed up; resolved 01:44:05 |
| ReplayerNotConsuming, MetricsWriterNotConsuming, ServiceDown | same | fired, resolved 01:45:11 |
| QueueOldestMessageTooOld | same | fired 01:36:27, resolved 01:46:28 |
| ReplayerLagHigh, MetricsWriterLagHigh | same, plus 5,100 web events and 5,100 OTLP requests | fired by 01:43:44, resolved 01:48:41 |
| WebAggregationStale, UmamiPasswordRotationNotRun | same (no jobs leader) | fired by 01:43:44, resolved 01:48:41 |
| DiskUsageHigh | none needed: the test host's root filesystem is 94% full | fires for real; resolution only in promtool tests |
| PatroniNoLeader, BackupTooOld | none possible: no Patroni or pgBackRest in the test stack | fire for real (absent); resolution only in promtool tests |
| CardinalityCapHit | 310 routes in one request | not fired: the routes were numeric and normalised into one; the drill now uses letters and was not rerun |
| WALArchivingFailing, UmamiHeartbeatFailing, ProbeDashboardFailing, NATSQuorumLost, ProbeIngestionFailing, IngestWebErrorRatioHigh, IngestOTLPErrorRatioHigh, VisitorSecretMissing | drills `wal`, `umami`, `nats-quorum` | not run: stopped for the shared host's load; promtool tests only |
| DBIPFileOld, ReplicationLagHigh | no older DB-IP file and no replica locally | promtool tests only |
