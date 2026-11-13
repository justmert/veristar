---
title: "Alerts runbook"
sidebarTitle: "Alerts"
description: "Every alert, its threshold, what it means and what to do."
---

Every alert of the platform, its threshold, what it means and what to do.
The rules live in `infra/ansible/roles/observability/files/rules/` and are
tested with `promtool test rules` (`rules/tests/`).

Severity `page` means act now. Severity `ticket` means act within a working
day.

The actions below are short; the full procedures are in the task runbooks
listed in [overview.md](overview.md), which also explains how to reach the nodes,
run `platformctl` and `jobs` on a core node, and read NATS state. The
fire-drill record at the end says which alerts have fired and resolved for
real, and when.

To reach the tools: `ssh -L 9090:127.0.0.1:9090 -L 3000:127.0.0.1:3000
ops@<core node>`, then Prometheus on `http://127.0.0.1:9090` and Grafana on
`http://127.0.0.1:3000` (dashboards in the folder Veristar).

## Where alerts show

Every firing alert is listed on the console's **Alerts** page,
`https://app.<BASE_DOMAIN>/operator/alerts`, for operators only. The control
plane reads every Alertmanager member live, and the page refreshes every 30
seconds, groups the alerts by severity, says how long each has been firing
and links each one to its section below. The operator tabs carry the number
of firing alerts, and every operator page shows a banner while an alert of
severity `page` fires. Details: [the operator guide](../operators/alerts.md).

That page is the alert channel. Alertmanager pushes nothing anywhere unless
a push channel is configured: with an operator input set it sends each group of alert
name and node to Discord, with a resolved message when the condition ends,
and the probe's Gatus sends its own messages for the status page checks and
the prober's results. Without I8 nobody is notified, so someone has to look
at the page.

The page also watches the alerting itself:

- **Alerting pipeline down**: the always-firing `Watchdog` is missing from
  Alertmanager. Prometheus is not evaluating rules or cannot reach
  Alertmanager, so alerts that should fire may be missing. Follow
  [Watchdog](#watchdog).
- **Alert source unreachable**: no Alertmanager member answered the control
  plane. The page then shows no list at all, never "all clear". Check
  `docker compose ps` in `/opt/veristar/observability` on the core nodes and
  the control plane's log for `alert_source_unreachable`.
- **N of M Alertmanager members did not answer**: the list comes from the
  members that answered, which each hold every alert; bring the named member
  back.

## Ingestion and edge

### IngestWebErrorRatioHigh
More than 1% of web events answered `rejected_unavailable` (the platform's
fault: queue publish not acknowledged, secrets or registry not loaded) for
5 minutes. Visitor-side refusals do not count.
Action: open the Ingestion dashboard and find the node. Check the gateway log
for `event not queued`; check NATS (NATSQuorumLost, NATSServerDown). If one
node is affected, drain it and restart its gateway
([edge-node-failure.md](edge-node-failure.md) steps 2 to 5).

### IngestOTLPErrorRatioHigh
More than 1% of OTLP points refused or failed by the collector for 5 minutes
(memory limiter, rate limits, unacknowledged JetStream publish).
Action: as above for the collector; `docker compose logs collector` on the
node. Rate-limit refusals of one tenant show as `rejected` in its token page.

### EdgeNodeNotReady
The gateway of an edge node does not answer scrapes, one of its readiness
checks fails, or its collector does not answer, for 2 minutes. Caddy's port
80 health already takes the node out of DNS.
Action: `ssh` to the node, `sudo docker compose ps` in
`/opt/veristar/edge`, read the failing check in `ops_ready_check` or
with `sudo docker compose exec caddy wget -qO- http://gateway:9090/readyz`.
Restart the failing service; if NATS on the node is the cause, see
NATSServerDown. Full steps: [edge-node-failure.md](edge-node-failure.md).

### VisitorSecretMissing
A gateway's `daily_secrets` readiness check fails for 1 minute: today's
visitor and ip-cluster secrets are not in the memory-only `edge_secrets`
bucket. Events are refused as unavailable meanwhile.
Action: check NATS quorum first (inhibited while NATSQuorumLost fires). The
gateways recreate the bucket themselves; if it stays, restart one gateway
and read its log for `visitor secret`.

### DBIPFileOld
The DB-IP file the gateway uses was built more than 45 days ago.
Action: run the monthly update ([dbip-update.md](dbip-update.md)): check the
month with `make geo-fetch GEO_MONTH=<YYYY-MM>`, then release and deploy a
gateway image built with that month. The file is inside the image, so a
`SIGHUP` alone does not load a new month on a deployed node. Attribution stays
as required by CC BY 4.0.

### CertificateExpiringSoon
A node serves a certificate for one of its hosts that expires within 14
days, read by the prober over a connection to the node's own address.
Action: check Caddy's log on that node for ACME errors (DNS-01 through
Cloudflare, an operator input.
`sudo docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile`
after fixing the cause; a node that cannot renew is drained. Full steps:
[certificates.md](certificates.md).

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

<a id="replayerlaghigh"></a><a id="metricswriterlaghigh"></a>

### ReplayerLagHigh, MetricsWriterLagHigh
More than 5,000 messages pending or unacknowledged for the replayer or the
metrics writer for 10 minutes.
Action: check that the leader consumes and how fast (Queues dashboard). If
Umami or the database is slow, fix that first; the consumers catch up by
themselves.

<a id="replayernotconsuming"></a><a id="metricswriternotconsuming"></a>

### ReplayerNotConsuming, MetricsWriterNotConsuming
No instance holds the consumer's lock for 2 minutes, or none is scraped.
Action: `docker compose ps` on both core nodes; start the service. If both
run, check the `locks` bucket and NATS.

### ReplayerUmamiProcessIdle
One of the umami processes of a core node (`core_umami_processes`) stored no
event for 15 minutes while the replayer leader on that node sent more than
one event a second to the others. The visitors routed to that process wait
in their lanes and their events queue in `WEB_EVENTS`; nothing is lost.
Action: `docker compose ps` on the node and `docker compose logs umami-<n>`
(process 0 is the service `umami`, process n is `umami-<n+1>`); restart it.

### ReplayerVisitStateNearlyFull, ReplayerVisitStateFull
The `visit_state` bucket holds more than 80 % of its byte limit
(`natsx.VisitStateMaxBytes`, 384 MiB) for 10 minutes, or it is full and the
replayer skipped writes in the last 5 minutes. It holds a
`sent.<event id>` key per delivered event for 24 hours, so it fills at about
4 to 5 million delivered events a day. When full, delivery goes on, but each
event is stored without its duplicate guard (a replayer crash before the ack
can store it twice) and without visit continuity (the visitor's next event
starts a new visit). Nothing is lost and the bucket drains as keys expire.
Action: if this is steady traffic, raise `VisitStateMaxBytes` and
`edge_nats_max_memory_store` together as [capacity.md](capacity.md) describes,
then `platformctl nats apply`; the numbers are the gauges
`replayer_visit_state_bytes`, `replayer_visit_state_max_bytes` and the counter
`replayer_visit_state_writes_skipped_total`.

### DLQNotEmpty
`WEB_DLQ` or `API_DLQ` holds messages: events or batches were rejected
permanently downstream.
Action: list the messages and their reasons with `platformctl dlq list`, fix
the cause, then `platformctl dlq replay` what is now storable and
`platformctl dlq purge --yes` what never will be
([queue-backlog-and-dlq.md](queue-backlog-and-dlq.md)). The alert resolves
when both streams are empty; without action the messages expire with the
stream (7 days web, 30 days API).

### NATSQuorumLost
Fewer than two NATS servers report JetStream, or no meta leader exists, for
1 minute. Ingestion stops (events are refused as unavailable).
Action: start the stopped servers first (`docker compose start nats` on the
edge nodes). Do not wipe a server's store while two are down.

### NATSServerDown
One NATS server is unhealthy or its exporter cannot read it for 2 minutes.
Action: restart it on that node; it rejoins and catches up.

## Database

<a id="etcdnoleader"></a><a id="etcdmembersdown"></a>

### EtcdNoLeader, EtcdMembersDown
An etcd member sees no leader, or fewer than two of three answer, for 1
minute. Patroni cannot renew its leader key; the primary demotes itself
after its TTL.
Action: start the stopped members on the edge nodes. Check time sync.

### PatroniNoLeader
No PostgreSQL member is primary for 1 minute. The control plane, the writer
and jobs fail.
Action: `patronictl list` in the postgres container; check etcd first, then
[database-failover.md](database-failover.md).

### ReplicationLagHigh
The replica replays more than 60 seconds behind for 5 minutes.
Action: check the replica's disk and network; a long-running query on the
replica can hold replay.

### BackupTooOld
The newest pgBackRest backup finished more than 26 hours ago, or no backup
is known (the textfile written hourly by `sa-pgbackrest-textfile`).
Action: on the leader's node, `sudo docker compose exec postgres pgbackrest
--stanza=sa info`; check the timers (`systemctl list-timers 'sa-pgbackrest-*'`)
and the last run (`journalctl -u sa-pgbackrest-diff.service`); run a backup
by hand with `sudo docker compose exec postgres pgbackrest --stanza=sa
--type=full backup` and fix the schedule. If the run failed on a refused
delete of an interrupted backup, see docs/deploy/backups.md section 1.

### WALArchivingFailing
WAL archiving failed in the last 15 minutes, or the primary archived nothing
for 15 minutes.
Action: `pg_stat_archiver` shows the last failed WAL; check that the node
reaches the storage account (`pgbackrest --stanza=sa check` in the postgres
container) and that the core identity still holds its role on the container
(`az role assignment list --scope <container id>`). Unarchived WAL piles up on disk.

## Platform

### DiskUsageHigh
A filesystem is more than 80% full for 10 minutes.
Action: find what grew (`du -xh / | sort -h | tail`): Docker images, logs,
NATS store, PostgreSQL. Prune old images; grow the volume if data grew.

### ServiceDown
A control plane, replayer, writer, jobs, prober, node, postgres, patroni,
caddy or haproxy target does not answer scrapes for 2 minutes. Caddy and
HAProxy are scraped through the node's observability relay, so a stopped
relay shows here as well.
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
Action: jobs logs for `umami-rotate`. Once the cause is fixed, restart the
jobs daemon (`sudo docker compose restart jobs`) so its leader runs the task.
`jobs run umami-rotate` refuses while a daemon leads (`a jobs daemon is
leading`); to run it by hand, stop `jobs` on both core nodes first
([overview.md](overview.md), "Running a jobs task by hand").

### APIRollupOrphanedMarks
The API rollup found pending hours whose minute partition was already
dropped by retention (`jobs_api_rollup_orphaned_marks_total` rose within the
last hour). The hourly rows of those hours were kept as they were and can no
longer be recomputed from minute data, so they may miss late points.
Action: check that `core_api_minute_retention_days` is at least 8 (the role
refuses less) and that the rollup is not far behind (`jobs` logs, `task
failed` for `api-rollup`, `jobs_api_rollup_last_batch_hours`). The jobs log
line `api rollup` names the counts; list the affected tenants and hours from
it and record them in the incident, since their hourly and daily figures
stay as they were.

### APIRollupDriftedHours
The API rollup found hours whose hourly rows disagreed with their minute rows
although no pending mark existed (`jobs_api_rollup_drifted_hours_total`
rose). The rollup has marked them again and recomputes them on its next pass,
so the figures heal on their own; the alert means a mark was lost, which is a
bug.
Action: confirm the next pass cleared it (the counter stops rising, the alert
resolves within the hour), then open an issue with the jobs log lines of the
pass (tenants and hours) and recent metrics writer restarts or errors.

### HostRebootRequired
Installed updates (a kernel or libc fix from unattended upgrades) have waited
12 hours for a reboot on this node (`/var/run/reboot-required`, written to
`node_reboot_required` by `sa-reboot-required.timer`). Until the reboot the
fix does not apply, and the patch target is 24 hours.
Action: `ansible-playbook -i inventories/<env>/ playbooks/reboot.yml`, which
drains, hands over the Patroni leader and reboots one node at a time
([security-patch.md](security-patch.md) section 6a).

### Watchdog
Always firing, by design. Alertmanager sends it every minute to the probe's
Gatus (external endpoint `monitoring_watchdog`, through the probe's Caddy,
which accepts that push only from the core nodes' addresses). Gatus alerts to
Discord when no push arrived for 5 minutes: then both Prometheus servers,
both Alertmanagers or the whole core tier are down, or the core tier cannot
reach the probe, and no other alert can be sent from inside.
The console's Alerts page uses the same signal: while no Alertmanager
member holds `Watchdog`, it shows "Alerting pipeline down" instead of the
all-clear, with or without a probe.
Action when Gatus or the console reports it missing: check the core nodes
([database-failover.md](database-failover.md) if both are down), then
`docker compose ps` in `/opt/veristar/observability` on each core
node. Never silence `Watchdog` in Alertmanager: a silence stops the heartbeat
and Gatus alerts after 5 minutes. Without a probe host in the inventory (the
probe bundle of docs/deploy/probe.md has no public listener) the route goes
to a receiver with no target, so nothing outside watches it.

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
The staging drill with real Discord delivery waits for an operator input.

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
