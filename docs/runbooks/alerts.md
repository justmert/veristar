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

That page is the alert channel. Prometheus evaluates every rule,
Alertmanager holds every alert, and the Alerts page, Grafana and the status
page show them. A Discord receiver is optional: with a webhook configured,
Alertmanager also sends each group of alert name and node to Discord, with a
resolved message when the condition ends, and the probe's Gatus sends its own
messages for the status page checks and the prober's results.

### Adding a receiver

Put the Discord webhook into the environment's sops file and converge; no
other change is needed:

```sh
sops --config infra/secrets/.sops.yaml edit infra/secrets/production/secrets.enc.yaml
# add: alertmanager_discord_webhook_url: https://discord.com/api/webhooks/...
#      gatus_discord_webhook_url: https://discord.com/api/webhooks/...
make production-deploy SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<release-images.json>
```

The observability role then writes `alertmanager.yml` with the `discord`
receiver (and Gatus gets its alerting block when the probe bundle is
rendered again). Removing the key removes the receiver at the next converge.

The page also watches the alerting itself:

- **Alerting pipeline down**: the always-firing `Watchdog` is missing from
  Alertmanager. Prometheus is not evaluating rules or cannot reach
  Alertmanager, so firing conditions can go unreported. Follow
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
Cloudflare, with the environment's Cloudflare API token).
`sudo docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile`
after fixing the cause; a node that cannot renew is drained. Full steps:
[certificates.md](certificates.md).

### CardinalityCapHit
Points went to the overflow route or service of a tenant in the last 15
minutes: the tenant has more than 300 routes or 50 services in a UTC day.
Action: none urgent. Find the tenant in `metricswriter` logs and tell them
their routes are not normalised
([what gets dropped](../onboarding/api/what-gets-dropped.md)). The overflow
keeps the totals right.

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

<a id="webeventsdelayed"></a><a id="webeventsneardiscard"></a><a id="webeventsbackloggrowing"></a>

### WebEventsDelayed, WebEventsNearDiscard, WebEventsBacklogGrowing
`WEB_EVENTS` keeps an accepted event at most 72 hours (stream max age, discard old). An event
still waiting then is dropped by the stream without an error anywhere: it never reaches Umami and
the gateway already answered 202. These alerts read `replayer_oldest_pending_age_seconds`, the
publish time of the oldest message the replayer leader holds; in a backlog that is the oldest
message of the stream, because the leader fetches in stream order.

- `WebEventsDelayed` (ticket): the oldest held event is more than an hour old for 10 minutes.
  The dashboards are an hour or more behind.
- `WebEventsBacklogGrowing` (ticket): more than 10,000 events wait and the backlog grew over the
  last hour for 30 minutes. The gateways accept faster than the replayer delivers, so it will not
  drain on its own.
- `WebEventsNearDiscard` (page): the oldest held event is more than 48 hours old. In less than
  24 hours the stream starts dropping events.

The backlog also takes disk: every NATS server keeps a replica of `WEB_EVENTS` in its file store
(`edge_nats_max_file_store`, 20G per server), which it shares with every other stream. A full
store refuses new messages on every stream, so ingestion then answers 503.

Action: follow [queue-backlog-and-dlq.md](queue-backlog-and-dlq.md) ("Backlog"). If Umami or the
database is down or slow, fix that first. If everything is healthy and the backlog still grows,
traffic is above what the replayer delivers: the measured rates and the levers are in
[../reference/availability.md](../reference/availability.md) ("Web event drain rate") and
[capacity.md](capacity.md). Before 72 hours, write down the oldest timestamp; events older than
that are lost and the tenants must be told.

<a id="replayernotconsuming"></a><a id="metricswriternotconsuming"></a>

### ReplayerNotConsuming, MetricsWriterNotConsuming
No replayer owns a web event partition, or no metrics writer holds its lock,
for 2 minutes, or none is scraped.
Action: `docker compose ps` on every core node; start the service. If all
run, check the `locks` bucket and NATS.

<a id="replayerpartitionsunowned"></a>

### ReplayerPartitionsUnowned
Fewer of the 32 `WEB_EVENTS` partitions than all have an owner for 5 minutes
(`replayer_partitions` minus the sum of `replayer_partitions_owned`). The
visitors of the unowned partitions wait; the others are delivered. A replayer
that stops hands its partitions over within seconds, and a killed one's are
taken within about a minute (15 s lock ttl and at most the 30 s ack wait).
Action: `docker compose ps replayer` and `docker compose logs --since 15m
replayer` on every core node. Each replayer logs `partition assignment
changed` with the members it sees and `lock acquired` for each partition it
takes. A replayer that cannot read the `locks` bucket logs `partition
membership not refreshed`; check NATS quorum
([edge-node-failure.md](edge-node-failure.md)). The partitions of a node that
stays down are taken by the others after its member key expires.

### ReplayerUmamiProcessIdle
One of the umami processes of a core node (`core_umami_processes`) stored no
event for 15 minutes while the replayer on that node sent more than one event
a second to the others. The visitors routed to that process wait
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

### EtcdDatabaseSpaceHigh
The etcd database file on a member uses more than half of its quota
(`etcd_mvcc_db_total_size_in_bytes` over `etcd_server_quota_backend_bytes`,
2 GiB) for 15 minutes. Hourly compaction and the weekly defrag
(`sa-etcd-defrag.timer`) keep it at a few megabytes, so one of them stopped.
At the quota etcd raises [EtcdNoSpaceAlarm](#etcdnospacealarm) and refuses
every write.
Action: check that the etcd command carries `--auto-compaction-mode=periodic`
and `--auto-compaction-retention=1h` (`sudo docker compose -f
/opt/veristar/edge/compose.yml config etcd`) and that the log shows
`completed auto periodic compaction` within the last two hours (`sudo docker
compose logs --since 2h etcd` in `/opt/veristar/edge`). Check the last defrag
run (`sudo journalctl -u sa-etcd-defrag.service`). Then compact and
defragment by hand: [etcd-maintenance.md](etcd-maintenance.md).

### EtcdNoSpaceAlarm
etcd raised the `NOSPACE` alarm: a member's database file reached the quota,
and the whole cluster refuses every write until the alarm is cleared, even
after space is freed. Patroni cannot renew its leader key, so switchovers,
failovers, `deploy.yml` and `reboot.yml` fail; failsafe mode keeps the
current primary running.
Action: follow [etcd-maintenance.md](etcd-maintenance.md) to the end,
including `alarm disarm`; the alert resolves at the next scrape once the
alarm is gone.

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
delete of an interrupted backup, see
[the storage account](../deploy/backups.md#1-the-storage-account).

### BackupJobFailed
The newest run of a backup unit (`sa-pgbackrest-full`, `-diff` or `-verify`)
across the database nodes failed, for 5 minutes. Every run that was not
skipped records its end and result (`sa_backup_last_run_success`, written by
`sa-backup-result` when the unit stops), so the failure stays on record when
the Patroni leader moves, while [SystemdUnitFailed](#systemdunitfailed)
clears once the old leader skips its next run. The `node` label names the
node of the failed run.
Action: on that node, `sudo journalctl -u <unit>` for the error, then follow
[BackupTooOld](#backuptooold) for a backup and
[WALArchivingFailing](#walarchivingfailing) for a failed `check`. Once the
cause is fixed, run the unit again on the current leader (`sudo systemctl
start <unit>`); its success clears the alert.

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
leading`); to run it by hand, stop `jobs` on every core node first
([overview.md](overview.md), "Running a jobs task by hand").

### APIRollupOrphanedMarks
The API rollup found pending hours whose minute partition was already
dropped by retention (`jobs_api_rollup_orphaned_marks_total` rose within the
last hour). The hourly rows of those hours were kept as they were and can no
longer be recomputed from minute data, so they can miss late points.
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
bug, or, right after the upgrade to the release with migration 00040, that a
jobs daemon of the earlier release rolled up an hour without its traffic class
(the first check of a new jobs process looks for exactly that).
Action: confirm the next pass cleared it (the counter stops rising, the alert
resolves within the hour), then open an issue with the jobs log lines of the
pass (tenants and hours) and recent metrics writer restarts or errors.

### APIRollupParkedHours
The database refused to recompute a pending hour of the API rollup
(`jobs_api_rollup_parked_hours_total` rose; `jobs_api_rollup_parked_hours`
is how many are parked now). The usual cause is a data error: the hour's
request counts add up past the 64-bit integer range, which only absurd
counts from one tenant's SDK can reach. The rollup kept the mark in
`api.rollup_pending` with `parked_at` and `parked_error` set, carried on
with every other hour, and no longer claims it; the hour stays pending, so
dashboards read it from its minute rows and retention keeps them.
Action: list the parked hours and their errors:

```sql
select tenant_id, hour, parked_at, parked_error
from api.rollup_pending where parked_at is not null order by hour;
```

Look at the tenant's minute rows of that hour (`api.metrics_minute`, the
largest `count` values) and its metrics writer log. Delete the rows that
cannot be true, record what was removed in the incident, then clear the
mark so the next pass recomputes the hour and its day:

```sql
update api.rollup_pending set parked_at = null, parked_error = null
where tenant_id = '<tenant>' and hour = '<hour>';
```

The alert resolves an hour after the last park; `jobs_api_rollup_parked_hours`
drops to 0 after the next pass.

### MCPToolFailuresHigh
More than a fifth of the MCP tool calls on a control plane failed on the
server side for 15 minutes: `internal`, `unavailable` (Umami or the database)
or `timeout` outcomes of `controlplane_mcp_tool_calls_total`, with at least
one such failure in about three minutes. Bad input from a client never
counts. AI clients get an error result for those calls; nothing else is
affected.
Action: the control plane log lines `mcp tool failed` name the tool and the
error; `UmamiHeartbeatFailing` or a database alert at the same time is the
usual cause, and fixing it clears this one. Without one, follow
[mcp-access.md](mcp-access.md), "Investigating failing calls".

### HostRebootRequired
Installed updates (a kernel or libc fix from unattended upgrades) have waited
12 hours for a reboot on this node (`/var/run/reboot-required`, written to
`node_reboot_required` by `sa-reboot-required.timer`). Until the reboot the
fix does not apply, and the patch target is 24 hours.
Action: `ansible-playbook -i inventories/<env>/ playbooks/reboot.yml`, which
drains, hands over the Patroni leader and reboots one node at a time
([security-patch.md](security-patch.md) section 6a).

### SystemdUnitFailed
A platform unit (`sa-*`) on this node has been in the failed state for 15
minutes (`sa_systemd_unit_failed`, written every 5 minutes by
`sa-systemd-failed.timer`). The `unit` label names it. The usual ones are the
backup units on the Patroni leader (`sa-pgbackrest-full`, `-diff`, or
`-verify`, which runs `pgbackrest verify` and then `check`) and the
start-at-boot units (`sa-core-start`, `sa-edge-start`,
`sa-observability-start`), which retry a failed start up to six times
before they stay failed, and `sa-etcd-defrag`, which fails without
defragmenting when a member of the etcd cluster is unhealthy. On a replica the backup units skip their run
(`Result=exec-condition`), which is not a failure; a backup unit failed on a
replica means its leader check got neither 200 nor 503 from Patroni's
`/primary`, so the node's Patroni API is down or refuses the node's
certificate.
Action: `sudo journalctl -u <unit>` for the error and `systemctl show <unit>
-p Result`. For a backup unit on the leader, follow
[BackupTooOld](#backuptooold): check `pgbackrest info` and run the failed
command by hand once its cause is fixed; a failed `check` also means WAL may
not reach the repository ([WALArchivingFailing](#walarchivingfailing)). For a
start unit, `docker compose ps -a` in the project directory
(`/opt/veristar/core`, `edge` or `observability`) and start what is down.
For `sa-etcd-defrag`, bring the unhealthy etcd member back
([EtcdNoLeader, EtcdMembersDown](#etcdnoleader)); the next
week's run defragments again. A failed backup unit also raises
[BackupJobFailed](#backupjobfailed), which stays until a later run succeeds.
Once fixed, `sudo systemctl reset-failed <unit>` (a later successful run also
clears it); the alert resolves within 5 minutes.

### TextfileStale
A node_exporter textfile was not written for longer than its limit for 5
minutes: `systemd.prom` 15 minutes (`sa-systemd-failed.timer`, every 5
minutes), `reboot.prom` 30 minutes (`sa-reboot-required.timer`, every 10
minutes), `pgbackrest.prom` 2 hours (`sa-pgbackrest-textfile`, hourly from
cron) and `tls.prom` 2 hours (`sa-tls-expiry.timer`, hourly). node_exporter
keeps serving the last file, so the alerts built on it (SystemdUnitFailed,
HostRebootRequired, BackupTooOld, InternalCertificateExpiringSoon) see old
values: a stopped `sa-systemd-failed` timer hides every failed unit.
Action: on the node, `systemctl list-timers 'sa-*'` and `sudo journalctl -u
<writer>.service` for a timer; for `pgbackrest.prom`, `systemctl status cron`
and run `sudo /usr/local/sbin/sa-pgbackrest-textfile` by hand. A converge
installs the timers again. The alert resolves once the file is written.

### TextfileScrapeError
node_exporter could not read a file of its textfile directory
(`/etc/veristar/observability/textfile`) for 15 minutes. It leaves an
unreadable file out completely, so that file's metrics vanish.
Action: `sudo docker compose logs --since 1h node-exporter` in
`/opt/veristar/observability` names the file and the parse error. Run that
file's writer by hand (see [TextfileStale](#textfilestale)) or delete a file
that no writer owns.

### InternalCertificateExpiringSoon
The private network's node certificate or the earliest trusted internal CA
on a node expires within 30 days, for 1 hour (`sa_tls_certificate_expiry_timestamp_seconds`,
written hourly by `sa-tls-expiry.timer` from `/etc/veristar/tls/root/node.crt`
and `/etc/veristar/tls/ca/ca.crt`). An expired certificate breaks every
mutual TLS link between the nodes: NATS routes, etcd, Patroni, PostgreSQL
replication and the scrapes. Node certificates last 365 days and are renewed
only when you run `make internal-ca`.
Action: for `cert="node"`, renew as in
[secret-rotation.md](secret-rotation.md#internal-ca-and-node-certificates)
("Renew node certificates": `make internal-ca ENV=<env>`, then a rolling
deploy of the running release). For `cert="ca"`, rotate the CA as described
in the same section. The alert resolves within the hour after the new
certificate is installed.

### Watchdog
Always firing, by design. Alertmanager sends it every minute to the probe's
Gatus (external endpoint `monitoring_watchdog`, through the probe's Caddy,
which accepts that push only from the core nodes' addresses). When no push
arrived for 5 minutes, the status page shows the `watchdog` check of the
`monitoring` group as failed, and Gatus alerts to Discord when a receiver is
configured. Then both Prometheus servers, both Alertmanagers or the whole
core tier are down, or the core tier cannot reach the probe, and no other
alert can be sent from inside.
The console's Alerts page uses the same signal: while no Alertmanager
member holds `Watchdog`, it shows "Alerting pipeline down" instead of the
all-clear, with or without a probe.
Action when the status page or the console reports it missing: check the core nodes
([database-failover.md](database-failover.md) if both are down), then
`docker compose ps` in `/opt/veristar/observability` on each core
node. Never silence `Watchdog` in Alertmanager: a silence stops the heartbeat
and Gatus marks the check failed after 5 minutes. The heartbeat goes to the
probe of a `probe` inventory group or to the external probe bundle
(`observability_probe_external`,
[Prometheus and the Alertmanager watchdog](../deploy/probe.md#7-prometheus-and-the-alertmanager-watchdog));
an environment with neither sends it to a receiver with no target.

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
The console or the authenticated machine API call fails from
the probe for 2 minutes (dashboard objective, 99.90%).
Action: check the core nodes' Caddy and the control plane.

### ProbeNodeCheckFailing
A node fails by its own address: TLS handshake, certificate for the host, or
HTTP below 500. The hostname can still work through the other nodes.
Action: as EdgeNodeNotReady or CertificateExpiringSoon for that node.

## Fire-drills

Run on the local test stack (`tests/observability/up.sh`, then
`tests/observability/drill.sh <drill>`), with a test receiver in place of
Discord. Each drill causes the condition for real and waits for the firing
and the resolved message. The results below are from 2026-11-13; "fired" and
"resolved" are the times the test receiver got the Alertmanager or Gatus
message. Alerts without a drill result are covered by the `promtool` rule
tests in `rules/tests/`.

| Alert | Drill | Result |
|---|---|---|
| EdgeNodeNotReady, ProbeNodeCheckFailing, Gatus `edge-2 in.obs.test` | stop the gateway of edge-2 | fired 01:28:46, resolved 01:33:00 to 01:33:36; the `in` hostname check and ingestion stayed up |
| CertificateExpiringSoon, Gatus `edge-2 in.obs.test` | serve a 7-day certificate on edge-2 | fired 01:34:48, resolved 01:39:48 |
| DLQNotEmpty | publish one message to `dlq.web.drill`, then purge | fired 01:40:29, resolved 01:45:25 |
| EtcdMembersDown, EtcdNoLeader | stop two of three etcd members | fired 01:47:20, resolved 01:52:20 |
| NATSServerDown | stop one NATS server | fired 02:13:44, resolved 02:18:40 |
| ProbeFreshnessFailing, Gatus `freshness web` | stop replayer, metrics writer and jobs | web freshness failed after 62 s and API freshness after 92 s (limit 45 s locally) while ingestion, console, Umami host and machine API checks stayed up; resolved 01:44:05 |
| ReplayerNotConsuming, MetricsWriterNotConsuming, ServiceDown | same | fired, resolved 01:45:11 |
| QueueOldestMessageTooOld | same | fired 01:36:27, resolved 01:46:28 |
| ReplayerLagHigh, MetricsWriterLagHigh | same, plus 5,100 web events and 5,100 OTLP requests | fired by 01:43:44, resolved 01:48:41 |
| WebAggregationStale, UmamiPasswordRotationNotRun | same (no jobs leader) | fired by 01:43:44, resolved 01:48:41 |
| DiskUsageHigh | none needed: the test host's root filesystem was 94% full | fired for real; resolution checked in the `promtool` tests |
| PatroniNoLeader, BackupTooOld | none: the test stack has no Patroni or pgBackRest | fired for real (absent series); resolution checked in the `promtool` tests |

On production, Prometheus loads every rule and the only alert firing in normal
operation is `Watchdog`
([production checks](../reference/drills/2026-11-13-azure.md#checks)).
