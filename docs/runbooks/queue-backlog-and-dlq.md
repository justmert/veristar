# Queue backlog and dead-letter queues

Web events wait in `WEB_EVENTS` until the replayer has stored them in Umami. API metric batches
wait in `API_METRICS` until the metrics writer has stored them in PostgreSQL. Both streams are
work queues, file-backed at three replicas. Messages that can never be stored
go to `WEB_DLQ` (kept 7 days) and `API_DLQ` (kept 30 days).

Alerts that lead here: `QueueOldestMessageTooOld`, `ReplayerLagHigh`, `MetricsWriterLagHigh`,
`ReplayerNotConsuming`, `MetricsWriterNotConsuming`, `DLQNotEmpty` ([alerts.md](alerts.md)).
Conventions are in [README.md](README.md).

## Limits that set the urgency

| Stream | Max age | What happens at the limit |
|---|---|---|
| `WEB_EVENTS` | 72 hours | the oldest accepted events are dropped by the stream; they never reach Umami |
| `API_METRICS` | 7 days | the same for metric batches |
| `WEB_DLQ` | 7 days | dead-lettered events expire |
| `API_DLQ` | 30 days | dead-lettered batches expire |

Measured drain rates on the local stand-in topology after the this stage tuning
([../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md)): about **280 web
events/s** with one Umami process and about **1,417 OTLP requests/s**. The core role now runs
several Umami processes per node (`core_umami_processes`, 4 by default) to raise the web rate; its
defaults file records the per-count measurements, and the rate on real servers is not measured.
The expected peak is 50 web events/s and 116 OTLP requests/s, so even at 280/s a backlog of one
hour at the expected peak drains in roughly 15 minutes. These rates come from a shared host and
are not a measurement of staging.

## Backlog

1. **Read the size and age.** From a core node:

   ```sh
   curl -s 'http://10.20.1.11:8222/jsz?streams=true&consumers=true' \
     | python3 -c 'import json,sys
   d=json.load(sys.stdin)
   for a in d.get("account_details",[]):
     for s in a.get("stream_detail",[]):
       c=[(x["name"],x.get("num_pending"),x.get("num_ack_pending")) for x in s.get("consumer_detail",[])]
       print(s["name"], s["state"]["messages"], s["state"].get("first_ts"), c)'
   ```

   (production: `10.10.1.11`). Each line is stream, messages held, time of the oldest message,
   and per consumer the pending and unacknowledged counts. A growing `WEB_EVENTS` with a
   `replayer` consumer whose pending count grows is a stalled or slow replayer.

2. **Is anyone consuming?** Exactly one replayer, one metrics writer and one jobs daemon lead,
   on either core node. On each core node:

   ```sh
   cd /opt/stellar-analytics/core && sudo docker compose ps replayer metricswriter jobs
   sudo docker compose logs --since 15m --tail 100 replayer
   ```

   If both instances of a service are down, start them (`sudo docker compose up -d replayer`).
   If both run and none leads, check the `locks` bucket in the stream list of step 1
   (`KV_locks`) and NATS quorum ([edge-node-failure.md](edge-node-failure.md)).

3. **Is the downstream healthy?** The replayer needs Umami; the writer needs the database.

   ```sh
   sudo docker compose exec caddy wget -qO- http://umami:3000/api/heartbeat; echo
   sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml list
   ```

   Fix Umami (UmamiHeartbeatFailing) or the database ([database-failover.md](database-failover.md))
   first. The consumers catch up by themselves; nothing has to be replayed by hand, and events
   keep their original acceptance time, so they land in the right hour in Umami.

4. **Watch it drain.** Repeat step 1 every few minutes. If the backlog grows faster than it
   drains and the oldest message approaches the stream's max age, check that every Umami
   process runs (`sudo docker compose ps` shows `umami` and `umami-2` and up), then see
   [capacity.md](capacity.md) for raising the number of Umami processes. The replayer's
   concurrency is already raised (`REPLAYER_WORKERS` 128, `REPLAYER_MAX_IN_FLIGHT` 900 in
   `infra/ansible/roles/core/defaults/main.yml`). Write down the oldest timestamp; events older
   than 72 hours will be lost and the tenants must be told.

## Dead-letter queues

A message lands in a DLQ only when a retry cannot fix it: Umami rejected the event permanently,
the message has no usable visitor key, or a metric batch cannot be decoded or has an invalid
message id.

1. **Count.** Step 1 above lists `WEB_DLQ` and `API_DLQ` with their message counts.

2. **Read the reasons from the logs.** Every message moved to a DLQ is logged with its stream
   sequence and reason:

   ```sh
   cd /opt/stellar-analytics/core
   sudo docker compose logs --since 24h replayer | grep -E 'moved to the dlq'
   sudo docker compose logs --since 24h metricswriter | grep -E 'moved to the dead-letter stream'
   ```

   Run it on both core nodes: the leader may have moved. The messages themselves carry the
   headers `Sa-Dlq-Reason`, `Sa-Dlq-Subject`, `Sa-Dlq-Stream` and `Sa-Dlq-Stream-Seq`
   (`internal/natsx/publish.go`).

3. **Fix the cause.** A reason that names a malformed event or batch is a client or code bug: open
   an issue with the reason and the stream sequence. A reason that names Umami (for example a
   rejected website id) points at the registry and Umami being out of step; run the control
   plane's reconcile (`jobs` task `reconcile`, every 10 minutes by itself).

4. **Clear it.** There is no tool in the repository today that re-publishes or purges DLQ
   messages, and no NATS command-line client is pinned. The messages expire with the stream's
   max age (7 and 30 days), and `DLQNotEmpty` stays firing until then. Erasure requests delete
   a visitor's or site's DLQ messages ([erasure-requests.md](erasure-requests.md)). A replay
   command is a gap to close before production.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-13 | this stage load run A at 10x, then drain | 0 acknowledged events lost; backlog empty 1,101 s after the run (bound 600 s): fail on drain time; consumer tuning made from it | [../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md) |
| 2026-11-13 | Alert fire-drills: replayer, writer and jobs stopped, 5,100 web events and 5,100 OTLP requests queued, then started | QueueOldestMessageTooOld, ReplayerLagHigh, MetricsWriterLagHigh, ReplayerNotConsuming fired and resolved after the consumers drained the queue | [alerts.md](alerts.md), fire-drills |
| 2026-11-13 | Alert fire-drill: one message on `dlq.web.drill`, then purged with the test drill tool | DLQNotEmpty fired and resolved | [alerts.md](alerts.md), fire-drills |
| 2026-11-13 | this stage: step 1 against the development stack's NATS (`127.0.0.1:8222`) | the command printed every stream with its counts (`WEB_DLQ 0`, `API_DLQ 0`, `WEB_EVENTS 0`) | this page |

Not yet exercised on staging. The purge in the fire-drill used `tests/observability/drill`, a
test tool, which is why step 4 names the missing operator tool instead.
