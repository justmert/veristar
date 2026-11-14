---
title: "Queue backlog and dead-letter queues"
sidebarTitle: "Queue backlog and DLQ"
description: "Handle a growing queue and inspect or replay dead-lettered messages."
---

Web events wait in `WEB_EVENTS` until the replayer has stored them in Umami. API metric batches
wait in `API_METRICS` until the metrics writer has stored them in PostgreSQL. Both streams are
work queues, file-backed at three replicas. Messages that can never be stored
go to `WEB_DLQ` (kept 7 days) and `API_DLQ` (kept 30 days).

Alerts that lead here: `QueueOldestMessageTooOld`, `ReplayerLagHigh`, `MetricsWriterLagHigh`,
`ReplayerNotConsuming`, `MetricsWriterNotConsuming`, `DLQNotEmpty` ([alerts.md](alerts.md)).
Conventions are in [overview.md](overview.md).

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
   # nats monitoring through the edge relay: tls with the node's client certificate
   sudo curl -s --cacert /etc/veristar/tls/root/ca.crt --cert /etc/veristar/tls/root/node.crt --key /etc/veristar/tls/root/node.key \
     'https://10.20.1.11:8222/jsz?streams=true&consumers=true' \
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
   cd /opt/veristar/core && sudo docker compose ps replayer metricswriter jobs
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

   How the replayer tells an outage from a bad event: after a failed send it checks the Umami
   process with the heartbeat and a collection probe, a send for a website id that never exists,
   which reaches Umami's database and is answered 400 "Website not found." (the heartbeat alone
   answers without the database). While that check fails, Umami as a whole is down: the event
   is retried without limit, up to the 72 hour max age, and its visitor waits. While it passes,
   the event itself is the problem: after 4 failed sends (about 36 s) it goes to `WEB_DLQ` with
   the reason `umami failed this event 4 times while its health checks passed`. Umami inserts the
   event row before its data, so such an event can show up to 4 times as a custom event. Only
   that visitor's later events wait meanwhile; other visitors and sites keep flowing
   (`TestPoisonEventDoesNotStallOtherSites`, `internal/replayer`). A visitor with more than 64
   events held behind such an event has the excess dead-lettered too, so one visitor cannot fill
   `REPLAYER_MAX_IN_FLIGHT`.

   The probe is a read, so it passes while Umami cannot write (a full data disk, a database
   connection that went to a read-only node, lock or statement timeouts on `website_event`,
   Prisma's pool exhausted). The replayer recognises that case from the sends themselves
   (review, `internal/replayer/writefault.go`): when within one minute at least 3 distinct
   visitors had a send fail on a process whose checks passed, and they are at least as many as
   the sends that process stored, the process counts as unable to write. Failures then do not
   count against the event and nothing is dead-lettered; the events are retried at most every
   30 s and all retry at once when a send succeeds again. `replayer_write_fault_retries_total`
   rises while this lasts (`TestWriteFaultIsNotDeadLettered` makes Umami's database read-only
   and checks that nothing reaches `WEB_DLQ`). Fix the database as for an outage. With very
   little traffic (fewer than 3 visitors failing within a minute) a write fault still looks like
   bad events and those events reach `WEB_DLQ` with the reason above; after the fix, replay them
   with `dlq replay --queue web --reason 'health checks passed'` (Dead-letter queues, step 4).

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

2. **List the messages.** `platformctl dlq list` prints one line per message: DLQ sequence,
   time it was dead-lettered, key (site id for `web`, tenant id for `api`), origin stream,
   sequence and subject, payload size and reason. It never prints the payload, which is a full
   event. Run platformctl as in [README.md](overview.md#running-platformctl-on-a-core-node) and
   replace its last words with:

   ```sh
   dlq list --queue web                 # WEB_DLQ
   dlq list --queue api                 # API_DLQ
   dlq list --queue web --key <site id> --from 120 --to 180 --reason umami
   ```

   The columns come from the headers `Sa-Dlq-Reason`, `Sa-Dlq-Subject`, `Sa-Dlq-Stream` and
   `Sa-Dlq-Stream-Seq` (`internal/natsx/publish.go`). The service logs say the same per message
   (`docker compose logs replayer | grep 'moved to the dlq'`, and `metricswriter` with
   `moved to the dead-letter stream`, on every core node).

3. **Fix the cause.** A reason that names a malformed event or batch is a client or code bug: open
   an issue with the reason and the stream sequence. A reason that names Umami (for example a
   rejected website id, or an error from a bad Umami release) points at Umami or at the registry
   and Umami being out of step; fix Umami, or run the control plane's reconcile (`jobs` task
   `reconcile`, every 10 minutes by itself).

4. **Replay what the fix makes storable.** Only after the cause is fixed, otherwise the messages
   come straight back:

   ```sh
   dlq replay --queue web --from 120 --to 180       # a sequence range, inclusive
   dlq replay --queue web --reason 'umami 5'        # every message whose reason contains the text
   dlq replay --queue api --key <tenant id>
   dlq replay --queue web --all
   ```

   Replay needs a selection (`--all`, `--from`, `--to`, `--key` or `--reason`, which combine).
   Each message goes back to its original subject on `WEB_EVENTS` or `API_METRICS` with its
   payload unchanged, so the event id inside it and its original time stay, and the replayer's
   duplicate guard still applies. Its message id is the original id plus
   `.replay.<dlq stream>.<dlq sequence>`, and the header `Sa-Dlq-Replay` names the DLQ message.
   The DLQ copy is securely deleted only after the stream acknowledged the publish, so a crash in
   between leaves it in the DLQ, and running the same replay again is deduplicated by the stream
   (reported as "already there from an earlier run"). One run stops at the DLQ's last sequence
   when it started, so a message that fails again is not taken twice. A message whose origin
   subject is not on the original stream is skipped, kept and reported, and the command exits
   non-zero.

   Ordering: a replayed event is stored after the events its visitor sent later,
   because those were delivered while it waited in the DLQ. This matters only for events
   dead-lettered behind a failing one (the 64-event cap of step 3 under Backlog). The event
   keeps its acceptance time, so it still lands in the right hour, but it joins the visit of
   the visitor's latest events (the cache token kept in `visit_state`), or, once that state
   expired after 24 hours, the visit Umami derives from the session and the event's hour. It
   can therefore be counted in another visit than the one it belonged to, which can change that
   visitor's visit count, visit duration and bounce by one visit.

5. **Purge what can never be stored.** Same selection, plus `--yes`:

   ```sh
   dlq purge --queue web --key <site id> --yes
   dlq purge --queue api --all --yes
   ```

   Purge securely deletes each selected message, the same way erasure does. Without a purge the
   messages expire with the stream's max age (7 and 30 days), and `DLQNotEmpty` keeps firing
   until then. Erasure requests delete a visitor's or site's DLQ messages by themselves
   ([erasure-requests.md](erasure-requests.md)); a replayed event that erasure should cover is
   back on `WEB_EVENTS`, where erasure waits for the replayer as for any queued event.

`platformctl` may publish to `web.events.*` and `api.metrics.*` for this (its NATS user in
`internal/natsx/topology.go`); no service user gained a permission.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-13 | this stage load run A at 10x, then drain | 0 acknowledged events lost; backlog empty 1,101 s after the run (bound 600 s): fail on drain time; consumer tuning made from it | [../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md) |
| 2026-11-13 | Alert fire-drills: replayer, writer and jobs stopped, 5,100 web events and 5,100 OTLP requests queued, then started | QueueOldestMessageTooOld, ReplayerLagHigh, MetricsWriterLagHigh, ReplayerNotConsuming fired and resolved after the consumers drained the queue | [alerts.md](alerts.md), fire-drills |
| 2026-11-13 | Alert fire-drill: one message on `dlq.web.drill`, then purged with the test drill tool | DLQNotEmpty fired and resolved | [alerts.md](alerts.md), fire-drills |
| 2026-11-13 | `platformctl dlq list`, `replay` (range, reason, crash rerun, failing again), `purge`, refusals, with the production NATS users and permissions | pass (`go test -run TestDLQ ./cmd/platformctl/`, real NATS server) | `cmd/platformctl/dlq_test.go` |
| 2026-11-13 | this stage: step 1 against the development stack's NATS (`127.0.0.1:8222`) | the command printed every stream with its counts (`WEB_DLQ 0`, `API_DLQ 0`, `WEB_EVENTS 0`) | this page |

Not yet exercised on staging. The purge in the fire-drill used `tests/observability/drill`, a
test tool, before `platformctl dlq` existed.
