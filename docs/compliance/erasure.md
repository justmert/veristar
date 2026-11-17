---
title: "Erasure workflow"
description: "How a project owner asks for erasure, what is erased, and how the request is tracked."
---

How a project owner erases web analytics data, what each kind of request
deletes, and how the request is tracked. This page is not legal advice. Last
updated 2026-11-17. The API contract is
`docs/reference/control-plane-api.md` section 10; the
> behaviour below is tested in `internal/controlplane/erasure/live_test.go`
> against real PostgreSQL, NATS, Umami, the gateway's visitor code, the
> replayer and the jobs follow-up.

## 1. What can be erased

A project **owner** asks for erasure on the project's Privacy page in the
console (`/projects/{tenant_id}/privacy`, tested in
`tests/e2e-console/specs/privacy.spec.ts`) or with
`POST /api/tenants/{tenant_id}/erasure`. Every member sees the request
history there. The page keeps a typed IP address and user agent in memory
only, never in browser storage, and clears them once the request is answered. Erasure is never blocked by a
suspension or by terms the project has not accepted yet. There are three kinds.

| Kind | The project owner supplies | What is deleted | When it works |
|---|---|---|---|
| `visitor_today` | the site, the data subject's IP address and user agent | that visitor's Umami sessions on both bands of the site for the current UTC day, with all their events; the visitor's undeliverable messages in `WEB_DLQ`; the visitor's record in `visit_state` | only during the UTC day of the visit |
| `session` | the site and an Umami session id, found with the session finder on the Privacy page (contract section 18.7) | that session and all its events; while the session's visit record still exists (24 hours after its last event), also the visitor's `WEB_DLQ` messages and the record | during the whole raw retention period |
| `site_reset` | the site and its domain, typed again as confirmation | every event and session of both Umami websites of the site (Umami's reset), every `WEB_DLQ` message and visit record of the site, and every aggregate row of the site | at any time |

After each kind the jobs task `erasure-followup` reads the affected UTC days of
`agg.web_daily` and `agg.web_daily_dim` again from Umami (for `site_reset` it
deletes every aggregate row of the site and reads the last days again), so the
operator view stops counting erased visits. The request shows
`reaggregated_at` when that is done.

## 2. Why visitor erasure works only on the day of the visit

The service never stores an IP address. A visitor is known only by a token
computed from the address, the user agent, the site and a secret that exists
for one UTC day in memory (`privacy-strategy.md` section 3). On the day of the
visit the gateways can recompute the token from the address and user agent the
owner supplies, and the replayer's visit record links the token to the Umami
sessions.

After midnight the secret is gone. No one, including the operator, can
recompute the token or tell which stored session belongs to which person. A
request on a later day therefore finds nothing, and the answer says so
(`outcome = "nothing_found"`, with the explanation in `message`). GDPR Art.
11(1) relieves a controller of keeping or acquiring information only to
identify a data subject, and Art. 11(2) suspends Arts. 15 to 20 when the
controller can show it cannot identify the person, unless the person provides
additional information that enables identification (S10). Once the secret no
longer exists, no information a data subject supplies, including an address
and user agent, can re-link a past day's sessions.

A data subject who can point to a session (for example the tenant finds it in
the dashboard by time and page) can still have it deleted with kind `session`.

## 3. How a request runs

1. The control plane checks membership and role, the body and the site.
2. `visitor_today`: the control plane sends the address and user agent to the
   gateways in one NATS request on `edge.visitor-token` (only the control plane
   may send it, only gateways may answer, `internal/natsx/topology.go`). A
   gateway answers with the token and the UTC day it belongs to. The address
   and user agent are not kept anywhere after this step: not in a table, a log
   line, an audit row, an error or a queue.
3. The control plane notes the last sequence of `WEB_EVENTS`, inserts the
   request into `cp.erasure_requests` (status `running`) with an audit row, and
   answers 202.
4. The run waits until `WEB_EVENTS` holds no event of the site with a sequence
   at or below the noted one, so an event accepted before the request and
   still queued (up to 72 hours) is in Umami or in the dead-letter queue before
   anything is deleted, and is deleted with the rest. If that does not happen
   within `ERASURE_QUEUE_WAIT` (default 15 minutes), the run fails without
   deleting anything and says why.
5. The run deletes as described in section 1. For `visitor_today` and
   `session` it removes the visit record first, so a later event of the same
   visitor starts a new session instead of reusing the deleted one. A
   `visitor_today` run first writes the session ids it is about to delete to
   a progress record (`erasing.<site>.<token>` in the same memory bucket, same
   24 hour limit) and removes it only after every session and dead-letter
   message is gone. A run that fails part way therefore leaves the sessions
   findable: the next request of the same day finishes the work and answers
   `erased` (`TestErasure/a_failed_visitor_run_is_finished_by_the_next_request`).
6. The run records `outcome`, `message`, the counts (`sessions_deleted`,
   `events_deleted`, `dlq_messages_deleted`) and the affected days. A failed
   run records what it deleted before the failure.
7. `erasure-followup` re-aggregates the affected days. It also fails a run
   whose control plane process stopped (no heartbeat for 2 minutes); such a
   request must be submitted again, because a `visitor_today` token existed
   only in that process. A request submitted again on the same day finds what
   the failed run left (step 5).

One request per site runs at a time; a second answers 409
`erasure_in_progress`.

## 4. What the records keep

`cp.erasure_requests` keeps the kind, site, requesting user, times, status,
outcome, message, counts, the UTC day (`visitor_today`) or the session id
(`session`), and the affected days. It never holds an IP address, a user
agent or a visitor token. The audit log keeps `erasure.request`,
`erasure.complete` and `erasure.fail` rows with the same restriction. The test
`TestErasure/no_residue` reads both tables, every message of every NATS stream
and bucket, and every log line of every component for the addresses and user
agents used, and finds none.

## 5. Limits

- **Same-day only for visitor erasure** (section 2).
- **The link is in memory.** The record that links a visitor token to its
  Umami sessions lives in the memory bucket `visit_state`, which a NATS
  restart empties; a restart that also empties `edge_secrets` changes the
  visitor's token for the rest of the day. A visit before such a restart
  cannot be located, although it may still be stored. The answer then says
  that the visit "could not be located", never that none was recorded, and
  names the time the link store was emptied when that was today; it points to
  a site reset as the way to remove everything that may remain
  (`TestErasure/a_visit_whose_link_was_lost_is_not_located`). Umami derives
  the session id from the token with its own `APP_SECRET`, which the control
  plane deliberately does not hold (it also signs Umami's login tokens), so
  the session cannot be computed instead.
- **Concurrent event.** If the visitor's own browser sends an event at the
  moment the run deletes the session, the replayer can store that event with
  the old session id after the session row is gone. Such an event belongs to
  no session, so Umami's reports, which join events to sessions, do not show
  it. `jobs retain-web` deletes events without a session that are younger than
  72 hours on its next run (tested in `internal/jobs/retainweb`).
- **Session kind and the dead-letter queue.** `WEB_DLQ` messages carry the
  visitor token, not the session id. A `session` request finds them only
  through the visit record, which exists for 24 hours after the visitor's
  last event. Older dead-lettered messages expire with the queue's 7-day
  limit.
- **Probing.** The workflow tells an owner whether an address and user agent
  visited its site today, and deletes that data in the same step. Only owners
  can use it, and every request is audited.
- **Backups.** Data deleted here stays in the encrypted PostgreSQL backups
  until they expire. The backup role keeps four weekly full backups, and the
  backup storage deletes previous blob versions 35 days after they were
  replaced ([../deploy/backups.md](../deploy/backups.md)).

## 6. Tenant exit

When an operator removes a project, the purge deletes its Umami team and
websites, its registry entries and, unless `removal_keep_aggregates` is set,
its aggregates (`docs/runbooks/tenant-removal.md`). A tenant that wants its
web data deleted while it stays enrolled uses `site_reset` per site.

## 7. Deleting your console account

You delete your own console account on the console's **Account** page (the
account menu at the bottom of the sidebar, then **Account**, `/account`), or
with `DELETE /api/me` (contract section 4.1.6). You confirm by typing your
GitHub login, or, for an account without GitHub, your whole Stellar address
(wallet) or your whole passkey account address. Tested in `internal/controlplane/account`,
`tests/isolation/console/account_test.go` and
`tests/e2e-console/specs/account.spec.ts`.

While you are the only owner of a project that is not removed, the deletion is
refused and the page lists those projects. Make another member an owner, or
ask the operators to remove the project (section 6), then delete the account.

While you are the last active operator admin, the deletion is refused with
`409 last_operator_admin`. Make another operator an operator admin on the
**Operators** page, then delete the account.

The deletion runs in one transaction and:

- removes you from every project, which revokes your MCP credentials for those
  projects; the reconciler then brings each project's Umami team in line;
- removes every operator roster row that holds your GitHub id, your Stellar
  address or your passkey account address (with its login, role, status
  reason and enrollment window) and deletes the passkeys you added for the
  operator area's step-up (credential id, public key, name, creation and last
  use);
- deletes your MCP access keys, AI client connections and their tokens, your
  sessions in every browser (with any step-up time they held), and the invites addressed to your GitHub account,
  your Stellar address or your passkey account address;
- deletes your Umami user, which ends every dashboard token you held. If Umami
  cannot be reached, nothing is deleted and the console asks you to try again;
- removes your GitHub id and login, your Stellar address, your passkey (its
  credential id, public key and account address) and your Umami user name
  from your account record. The record keeps
  only a random account id, so projects, sites, tokens and audit rows that name
  you as their creator or actor stay consistent;
- writes one `account.delete` audit row that names you by that account id only.

The audit log is append-only and keeps the rows written before the deletion.
They name you by the account id. Some of them also hold your GitHub login or
your Stellar address or passkey account address in their detail as it was at
the time, for example
`user.sign_in`, `user.identity_link` and the `member.add` row of the project
that added you. The roster's `operator.add` and `operator.bootstrap` rows keep
the GitHub id and login or the Stellar address and the reason they were
written with, and `operator.passkey_add` rows keep the passkey's name.

Projects you created keep running under their other owners. Operator API keys
you created as an operator stay active, because machine consumers of the
service use them; revoke them on the **API keys** page before you delete the
account if they should stop.

Signing in with the same GitHub account or wallet later creates a new, empty
account. A deleted account's passkey no longer signs in; create a new passkey
to start a new account. The deletion removes the person from the operator roster;
an identity in `OPERATOR_GITHUB_IDS` or `OPERATOR_STELLAR_ADDRESSES` is put back
on the roster at the next control plane start, so take it out of that setting as
well.

To stop one sign-in method without deleting the account, unlink it on the
**Account** page (contract section 4.1.8). The GitHub id or the address leaves
the account record at once.

