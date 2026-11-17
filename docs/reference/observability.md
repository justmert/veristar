---
title: "Observability reference"
sidebarTitle: "Observability"
description: "The metrics, probes and alert rules behind monitoring, and the sources they were checked against."
---

The metrics, receivers, status checks and SLO measures behind
`infra/ansible/roles/observability`, `cmd/prober`, `cmd/sloreport` and the alert
rules. Upstream behaviour was read from the pinned sources in the Go module cache or the PyPI sdist on 2026-11-13, and the metric
names were confirmed on the local test stack (`tests/observability`).

## Metrics by component

| Component | Endpoint | Names used |
|---|---|---|
| Go services | ops port `/metrics` (`internal/obs`) | `ops_ready_check{check}` (each readiness check at scrape time), `go_*`, `process_*` |
| gateway | ops port | `gateway_responses_total{route,outcome}`, `gateway_request_duration_seconds`, `gateway_degraded_total`, `gateway_outcomes_total` (request outcomes, and the evidence counters `evidence_net_<class>` and `network`), `gateway_geo_build_timestamp_seconds`, `gateway_network_build_timestamp_seconds` |
| collector v0.165 | `service.telemetry.metrics` with a pull prometheus reader on 8888 | `otelcol_receiver_accepted_metric_points`, `otelcol_receiver_refused_metric_points`, `otelcol_receiver_failed_metric_points`, `otelcol_processor_incoming_items`/`outgoing_items{processor}`, `otelcol_process_*`. No `_total` suffix; the custom jetstream exporter has no exporterhelper metrics |
| replayer | ops port | `replayer_leader`, `replayer_consumer_pending_messages`, `replayer_outcomes_total`, `replayer_send_duration_seconds`, `replayer_oldest_pending_age_seconds`, `replayer_umami_sends_total{process}` (sends answered 200 per Umami process, rule `ReplayerUmamiProcessIdle`) |
| metrics writer | ops port | `metricswriter_leader`, `metricswriter_consumer_pending_messages`, `metricswriter_cardinality_overflow_points_total`, batch and point counters |
| jobs | ops port | `jobs_leader`, `jobs_task_runs_total{task,result}`, `jobs_task_last_success_timestamp_seconds{task}`, `jobs_umami_rotated_epoch_day` |
| NATS | prometheus-nats-exporter 0.20.3, `-jsz=all -varz -healthz`, one per server | `jetstream_stream_total_messages`, `jetstream_stream_first_seq`, `jetstream_consumer_num_pending`, `jetstream_consumer_num_ack_pending`, `jetstream_server_jetstream_disabled`, `jetstream_server_total_streams{is_meta_leader}`, `gnatsd_healthz_status` (0 is healthy), `gnatsd_varz_*` |
| etcd 3.7 | client port `/metrics` | `etcd_server_has_leader`, `etcd_server_leader_changes_seen_total` |
| Patroni 4.1.5 | REST API `/metrics` (`patroni/api.py`) | `patroni_primary`, `patroni_replica`, `patroni_cluster_unlocked`, `patroni_xlog_location`, `patroni_xlog_replayed_location`, `patroni_postgres_running` |
| PostgreSQL | postgres_exporter 0.20.1 default collectors | `pg_up`, `pg_replication_lag_seconds`, `pg_replication_is_replica`, `pg_stat_archiver_failed_count`, `pg_stat_archiver_last_archive_age` |
| pgBackRest | node_exporter textfile written hourly by the role | `pgbackrest_last_backup_completion_timestamp_seconds{stanza,type}`, `pgbackrest_info_success` |
| nodes | node_exporter 1.12.1 | `node_filesystem_*`, `node_cpu_*`, `node_memory_*` |
| Caddy 2.11.9 (edge and core) | plain-http listener `:2021` with only the `metrics` handler, added by `deploy/caddy/assemble.sh`, plus the global `metrics` option for the HTTP counters; relay port 9307 | `caddy_http_requests_total{server,handler}`, `caddy_http_request_duration_seconds`, `caddy_reverse_proxy_upstreams_healthy`, `caddy_config_last_reload_successful`. The admin API stays on the container's loopback: on any other address it accepts unauthenticated config changes from every host that reaches it, and `caddy reload` and the health check use it there |
| HAProxy 3.4.7, alpine build (core) | `frontend stats` on `core_haproxy_stats_port` (8404): `/metrics` from the built-in prometheus-exporter service (`USE_PROMEX`), `/stats` page; relay port 9308 for `/metrics`, `/stats` on the node's loopback for an SSH tunnel | `haproxy_backend_active_servers{proxy="primary"}` (0 means no Patroni leader reachable), `haproxy_server_status`, `haproxy_frontend_*`, `haproxy_process_*` |
| prober | ops port, through the probe Caddy behind a bearer token | `prober_check_success{check}`, `prober_check_runs_total{check,result}`, `prober_check_duration_seconds`, `prober_freshness_backlog_events{pipeline}`, `prober_tls_certificate_expiry_timestamp_seconds{node,host}` |

The exporter has no first-message timestamp, so "age of the oldest message"
is derived: `WEB_EVENTS` and `API_METRICS` are work queues, so their first
sequence is the oldest unacknowledged message, and a first sequence that did
not move for 10 minutes while the stream was never empty means that message
is at least 10 minutes old (rule `QueueOldestMessageTooOld`).

## Discord receivers

- Alertmanager 0.34.2: `discord_configs` with `webhook_url` or
  `webhook_url_file` (exactly one), `title`, `message`, `content`,
  `username`, `avatar_url`, `send_resolved`. Title is cut at 256 runes,
  message at 4096, content at 2000 (`notify/discord`). The role uses
  `webhook_url_file`, so the secret stays in a 0400 file.
- Gatus 5.38.0: `alerting.discord` with `webhook-url`, `title`,
  `message-content`, `default-alert`, and per-group `overrides`
  (`alerting/provider/discord`). The URL is read from the environment
  (`${GATUS_DISCORD_WEBHOOK_URL}`).
- Neither validates the host of the URL, so the local test stack points both
  at a labelled test receiver (`tests/observability/receiver`) and exercises
  the real Discord code paths. Adding a Discord webhook to an environment is in
  [../runbooks/alerts.md](../runbooks/alerts.md), "Adding a receiver".

## Gatus

- External endpoints: `external-endpoints[]` with `name`, `group`, `token`,
  `alerts`, `heartbeat.interval` (at least 10 s). Results are pushed with
  `POST /api/v1/endpoints/{group}_{name}/external?success=&error=&duration=`
  and a bearer token (`api/external_endpoint.go`); the key lowercases and
  replaces `/ _ . , space # + &` with `-` (`config/key/key.go`). The result's
  timestamp is the time Gatus received it.
- Retention: `storage.maximum-number-of-results` per endpoint (default 100)
  and `maximum-number-of-events` (default 50). Hourly uptime entries are kept
  for 30 days (`uptimeRetention`, cleanup above 32 days in
  `storage/store/sql/sql.go`). 30 days is shorter than a calendar month, so
  `sloreport collect` stores each day's raw results in `docs/slo/data/` and
  the monthly report is computed from those files. The role sets
  `maximum-number-of-results: 20160` (one week at 30 s), and
  `sloreport collect -page-size` must match it.
- Uptime API: `GET /api/v1/endpoints/{key}/uptimes/{1h|24h|7d|30d}`; raw
  results: `GET /api/v1/endpoints/{key}/statuses?page=&pageSize=`, newest
  page first, page 1 capped at the maximum number of results.
- What Gatus cannot do at 5.38.0, so the prober does it and pushes the
  result: conditions have no hash function (no SHA-384 of the tracker file),
  and a check by address cannot present SNI, because the client sets no TLS
  server name from a `Host` header and `tls://` checks dial without one
  (`client/client.go`).

## Measures

- Ingestion (99.99%): one result per 30 s interval, `slo_ingestion`,
  available when the gateway answered 202 to the probe's web event and the
  collector answered 200 to its OTLP metric.
- Dashboard (99.90%): `slo_dashboard`, available when the console and an
  authenticated machine API call (`/web/hourly` with the probe's scoped key)
  both answered.
- Freshness, reported separately: `slo_freshness-web` and
  `slo_freshness-api`. Accepted probe events older than the limit (10 minutes
  in production) must be visible through `/web/hourly` and `/api/hourly`. The
  prober keeps a baseline count and the accepted sends after it, so events in
  flight or replayed twice can only raise the count (`internal/prober`).
