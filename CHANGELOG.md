# Changelog

This file records the changes in each release of Veristar. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Platform releases are the `v*` tags. Each one publishes eleven container images,
signed with the release key and verified before any node pulls them; the image
digests of every release are in
[docs/security/supply-chain.md](docs/security/supply-chain.md). The web tracker
has its own version line, listed in [Tracker releases](#tracker-releases),
because every tracker version stays served unchanged at `/v/<version>/sdk.js`.

The operator announces intended additions or replacements of sub-processors
here, before they take effect, as the processing terms require
([docs/compliance/dpa-template.md](docs/compliance/dpa-template.md), clause 6).

## [Unreleased]

### Added

- API onboarding for Java: the guide
  [docs/onboarding/api/java.md](docs/onboarding/api/java.md) and the example
  `examples/otel-java-spring`, Spring Boot 4.1.2 on Java 25 with the
  OpenTelemetry Java agent 2.33.1, which dual-exports through a declarative
  configuration file. `tests/otelapps` runs it in every export mode and the
  `apipath` suite against the platform collector; the console's API analytics
  setup page has a Java tab.
- API onboarding for the Stellar Anchor Platform:
  [docs/onboarding/api/anchor-platform.md](docs/onboarding/api/anchor-platform.md)
  and `examples/otel-anchor-platform` attach the Java agent to the Anchor
  Platform 5.0.0 SEP server and platform server, with request metrics to the
  platform and traces to the anchor's own backend. `TestAnchorPlatform` runs
  the real Anchor Platform image on PostgreSQL and checks every SEP-1, SEP-10,
  SEP-24 and platform API route and status that arrives.
- Web analytics guides for documentation sites: Docusaurus (`scripts` in
  `docusaurus.config.ts`), VitePress (`head` in `.vitepress/config.ts`) and
  Mintlify (a `tracker.js` file in the docs repository), each with the
  console's tag and its integrity value
  ([docs/onboarding/web/docusaurus.md](docs/onboarding/web/docusaurus.md),
  [docs/onboarding/web/vitepress.md](docs/onboarding/web/vitepress.md),
  [docs/onboarding/web/mintlify.md](docs/onboarding/web/mintlify.md)). The
  examples `examples/docusaurus` (Docusaurus 3.10.2), `examples/vitepress`
  (VitePress 1.6.4 on Vite 6.4.6 through an npm override) and
  `examples/mintlify` (Mintlify's local preview, CLI 4.3.49) run in the
  browser test matrix and in the real-gateway browser test.
- Calls per contract function: for every verified Soroban contract, the
  daily successful and failed calls into each function, split by caller
  (operations that invoke the contract, and other contracts calling it),
  with the distinct accounts behind them. The `chain-activity` task reads
  them from the `getTransaction` answers it already fetches: the envelope
  for the calls of accounts, the diagnostic `fn_call` and `fn_return`
  events for the calls of contracts and their failures. Distinct accounts
  are counted in memory; no address is stored. At most 64 functions per
  contract and day keep their own row, the rest add up under `{other}`.
  Contract days stored before this release are read again while the RPC
  window holds them. Shown on the project's On-chain page with daily calls
  and a CSV export, in the operator's project detail with the same export,
  in What SCF sees, at `GET /api/metrics/v1/onchain/functions`, through the
  MCP tools `get_contract_functions` and `get_tenant_contract_functions`,
  and in signed usage reports, format version 3 (versions 1 and 2 still
  verify). Migration 00047 (`agg.chain_function_daily`,
  `agg.chain_daily.functions`, row-level security, `agg.purge_chain`)
  ([docs/reference/onchain-sources.md](docs/reference/onchain-sources.md#calls-per-contract-function),
  [control-plane-api.md](docs/reference/control-plane-api.md#159-calls-per-contract-function)).
- Web event delivery from every core node: `WEB_EVENTS` is split into 32
  partitions by a hash of the visitor token, each with its own consumer
  (`replayer-p00` to `replayer-p31`) and lock. The replayer on every core node
  owns a share of the partitions (11, 11 and 10 on three nodes) and delivers
  them to its own node's Umami processes, so delivery grows with the node
  count. A visitor's events stay in one partition with one owner, so their
  order, visit continuity and the duplicate guard hold through node loss and
  rolling deploys; a replayer that stops delivers what it holds before it
  hands its partitions over
  ([docs/architecture.md](docs/architecture.md), "Web event partitions").
- `platformctl nats retire-legacy-web` removes the single web subject of
  earlier releases once its last event is delivered.
- Replayer metrics `replayer_partitions`, `replayer_partitions_owned`,
  `replayer_legacy_leader`, and per partition
  `replayer_partition_pending_messages`,
  `replayer_partition_ack_pending_messages` and
  `replayer_partition_oldest_pending_age_seconds`; the alert
  `ReplayerPartitionsUnowned`; two partition panels on the queues dashboard.

### Changed

- Terms of use `2026-11-18-draft.6`: SCF also receives, per verified
  contract, UTC day, function and kind of caller, the counts of successful
  and failed calls and of the distinct accounts that made them; the accounts
  behind a function's daily count are held in memory while the day is read
  and never stored. The data processing terms and the records of processing
  list the same. Every project accepts it at its next sign-in.
- Gateways queue web events on `web.events.<site id>.<partition>` instead of
  `web.events.<site id>`. Erasure waits for every partition of a site, and
  `platformctl dlq replay` and `platformctl queue forward` send a web event
  to its partition.
- `replayer_leader` is 1 on every replayer that owns a partition, and the
  replayer lag gauges count the partitions the instance owns; the queues
  dashboard sums them over the nodes. `ReplayerLagHigh` and
  `WebEventsBacklogGrowing` read the sum of every replayer consumer.
- `deploy.yml` installs the NATS permissions of the new release on every
  NATS server before the core tier. The replayer container gets 30 s to stop.

### Fixed

- A page whose path starts with two slashes (`https://example.com//docs/a`)
  is stored under its own path. Umami read such a path as a host and stored
  `/a`, and a path such as `//search%20term` made Umami refuse the event,
  which went to the dead letter queue. The gateway also drops a referrer
  host that the browser URL parser refuses (`a<b`, `1.2.3.256`, a zone),
  which Umami refused the same way, and refuses a site origin whose last
  label is a number but not an IPv4 address.
- The disaster recovery runbook stops every Umami process before the import
  (`umami` and `umami-2` and up). Stopping process 1 alone left a connection
  to the `umami` database open, so `scripts/migrate.sh` could not drop it. The
  import now follows the tunnelled sequence of the handover guide and compares
  with `verify-migration` before the reconcile writes to the new cluster. The
  security patch and incident runbooks stop every Umami process too, and
  `tests/docs` refuses a runbook command that stops `umami` without `umami-2`.

### Upgrade notes

- Web events move from one subject per site to 32 partitions. The rolling
  deploy carries the queue across without losing or repeating an event:
  `nats apply` keeps the old subject and its consumer (now filtered to
  `web.events.*`) next to the partitions, the replayer drains the old
  consumer under the old `replayer` lock, and while both kinds are queued it
  keeps each visitor's events in order. After the deploy, once every gateway
  runs this release, run `platformctl nats retire-legacy-web`. The steps,
  expected log lines and the rollback are in
  [docs/deploy/upgrade.md](docs/deploy/upgrade.md), "Upgrading to
  partitioned web events".

## [0.4.0] - 2026-11-18

### Added

- Alerts on web events waiting for Umami: `WebEventsDelayed` (oldest over an
  hour), `WebEventsBacklogGrowing` (over 10,000 and still growing) and
  `WebEventsNearDiscard` (oldest over 48 hours, 24 hours before the queue
  drops it), with a runbook section
  ([docs/runbooks/alerts.md](docs/runbooks/alerts.md)).
- Cohorts for fair comparison: every project carries its SCF program, round,
  category and award date. The project proposes them at enrollment or on its
  "What SCF sees" page, and an operator confirms, edits, sets or dismisses
  them on the project's detail page; every change is in the audit log.
  Migration 00035 (`cp.tenant_cohorts`, `cp.threshold_rules`, row-level
  security under the rules of 00003)
  ([docs/operators/global-views.md](docs/operators/global-views.md),
  [docs/reference/control-plane-api.md](docs/reference/control-plane-api.md)
  section 19).
- Growth and percentiles in Global web and Global API: verified visitors,
  page views, requests, error rate and on-chain operations each carry the
  value of the previous period of equal length, the change, and the
  percentile inside the selected cohort, computed in SQL from the daily
  aggregates. Every change and percentile sorts; cohort filters (program,
  round, category, award date) narrow the tables and set the percentile
  population. The CSV exports carry every new column, and the compare
  exports mark the days after the award.
- Award dates as annotations on the compare charts and the project's own
  charts.
- Saved threshold rules: a metric (verified visitors, API requests or on-chain
  operations), a daily minimum, qualifying days per month over the last
  complete months, an optional minimum integrity grade and an optional
  cohort. The Rules page lists the projects that meet a rule with their
  qualifying days and grade, and exports them as CSV. Rules are audited and
  served by the machine API (`/rules`, `/rules/{rule_id}/matches`) and the
  operator MCP tools (`list_cohorts`, `list_threshold_rules`,
  `get_threshold_rule_matches`) ([docs/operators/threshold-rules.md](docs/operators/threshold-rules.md)).
- "What SCF sees" shows the project its cohort and proposal, its percentiles
  in its program and round, its changes, and the rules it meets.
- Signed usage reports: a member creates a report of the project for a
  period of closed UTC days, with web, API and on-chain totals and series,
  the integrity grade and its reasons, the cohort and percentiles, and the
  version of every method. The report is written in its RFC 8785 canonical
  JSON form and signed with the platform's Stellar report key using SEP-53;
  it is stored immutably and shared at `https://app.<BASE_DOMAIN>/r/<id>`,
  where the page verifies the signature in the browser and offers the JSON
  file. A member revokes a link at any time. Creation, views and revocation
  are rate-limited and audited. `platformctl report verify` checks a file
  offline or against the published keys. Migration 00036
  ([docs/onboarding/signed-reports.md](docs/onboarding/signed-reports.md),
  [docs/reference/usage-reports.md](docs/reference/usage-reports.md),
  control plane API section 20).
- The report signing key `GDG7D3Z267FHK3JHVPXF5SHTSQFEDLWYKEI5ECMSLDGKJUXPXZOJGF7O`
  is published under `ACCOUNTS` in `https://app.veristar.app/.well-known/stellar.toml`
  (served by the control plane from the key it signs with) and on the status
  page. New control plane settings `REPORT_SIGNING_SEED`, `REPORT_SIGNING_KEY`
  and `REPORT_SIGNING_RETIRED_KEYS`; the SOPS keys `report_signing_seed` and
  `report_signing_key` are generated by `new-secrets.py` and kept by
  `rotate-secrets.py`, with their own rotation procedure
  ([docs/runbooks/secret-rotation.md](docs/runbooks/secret-rotation.md)).
- The removal purge deletes a removed project's usage reports (step
  `reports`).
- Passkey sign-in: **Continue with a passkey** on the sign-in page creates a
  [soropass](https://docs.soropass.dev/docs) passkey or signs in with one the
  browser holds. A soropass passkey's ES256 key is the signer of a Stellar
  smart account, and the account's contract address (C-address), derived from
  the credential id and the key the way the soropass AccountFactory on the
  public network deploys it, names the console account. The console runs the
  ceremonies with `@soropass/core` 0.3.1; the control plane verifies the
  WebAuthn assertion over a challenge of its own, bound to the console origin
  and relying party, with user verification, a five minute expiry and single
  use (`POST /api/auth/passkey/challenge` and `/verify`, contract 4.1.9). The
  page runs under the unchanged content security policy.
- A passkey links to and unlinks from an account like GitHub and a wallet
  (`/api/me/identities/passkey`); the last sign-in method stays. Owners invite
  a passkey account by its C-address, `OPERATOR_STELLAR_ADDRESSES` takes
  C-addresses of passkey operators, and deleting an account removes its
  passkey. Migration `00037` adds `passkey_address`, `passkey_credential_id`
  and `passkey_public_key` to `cp.users`; tenants read the address only.
- Weekly active accounts by the SCF Growth Hack definition ("an active account
  means the account that initiated at least one operation"): for each ISO week
  in UTC, the distinct accounts that initiated an operation involving the
  project's verified accounts or contracts. The `chain-activity` task reads
  each day's initiating accounts with its counts, from Horizon operations for
  accounts and from the envelopes of the event transactions through batched
  `getTransaction` calls for contracts, while Stellar RPC still holds them. An
  open week keeps only keyed hashes under a key held for that week; at the
  week's close the task stores the count and deletes the hashes and the key.
  A week that could not be read whole shows as missing with its reason, never
  as zero. The tenant on-chain panel, the operator column, compare and tenant
  detail, "What SCF sees", the CSV exports, the new machine route
  `GET /api/metrics/v1/onchain/weekly` and the MCP tools carry it
  ([docs/reference/onchain-sources.md](docs/reference/onchain-sources.md),
  [docs/reference/control-plane-api.md](docs/reference/control-plane-api.md)
  section 15.8). Migration 00038.
- Integrity filter: the Global web table, its CSV export,
  `get_global_web_overview` and `GET /api/metrics/v1/web/integrity` take
  `integrity_at_or_below` (`C` for grade C, `B` for B and C, `A` for every
  graded project or site day).
- "Appeal this grade" next to the integrity grade on the project overview and
  in "What SCF sees": it opens a new issue on the Integrity appeal form, filled
  in with the project slug, site domains and grades, period, grade, reason
  codes and rule set version, and no personal data. The grade stays automatic;
  operators read the appeal and decide
  ([docs/security/bot-and-inflation.md](docs/security/bot-and-inflation.md)
  section 9).
- Tracker 1.3.0 is the release the console recommends, and the snippet page
  offers "Send the page title" (`data-send-title="true"`) from it. Older
  versions keep working unchanged.
- The gateway accepts the 1.3.0 page signals: `fr` on events of a framed
  page and the never-visible notice (`t: "nv"`), which passes every check of
  an event and is counted, never queued. Wire format 1 stays: every new field
  is optional ([docs/reference/wire-v1.md](docs/reference/wire-v1.md)).
- Integrity rule version 2 adds `framed_share` (B at 25%, C at 50% of the
  page views that report signals) and `never_visible_share` (B at 30%). Both
  can only lower a grade; version 1 is still applied as written, so stored
  grades reproduce from their inputs. The report shows the signal counts and
  shares in each day's evidence.
- An estimate of the visits not measured because of privacy signals: the
  gateway counts, per site and UTC day, the requests for the tracker file
  and the part with `Sec-GPC: 1`, attributing each request to a site by the
  page origin in the `Referer` header. The share is on the project's
  overview, in "What SCF sees", as a sortable column of the operators' global
  web view, in the compare answer, the CSV exports, the machine API and the
  MCP tools, from 100 requests on. The method and its accuracy limits are in
  [docs/compliance/privacy-strategy.md](docs/compliance/privacy-strategy.md),
  section 6.
- Database migration `00039_web_signals_coverage`: `cp.web_signals_daily`,
  `agg.web_coverage_daily` and three signal columns of
  `agg.web_integrity_daily`, under row-level security.
- Web dashboard in the console. Each project has a **Web** page with page
  views, unique visitors, visits, bounce rate and visit duration for today, 7,
  30 or 90 days or any range of UTC days, each against the previous period;
  daily and hourly charts; breakdowns by page, entry and exit page, referrer,
  country, browser, operating system, device, custom event and UTM value (for
  sites that collect UTM); the visitors right now; the counted and the
  quarantined band; the integrity grade and the on-chain panel of the range.
  For the whole project or one site.
- Control plane routes under `/api/tenants/{tenant_id}/web/`: `summary`,
  `breakdown`, `hourly`, `realtime` and `sessions`
  ([docs/reference/control-plane-api.md](docs/reference/control-plane-api.md),
  section 18). Totals, days and the referrer, country, browser, OS and device
  breakdowns come from the daily aggregates operators read; the rest is read
  from Umami on the private network as the member's own Umami user. Engine
  reads are limited to 120 a minute per member.
- The Privacy page's session finder lists one site's sessions of a day, so an
  owner finds the session id for the erasure of one session in the console.
- `TestWebDashboardParity` compares every dashboard figure with Umami's own
  answer for the same websites and range.
- `make test-caddy-closed` fails when an internet-facing Caddy site refers to
  Umami.
- API traffic classes: CORS preflights (every `OPTIONS` request), health and
  probe routes (`/health`, `/healthz`, `/livez`, `/readyz`, `/ping`, `/status`,
  `/metrics`, `/actuator/health/liveness` and the other conventions listed in
  the collector reference) and requests from known health checkers and uptime
  monitors (`ELB-HealthChecker`, `kube-probe`, `GoogleHC` and others) are
  stored apart and kept out of the headline request volume, error rates and
  latency percentiles everywhere: the API traffic page, the operator views,
  the CSV exports, the machine API and the MCP tools. Each of them shows the
  count as `infrastructure_requests`. The rule set is hard-coded and
  versioned, version 1, with a golden corpus
  ([docs/reference/collector.md](docs/reference/collector.md), "Traffic
  classes"). Migration 00040 adds `traffic_class` to the three API metric
  tables and `api.traffic_class_rules`.
- The operator roster lives in the database and is managed on the console's
  new Operators page. Operator admins add operators by GitHub login or Stellar
  address, suspend, resume, promote, demote and remove them, and reset their
  passkeys; every change takes a reason and is audited. The roster always
  keeps one active operator admin
  ([docs/operators/roster.md](docs/operators/roster.md)). Migration 00041.
- The operator area needs a passkey: a step-up recorded on the session, valid
  for 12 hours by default. It uses the passkeys and the verification of
  passkey sign-in; an operator who signs in with a passkey confirms with it,
  and every operator can add more passkeys, the first one inside a 24-hour
  enrollment window. Machine API keys and operator MCP credentials work
  without it.
- The console's API traffic page names the infrastructure requests the
  figures leave out, and the routes list marks routes that only
  infrastructure requests reached.
- Alert `SystemdUnitFailed` (ticket): a platform `sa-*` systemd unit has been
  failed for 15 minutes, read from `sa_systemd_unit_failed`, which the new
  `sa-systemd-failed.timer` writes every 5 minutes on every node. It covers a
  failing backup, the leader's `pgbackrest verify` or `check`, and the
  start-at-boot units ([docs/runbooks/alerts.md](docs/runbooks/alerts.md#systemdunitfailed)).
  The platform has 49 alerts.
- OpenTofu variable `public_ingress_open` (default `true`): `false` limits TCP
  80, TCP 443 and UDP 443 to `admin_cidrs` without stopping anything
  ([docs/deploy/opentofu.md](docs/deploy/opentofu.md)).
- Signed usage reports name `api_metrics` method version 2: infrastructure
  requests are left out of every API number of a report
  ([docs/reference/usage-reports.md](docs/reference/usage-reports.md)).
- etcd compacts its history every hour (`--auto-compaction-mode=periodic`,
  `--auto-compaction-retention=1h`) and runs with an explicit 2 GiB quota
  (`--quota-backend-bytes`). `sa-etcd-defrag.timer` defragments each member
  once a week, one member at a time and only while every member is healthy.
  New alerts `EtcdDatabaseSpaceHigh` (the database file above half the quota)
  and `EtcdNoSpaceAlarm`, and the runbook
  [docs/runbooks/etcd-maintenance.md](docs/runbooks/etcd-maintenance.md).
- `TextfileStale` alerts when a node_exporter textfile writer stopped
  (`systemd.prom` older than 15 minutes, `reboot.prom` 30 minutes,
  `pgbackrest.prom` and `tls.prom` 2 hours), and `TextfileScrapeError` when
  node_exporter cannot read one.
- `InternalCertificateExpiringSoon` alerts 30 days before the private
  network's node certificate or internal CA expires, from
  `sa-tls-expiry.timer`.
- `BackupJobFailed` alerts when the newest run of a backup unit across the
  database nodes failed. Each run that was not skipped records its result
  (`sa-backup-result`), so the failure stays visible after the Patroni leader
  moves, which clears the unit's failed state.

- Every platform release carries its third-party licence report as release
  assets, `licences.json` and `console-bundle.txt`, next to
  `release-images.json`; `release-images.yml` and `release-workstation.sh`
  run the licence gate on the tag and stop the release when it fails
  ([docs/security/supply-chain.md](docs/security/supply-chain.md), section 7).

### Changed

- `make webpath`, `make console-e2e` and the browser matrix of `make
  gateway-it` run their browsers in a container of their own (test image
  `mcr.microsoft.com/playwright` v1.64.1, the workspace's Playwright
  release), so changes to the host's network interfaces no longer abort
  Chromium navigations. The specs, scenarios and checks are unchanged;
  `SA_E2E_BROWSERS=host` runs the browsers on the host
  ([CONTRIBUTING.md](CONTRIBUTING.md)).
- The capacity documents separate the ingest rate from the delivery rate:
  700 events a second per node is what a gateway accepts, measured without
  challenge requests, while one replayer delivers for the whole cluster. The
  local delivery measurements and what a backlog costs (the 72 hour discard,
  the NATS file store) are in
  [docs/reference/capacity.md](docs/reference/capacity.md). Corrected too:
  the proof key's lifetime, the CORS header the gateway sends, and the
  gateway's check order, which now lists the revoked-release check and where
  a never-visible notice leaves it.
- The tracker file `sdk.js` is served compressed with zstd or gzip when the
  browser accepts it; the integrity hash covers the decoded file, so pinned
  snippets keep working
  ([docs/reference/gateway-limits.md](docs/reference/gateway-limits.md)).
- The rolling `deploy.yml` and `reboot.yml` visit the core node holding the
  Patroni leader last, so a run does one switchover, to a replica that is
  already done, instead of up to three.

- Terms of use `2026-11-17-draft.5`: passkeys as a console sign-in method
  and the data a passkey sign-in stores, the fields tracker 1.3.0 no longer
  reads, the per-site counts of tracker file requests behind the estimate of
  visits not measured, the separate count of preflight, health check and
  probe API requests, the operator roster and step-up passkey data, the
  weekly active accounts, and the full list of project statistics SCF
  receives. It names the hosting operator and the privacy contact, and
  announces sub-processor changes in the changelog and a new terms version.
  Every project accepts it at its next sign-in.
- The core tier must be upgraded before the edge tier again: older jobs
  daemons refuse the new `signal_<name>` and `script_<name>` counters on
  `EDGE_STATS`, and older gateways refuse the events of tracker 1.3.0.
- `release-tracker.yml` packs and publishes the npm package only when the
  repository variable `NPM_PACKAGE` is set, and otherwise releases and
  attests `sdk.js` alone with a notice in the log.
- `OPERATOR_GITHUB_IDS` and `OPERATOR_STELLAR_ADDRESSES` are the bootstrap of
  the roster: at every start each listed identity is made an active operator
  admin. Taking an identity out of them no longer removes the person; an
  operator admin removes them on the Operators page.
- Deleting your account removes you from the roster and deletes your
  passkeys. The last active operator admin cannot delete their account.
- Umami is the engine only: it stores the events and computes the metrics,
  and its interface is closed to projects
  ([ADR 0005](docs/adr/0005-umami-as-engine-only.md), which supersedes ADR
  0004). Platform administrators reach Umami's interface over an SSH tunnel to
  a core node's loopback, `127.0.0.1:3300`
  ([docs/operators/umami-admin-access.md](docs/operators/umami-admin-access.md)).
- The MCP web tools read Umami through the same reader as the web dashboard,
  sharing its login cache.
- The dashboard availability check of the prober is the console plus the
  machine API read.
- `make umami-compat` checks Umami's API and data contract only.
- Members read the web aggregates of their own project under row-level
  security: migration 00042 grants `cp_tenant` read on `agg.web_daily_dim`.

- etcd 3.6.16 to 3.7.3, the newest release. See the upgrade notes for the
  rolling upgrade.
- Go modules: `github.com/jackc/pgx/v5` 5.12.0 to 5.13.0,
  `golang.org/x/crypto` 0.58.0 to 0.59.0, `golang.org/x/time` 0.15.0 to
  0.16.0, `github.com/oschwald/maxminddb-golang/v2` 2.9.0 to 2.9.1, and with
  them `golang.org/x/sync` 0.25.0, `golang.org/x/sys` 0.50.0 and
  `golang.org/x/text` 0.44.0. pgx now reloads the client certificate and key
  named by `PLATFORM_DB_SSLCERT` and `PLATFORM_DB_SSLKEY` for each new
  database connection, so a rotated certificate is used without a restart.
- npm: `cn` 0.4.0 to 0.5.2 (console and brand package) and
  `@vitejs/plugin-vue` 6.0.10 to 6.0.11 (Vue example).
- Base images at their current digests: `gcr.io/distroless/static-debian13`
  and `cc-debian13` (`nonroot`), `golang:1.27.4-trixie`, `caddy:2.11.9` and
  `caddy:2.11.9-builder`, `postgres:18.7-trixie` (the base of
  `postgres-ha`); `ghcr.io/astral-sh/uv` 0.13.7 to 0.13.8 in the Python
  example. Test images: `curlimages/curl` 8.11.1 to 8.22.0 and `registry`
  3.0.0 to 3.1.2.
- Signed usage reports are format version 2: `methods.integrity_rules`
  lists every integrity rule version that graded the period, and a report
  carries what "What SCF sees" shows next to the counts: the estimate of
  visits not measured because of privacy signals, whether web and API data
  exist, whether API metrics were lossy, the infrastructure requests left
  out, and the weekly active accounts. Version 1 reports keep their layout
  and still verify; the public report page reads both
  ([docs/reference/usage-reports.md](docs/reference/usage-reports.md)).
- A report ends four days before today at the latest: the daily aggregation
  and grading read the last three days again, so only older days are final.
  A later end answers 422 `period_not_final`.
- The integrity report and the appeal link name the rule versions that
  graded the period (`rule_versions`), not only the version applied now.
- On-chain figures that rank, compare or grade a project count verified ids
  only, as weekly active accounts do: the On-chain column and compare series,
  the on-chain key metric and its percentile, threshold rules on on-chain
  operations, "What SCF sees" and signed reports.
- The stellar.toml link accepts an `ORG_URL` on the `www.` form of the
  domain or with a trailing dot, and links both ways only for a verified
  account and a file for the public network.
- The SEP-53 ownership message names the console host and the project's
  name next to its id.
- Removing an id clears the proof of every contract proven through it.
  The deployer check says when a transaction called a contract, since a
  contract a factory created is not read; such a contract uses the admin
  check.
- The platform's internal probe project is no longer graded by the hourly
  integrity job.

### Removed

- `GET /api/sso/umami` and the console's `/sso-error` page.
- The `web.<BASE_DOMAIN>` host: its DNS record, Traffic Manager profile,
  Caddy site (`deploy/caddy/web.Caddyfile`) and certificate.
- The Veristar theme of Umami's interface (`deploy/caddy/umami-theme`), its
  tests and the replace-response Caddy plugin it needed.
- The control plane setting `WEB_ORIGIN` and the prober setting
  `PROBER_WEB_URL` with its `web host` check.
- `make test-caddy-web`.

### Fixed

- `sa-core-start` retries a failed start (every 20 s, up to six times in 30
  minutes) instead of staying failed when Docker's restart policy races it at
  boot and a container briefly reports a dependency as unhealthy.
  `sa-edge-start` and `sa-observability-start` retry the same way.
- The backup units run only on the Patroni leader in Azure mode too; on a
  replica they skip their run cleanly instead of failing.
- `jobs run aggregate-web --from` refuses a day before the raw retention
  cutoff, and the erasure follow-up skips such days: Umami answers them with
  zeros, which replaced the kept aggregates.
- `jobs run integrity` on a day before the regrade window keeps the day's
  stored network count and rule version, since its network keys are pruned;
  regrading it counted zero networks and could add `few_networks`. Such a day
  that was never graded stays ungraded.
- A site reset also deletes the site's integrity grades, coverage estimates,
  gateway outcome counters, network class evidence, page signals and network
  keys, not only `agg.web_daily` and `agg.web_daily_dim`.
- `WEB_RAW_RETENTION_MONTHS` below 25 is refused at startup; tenant
  dashboards need 24 full months of raw rows.
- The down steps of migrations 00031, 00034 and 00037 see the rows they
  guard against: forced row-level security hid every row from the migration
  role, so their refusals never fired. Only the down sections changed.
- Cumulative metric streams that the collector's rewriting made identical
  (two libraries recording the same route, an unknown or lower-case method,
  a long `error.type`, a route literal the normalizer replaces) are counted
  apart again: each cumulative point carries a stream id the collector
  computes from what it arrived with (`scf.stream`), part of the writer's
  stream state and never a row dimension. Before, such streams were counted
  from each other's values and stored too few or far too many requests.
- An export that is larger than 1 MiB after the collector's filtering is
  stored instead of being answered 503 forever: every NATS server takes
  messages up to 8 MiB (`max_payload`), and an export above that limit is
  refused with 400 instead of a retry that can never succeed.
- A data point with a value the platform cannot store (time 0 or past 2262,
  a start after its time, bounds that are not finite and increasing, no or
  mismatched buckets, a count above 10^12, a sum that is not finite, an
  unspecified temporality) is dropped alone by the collector and counted in
  `tenantguard_invalid_items`, and the metrics writer leaves out such a point
  alone too. Before, the whole message went to `API_DLQ` and fired
  `DLQNotEmpty`. A server span without a start or with an end the platform
  cannot store is dropped alone instead of failing its trace export.
- One point adds at most 10^9 requests, so no tenant's counts can overflow
  the sums the platform keeps; and an hour the database still refuses to
  roll up is parked (`parked_at`, `parked_error` in `api.rollup_pending`,
  migration 00046, alert `APIRollupParkedHours`) instead of rolling back the
  batch and stopping the rollup of every tenant.
- The operator API views count `services`, `routes` and `has_api_data` from
  application requests only, and order the compare services by application
  requests, so a health check route no longer counts as a route.
- The machine API reference says that `/api/hourly` includes the hours the
  rollup has not reached yet, and that a day or hour with only
  infrastructure requests has an item with `requests` 0.

### Security

- Umami's Next.js application, its session tokens and its share and report
  routes have no path from the internet. No session token travels in a URL any
  more: the control plane holds a member's Umami token in memory for at most
  5 minutes and never sends it to a browser.
- Every web dashboard read passes the membership check, row-level security,
  a site check among the project's own sites, and, for Umami reads, Umami's
  own team check as a second gate. The isolation gate covers all five routes.
- The page signals of tracker 1.3.0 can no longer be forged in bulk to lower
  another project's integrity grade. A page view counts toward the framed
  share only when it is queued in the primary band, and a never-visible
  notice now passes the same-network and per-visitor volume checks and counts
  only when an event of that visitor would have stayed in the primary band
  ([docs/security/bot-and-inflation.md](docs/security/bot-and-inflation.md)
  section 9, [docs/security/threat-model.md](docs/security/threat-model.md)
  section 3.4).
- The page hostname is redacted like the referrer host before an event is
  queued. A site registered with a wildcard origin accepts any subdomain
  label, so a Stellar key used as a label no longer reaches Umami.
- Requests for the tracker file count toward a site's unmeasured-share
  estimate at most 60 at once and one per 10 seconds per site and address
  prefix on each node, so forged `Referer`, `Sec-Fetch-Dest` and `Sec-GPC`
  headers from one network can no longer push the share toward 0% or 100%.
  The file itself is still served to every request and stays cacheable
  ([docs/compliance/privacy-strategy.md](docs/compliance/privacy-strategy.md)
  section 6).

- pgx 5.13.0 fixes GHSA-w9rp-vfrh-q8vv, a panic in `Rows.Values` on a
  malformed server response.
- `golang.org/x/crypto` 0.59.0 carries the fix for GO-2026-5932
  (`x/crypto/openpgp`, never imported here); govulncheck reports no
  vulnerability in either Go module.
- An operator account changes its sign-in methods (link or unlink GitHub, a
  wallet or a passkey) only after a passkey step-up; the account page asks
  for it. A sign-in passkey steps up only when the roster names its address,
  so a passkey linked from a stolen session never opens the operator area,
  and unlinking the last step-up passkey answers 409 `last_passkey`.
- An ended console session stays ended: a request still running during a
  sign-out, a roster suspension, removal or passkey reset, or an account
  deletion no longer writes the session back. A token renewal at a step-up
  keeps the sign-in's 7-day deadline instead of starting a new one.
- The console cookies carry the `__Host-` prefix (`__Host-sa_session`,
  `__Host-sa_oauth`, `__Host-sa_stellar`, `__Host-sa_passkey`, all at
  `Path=/`), so no other host of the domain can set them.
- `platformctl report verify` trusts Veristar's own stellar.toml, built
  into the tool, instead of the one a report names; `--toml` names another
  deployment's file. Every text it prints from a report is quoted, so a
  project name cannot add a line to the verdict.
- A SEP-53 ownership proof holds only while the account's master key can
  authorise a payment alone (weight above 0 and at least the medium
  threshold); otherwise it answers `master_key_no_weight` and the data entry
  method proves the account. A challenge is spent in the same statement
  that checks it is still open.
- The public report page shows no figure until the signature holds.
- Report verifiers read a number by its value as RFC 8785 does and refuse a
  lone surrogate escape, the same in the CLI and the browser; every route
  serves a report member as the exact signed bytes.
- A report link's revocation is fixed once written: the database sets its
  time, and neither the time nor who revoked changes afterwards.
- The control plane refuses to start, and the chain-activity task refuses to
  read, when Horizon or RPC serve another network than the public one.

### Upgrade notes

In the order of the rollout: before the deploy, during the rolling deploy
(`deploy.yml`, [docs/deploy/upgrade.md](docs/deploy/upgrade.md)), then after it.

**Before the deploy**

- This release is signed in key mode on a workstation, while the
  repository's GitHub Actions are off
  ([docs/security/supply-chain.md](docs/security/supply-chain.md) section
  2.1). `infra/ansible/vars/signing.yml` verifies keyless releases, so add
  `ANSIBLE_EXTRA='-e sa_signature_mode=key'` to every `make <env>-deploy`;
  without it `verify-release.yml` refuses the release before any node is
  touched.
- Add the usage report signing pair to the SOPS file of an existing
  environment before you deploy: the control plane refuses to start without
  `REPORT_SIGNING_SEED` and `REPORT_SIGNING_KEY`, and `new-secrets.py`
  writes them only for a new environment. Run
  `platformctl report keygen /dev/shm/report-key` on the controller and write
  both files into `infra/secrets/<env>/secrets.enc.yaml` with `sops set`, as
  in steps 1 and 3 of
  [the usage report signing key](docs/runbooks/secret-rotation.md#the-usage-report-signing-key)
  (there is no current key to retire).
- Remove `WEB_ORIGIN` from the control plane environment and `PROBER_WEB_URL`
  from the prober environment if you set them by hand; the templates no longer
  write them.
- Optional settings: `OPERATOR_STEP_UP_MAX_AGE` (default `12h`) and
  `OPERATOR_ENROLL_WINDOW` (default `24h`), set through
  `sa_operator_step_up_max_age` and `sa_operator_enroll_window` in the
  inventory; the core role passes the defaults when they are absent.
- etcd moves from 3.6.16 to 3.7.3. etcd supports this as a rolling upgrade
  from 3.6.11 or later. Before the deploy, check
  `etcdctl endpoint health --cluster` and take a snapshot
  (`etcdctl snapshot save`) on one member. Once every member runs 3.7, going
  back to 3.6 needs etcd's downgrade procedure or the snapshot, not a pin
  change ([docs/security/supply-chain.md](docs/security/supply-chain.md),
  section 8.2).

**During the rolling deploy**

- `deploy.yml` now stops the jobs daemon on every core node before the
  migrations, and each node's core role starts it again with the new image.
  A jobs daemon of `0.3.0` still running on a node not yet updated would roll
  up an hour's minute rows without their traffic class and store every class
  under application traffic. As a second guard, the API rollup compares hourly
  rows with their minute rows per traffic class, and the first check of each
  new jobs process reaches back to `api.traffic_class_rules.applied_from`
  (at most 8 days), so such an hour and its day are recomputed. If a deploy
  stops before a core node is updated, its jobs daemon stays stopped: run the
  deploy again.
- Run the database migrations (`00035_cohorts_and_rules.sql` to
  `00046_rollup_parked_hours.sql`) before the new control plane and jobs
  start; the rolling `deploy.yml` does this in its bootstrap step, right
  after the signature check. Migration 00043 lets the jobs role read
  `api.traffic_class_rules`; 00044 and 00045 are needed by the new control
  plane and jobs. Between migration 00040 and the new metrics writer, the
  writer of the earlier release cannot store minute rows (the table's key
  changed); its messages wait in `API_METRICS` and are applied by the new
  writer, as during any writer restart.
- Upgrade the core tier before the edge tier: the rolling deploy runs the
  core nodes, then the edge nodes. The new metrics writer continues each
  cumulative stream it already follows when the new collector's first point
  with a stream id (`scf.stream`) arrives, from the stream's stored state, so
  the change of identity counts no history twice. A metrics writer of the
  earlier release would take those points for new streams and count each
  process' history again. Streams that two source series had shared before
  the upgrade continue once from that shared state, which they had already
  miscounted. A collector of the earlier release sends no traffic class, so
  its requests are stored as application traffic until the edge nodes run
  the new one.
- NATS `max_payload` rises from the 1 MiB default to 8 MiB
  (`roles/edge/templates/nats.conf.j2`). The edge role applies a changed
  `nats.conf` with its reload handler (SIGHUP), which takes `max_payload`
  without a restart; run the edge tier one node at a time as usual. Until
  every node has the new configuration, an export above 1 MiB can still be
  answered 503 and is retried by the SDK. A client keeps the limit the
  server announced when it connected: the new collector renews its
  connection when an export is above that limit, and the core services
  publish nothing near it. The dev stack mounts
  `deploy/compose/nats-dev.conf`; recreate its `nats` container
  (`make dev-up`).
- The edge play of `deploy.yml` recreates the etcd container of each node
  with etcd 3.7.3 and the new compaction and quota flags, one member at a
  time, and waits for it to be healthy before the next node. Do not move it
  with `site.yml`, whose edge play recreates every member at once. Patroni
  keeps its leader through the upgrade.

**After the deploy**

- A cluster that ran without compaction keeps its etcd database file at its
  size until it is defragmented: compact and defragment once, one member at
  a time, as in
  [docs/runbooks/etcd-maintenance.md](docs/runbooks/etcd-maintenance.md).
- The deploy installs the timers `sa-etcd-defrag` and `sa-tls-expiry` and the
  backup result writer. `BackupJobFailed` sees a backup unit after its first
  run that was not skipped.
- The session cookie is renamed to `__Host-sa_session`. The control plane no
  longer reads `sa_session`, so everyone signs in again once after the
  upgrade; the old cookie expires on its own. A sign-in that is in progress
  during the upgrade (GitHub, wallet or passkey) starts again from the
  sign-in page.
- At the first start after the upgrade, every identity in
  `OPERATOR_GITHUB_IDS` and `OPERATOR_STELLAR_ADDRESSES` becomes an operator
  admin and gets a 24-hour window to register a first passkey. Each operator
  signs in after the upgrade, opens the operator area and registers a passkey
  within that window; an operator admin resets the passkeys of anyone who
  misses it. When no operator admin registered one in time, restart the
  control plane on one core node (`docker compose restart controlplane`):
  each identity of the settings that still has no passkey gets a new window.
  Then add the other operators on the Operators page and keep the settings
  to the identities that are your path back in after a lockout
  ([docs/runbooks/operators.md](docs/runbooks/operators.md)).
- Infrastructure requests are counted apart from the moment migration 00040
  runs (`traffic_classes.applied_from` in the overview answer). Data stored
  before keeps every request as application traffic, as it was counted then;
  a range that spans the upgrade compares numbers counted both ways.
- Links and bookmarks to `web.<BASE_DOMAIN>` stop working. Members find their
  web dashboard under **Web** in each project in the console.
- Once the release has soaked, apply OpenTofu for each environment. The plan
  destroys the Traffic Manager profile `web`, its three external endpoints
  (`web/0` to `web/2`) and the Cloudflare CNAME `web`; nothing else changes.
  The apply also rewrites `hosts.json` without the `web` hostname that the
  `0.3.0` templates read, so it is the point after which a rollback needs
  one more step: keep a copy of the earlier `hosts.json` before the apply,
  and a rollback to `0.3.0` after the apply first applies OpenTofu from a
  `v0.3.0` checkout
  ([docs/deploy/upgrade.md](docs/deploy/upgrade.md), "Rollback across an
  OpenTofu change").
- Verify reports of a deployment other than app.veristar.app with
  `platformctl report verify <file> --toml https://app.<BASE_DOMAIN>/.well-known/stellar.toml`.
- Ids proven before this release keep their proof: the weighted master key
  rule applies to proofs made from now on.

## [0.3.0] - 2026-11-16

### Added

- On-chain witness: a project declares the Stellar accounts and Soroban
  contracts it operates (the G and C ids of its enrollment answers are copied
  in), proves control of each, and gets their daily operations, transactions
  and distinct counterparties next to its web and API usage. Accounts are read
  from Horizon, contracts from the events they emit through Stellar RPC, by the
  hourly jobs task `chain-activity`, which paces its requests and backs off on
  429 and 5xx. A day it could not read is stored as missing with its reason,
  never as zero. Accounts are proven by a SEP-53 signed message from a wallet
  or a `veristar` data entry; contracts through a verified account that their
  instance storage names as admin or owner, or that deployed them, checked by
  recomputing the contract id. The view also checks the two-way SEP-1 link:
  the verified domain's stellar.toml lists the account in `ACCOUNTS` with
  `ORG_URL` on the domain, and the account's home domain is the domain
  ([docs/onboarding/onchain.md](docs/onboarding/onchain.md),
  [docs/reference/onchain-sources.md](docs/reference/onchain-sources.md),
  [docs/reference/control-plane-api.md](docs/reference/control-plane-api.md)
  section 15). Migration 00032.
- The operator views show an on-chain column in Global web and Global API, an
  on-chain section in Compare and in the project detail, and the machine API
  serves `/api/metrics/v1/onchain/ids` and `/api/metrics/v1/onchain/daily`.
  MCP tools `get_onchain_activity`, `get_scf_view` and `get_tenant_onchain`
  ([docs/operators/onchain-and-exports.md](docs/operators/onchain-and-exports.md)).
- CSV exports: Global web, Global API and both compare views export exactly
  the rows, range, filters and order on screen, and a project exports its
  overview's daily web, API and on-chain numbers. The server streams each file
  through the role and row-level security of the view it mirrors, and writes
  an `export.csv` audit row (section 16).
- **What SCF sees**: each project reads its own row of the operator views,
  its integrity grade and on-chain summary, computed by the operator views'
  queries for the same range (section 16.3).
- The stellar.toml signal keeps the valid accounts of `ACCOUNTS` (at most 50)
  as `accounts`.

- Wallet sign-in: the console's sign-in page offers **Continue with a Stellar
  wallet** next to GitHub. The wallet signs a SEP-10 challenge for the public
  network that is bound to the console's host, expires after five minutes and
  verifies once across every control plane instance; the control plane checks
  it with the Go Stellar SDK (`github.com/stellar/go-stellar-sdk` v0.9.0). The
  picker is the console's own, on the Stellar Wallets Kit 2.8.0 module API, so
  the strict content security policy holds
  ([docs/onboarding/sign-in.md](docs/onboarding/sign-in.md),
  [docs/reference/control-plane-api.md](docs/reference/control-plane-api.md)
  section 4.1.7). Migration 00034.
- Linked sign-in methods: an account links a GitHub account and a Stellar
  wallet, each proven in the same session, and unlinks either while the other
  remains (section 4.1.8). Owners add members and send invites by GitHub
  username or by Stellar address. The optional setting
  `OPERATOR_STELLAR_ADDRESSES` grants operator rights to wallet accounts,
  beside `OPERATOR_GITHUB_IDS`.
- A shared wallet helper for console pages
  (`apps/console/src/lib/stellar-wallet` and `components/wallet-picker.tsx`):
  wallet list, connect, transaction signing and SEP-53 message signing.
  Every package it puts in the console bundle carries a permissive licence:
  `@lobstrco/signer-extension-api` is moved from 2.0.0 (GPL-3.0), which the
  kit pins, to 3.0.0 (Apache-2.0), and xBull is reached through its
  extension's own API instead of the kit's AGPL-3.0 connector library.

- Integrity grade of web traffic: every site gets an A, B or C grade per UTC
  day with the reasons that set it, computed by the new jobs task `integrity`
  from evidence the platform's edge observes: the network class of each
  event, the distinct networks, the quarantine band, proof-of-work failures
  and the site's own baseline. What the page reports can only lower a grade.
  Operators see it as a sortable column in the global web table, in compare
  and in the tenant detail, with a reasons panel; projects see the same grade
  and reasons on their own overview. It is served at
  `GET /api/tenants/{tenant_id}/integrity`, `GET /api/operator/web/integrity`
  and `GET /api/metrics/v1/web/integrity`, on every `/web/daily` item, in the
  MCP tools `get_web_integrity` and `get_tenant_integrity`, and as
  `integrity_grade` columns in the CSV exports
  ([docs/security/bot-and-inflation.md](docs/security/bot-and-inflation.md)
  section 9, [docs/reference/control-plane-api.md](docs/reference/control-plane-api.md)
  section 17). Migration 00033.
- Network class at the edge: the gateway classifies the network of every
  event as access, organisation, hosting, anonymiser or unknown from open data
  baked into its image and pinned by month: DB-IP IP to ASN Lite (CC BY 4.0),
  ipverse as-metadata categories (CC0 1.0) and Tor exit lists (CC0)
  ([docs/reference/geo.md](docs/reference/geo.md)). Only the class and a daily
  keyed hash of the network leave the gateway; no address and no network
  number is stored.

### Changed

- Terms of use `2026-11-16-draft.4`: the hosting operator runs the service as
  processor for each tenant, and SCF, which does not run the service, receives
  aggregate statistics about each enrolled project on the tenant's instruction
  given at enrollment (section 1.1). The processing terms, the records of
  processing and the privacy strategy follow. Every project accepts the new
  version at its next sign-in.
- `/api/me`, members, invites and the operator tenant detail carry
  `stellar_address` and `display_name`; `github_id` and `github_login` are
  `null` for a wallet-only account. A deleted account's row now holds nulls
  instead of `0` and an empty login.
- New Umami users are named `u-<account id>` and the name is stored with the
  account, so linking or unlinking GitHub never renames a dashboard user.
  Existing `gh-<github id>` users keep their names.

- The same-network threshold follows the network class and the site's size:
  `IPCLUSTER_THRESHOLD` is the floor for access networks and grows with the
  site's typical daily visitors, and hosting networks and Tor exits get a
  quarter of it. Popular and mobile-first sites are no longer quarantined for
  their audience's carrier-grade NAT. Each site's scale reaches the gateways
  in its registry entry.

### Upgrade notes

- Optional configuration of the on-chain witness, for the control plane and
  the jobs process: `STELLAR_HORIZON_URL` (default
  `https://horizon.stellar.org`), `STELLAR_RPC_URL` (default
  `https://mainnet.sorobanrpc.com`), `STELLAR_HORIZON_RPS` (default 0.8) and
  `STELLAR_RPC_RPS` (default 2). Both processes need outbound HTTPS to the two
  hosts. Removal now runs a `chain` purge step after `agg`. The core role
  passes the defaults to both (`core_stellar_env`).
- The optional Ansible variable `sa_operator_stellar_addresses` sets
  `OPERATOR_STELLAR_ADDRESSES`; empty, the default, means no wallet operator.

- Roll out the jobs and the control plane before the gateways. The gateways
  now send evidence counters on `EDGE_STATS` that an older jobs daemon refuses
  as malformed. The rolling `deploy.yml` now runs the bootstrap first, then
  the core nodes, then the edge nodes
  ([docs/deploy/upgrade.md](docs/deploy/upgrade.md)).

### Security

- Umami advisory GHSA-vhf9-rcmp-v5fc (SQL injection in the legacy reports
  API) does not affect the pinned Umami 3.5.0, and the web Caddy refuses its
  routes. A test against the real Caddy and Umami stack proves both
  ([docs/security/supply-chain.md](docs/security/supply-chain.md), section 8.1).
- The hourly security workflow files one issue per image, updated in place,
  lists each finding once, and skips findings that carry a recorded decision
  in `docs/security/advisories-reviewed.json`.

## [0.2.3] - 2026-11-15

### Changed

- Nodes and the deploy controller pull the platform images from `ghcr.io`
  without signing in, because the image packages are public. A deploy signs
  every node out of the registry and removes the stored pull token; the token
  is now needed only for private packages.
- Releases are signed keyless by `release-images.yml` with the workflow's
  GitHub identity, logged in Rekor, with build provenance and SBOM
  attestations. Deploys verify that identity at the release tag instead of the
  release key.

### Security

- Moved past the npm advisories GHSA-mrhx-c7xc-m3w5 (zod), GHSA-528h-pc64-c93x,
  GHSA-mjw6-4jj6-33hc and GHSA-hqr4-qq8f-hg3x (stream-json),
  GHSA-w5hq-g745-h8pq (uuid), GHSA-mh32-jxcr-mh9f and GHSA-r3h5-4585-7x3w
  (astro). All of them were in test dependencies and example apps only; the
  console, the website, the tracker and the images are unchanged by it.

## [0.2.2] - 2026-11-14

### Added

- Console accounts can be deleted by their owner in the account area, through
  `DELETE /api/me` (migration 00031). Deletion
  removes the account's GitHub id, login and avatar, its project memberships
  with their web dashboard access, its sessions, MCP access keys and OAuth
  tokens, and the invites addressed to it; it is refused while the account is
  the only owner of a project.
- Light theme for the console and the public website, with a system, light and
  dark choice. The Umami web dashboards follow the light mode with their own
  light palette.
- Public website, built as a static app from the same design as the console.
- `@veristar/brand` token package: colours, type scale, radii and shadows for
  the console, the website, the documentation and the Umami theme, generated
  from one source.

### Changed

- The console moved to the new design: Inter and JetBrains Mono, a terracotta
  accent, wider centred pages, projects shown as metric cards, and focus moved
  to the page on navigation. The pages from sign-in to the operator area were
  ported to the brand tokens.
- One-time secrets (OTLP tokens, operator API keys, MCP access keys) are shown
  in a labelled section with a labelled confirmation checkbox.
- The documentation was rewritten for public readers: onboarding guides with
  the npm package build steps and measured limits, the support page, the
  deploy pages, every runbook and the compliance pages.
- `reboot.yml` reboots a converged node once, in the core play after the
  PostgreSQL leader handover, and waits for its gateway before it undrains
  the node.

### Fixed

- A planned PostgreSQL switchover finds its candidate: Patroni presents a
  client certificate on its calls to the other members.
- The external prober health-checks its reused HTTP/2 connections with pings
  and bounds dialing, the TLS handshake and the response headers with its
  request timeout. After a node reboots, its checks recover on their own
  instead of staying red until the prober restarts.
- `rotate-all.yml` resets the stored Grafana admin password in the
  observability project, where Grafana runs.
- Runbook and documentation fixes: the NATS and etcd checks of the edge node
  runbook, the wait after draining a node that is also in the app and web
  answers, the targeted plan for resizing one node, the remote `az` login for
  the converge during a secret rotation, and stale section references.

## [0.2.1] - 2026-11-14

### Added

- `platformctl probe-tenant rotate-otlp-token` rotates the OTLP token of the
  probe tenant that the external prober reports with.
- Production Prometheus scrapes the prober and Gatus metrics of the external
  probe, and Alertmanager pushes its watchdog heartbeat to it.

### Fixed

- HAProxy on a node starts without waiting for that node's own PostgreSQL
  member to be healthy. It routes to the leader wherever that runs, so the
  node's services that reach the database through it stay up while a replica
  catches up after a reboot.
- A PostgreSQL replica that was off while the leader recycled the WAL it
  needs fetches that WAL from the pgBackRest repository instead of retrying
  the leader.

## [0.2.0] - 2026-11-14

### Added

- MCP server at `https://app.<BASE_DOMAIN>/mcp` with read-only tools for a
  project's web and API analytics. AI clients connect with an access key or
  through OAuth with a consent screen in the console; a connection acts as the
  person who made it and reads one project only. Its credentials stop
  working when the user revokes them, when the membership ends, when the
  project is suspended, rejected or removed, and when an operator loses the
  operator role. The console has an AI page with client setup snippets, and operators
  have MCP keys of their own.
- An alert on failing MCP tool calls.
- Umami dashboard theme: the Veristar mark, brand colours, self-hosted fonts
  and icons, served by the web Caddy and shipped in the console image. The
  Umami upgrade check fails when the class names the theme relies on change.
- Status page at `https://status.<BASE_DOMAIN>`, served by the external probe
  on a shared host behind that host's Caddy, with `sloreport watch` copying each ended day out
  of the probe's Gatus.
- `scripts/supply-chain/release-workstation.sh` builds, gates, signs and
  publishes a release from a workstation with the same checks as the release
  workflow.

### Changed

- Production runs on three converged nodes in Azure Denmark East, one per
  availability zone, with every service in every zone and three Patroni
  members.
- On converged nodes each service uses only its own node's NATS server,
  clients and routes ping every 5 seconds, and clients keep the configured
  server order. Tests pin these settings in the rendered role files.
- Caddy and the gateway run with `GOGC=200` and adaptive goroutine stacks; the
  event route is not access-logged, and the gateway keeps enough idle upstream
  connections for every request in flight.
- Node network interfaces use accelerated networking.

### Fixed

- The restore play sees the backup role's defaults and reports its result
  correctly.
- Observability containers start after boot, as the edge and core projects do.
- Edge peer templates no longer render whole host variables, which a backup
  role default broke on converged nodes.
- Rotating secrets to new recipients re-encrypts the private network's
  certificate files.

## [0.1.0] - 2026-11-13

The first release.

### Added

- Web analytics: a version-pinned tracker sends page views and custom events
  to an edge gateway that checks a server-issued proof of work, the page's
  origin, rate limits and a same-network rule, keeps the client address in
  memory only for the request, and queues the event in NATS JetStream. A
  replayer delivers it to an unmodified Umami 3.5.0, which is each project's
  web dashboard. No cookies and no stored IP addresses; visitors are counted
  per UTC day with a secret destroyed at midnight. A consent mode starts
  tracking only after the site calls `umami.start()`.
- Country lookup with a local DB-IP database; only the country code is kept.
- API analytics: an OpenTelemetry Collector distribution that drops traces,
  spans and logs, keeps HTTP request counts, status codes and latency
  histograms per normalised route, and a metrics writer that stores them in
  PostgreSQL with rollups for at least 24 months. Tenants send to it with
  per-project OTLP tokens. Example apps in Node.js, Go, Python and Rust.
- Control plane and console: GitHub sign-in, project enrollment with operator
  review and enrollment modes, members and invitations by GitHub id, sites
  with domain verification and `stellar.toml` checks, Umami provisioning per
  project, a privacy page, an API traffic dashboard and project removal.
- Operator area: global web and API views built from aggregates, comparison
  across projects, operator API keys for machine consumers, a machine metrics
  API, and a live alerts page reading Alertmanager over mutual TLS, with a nav
  badge and a banner for critical alerts.
- Erasure requests, raw event retention limits and the compliance documents:
  processing terms, records of processing, impact assessment, sub-processors
  and jurisdiction guidance.
- Operations: OpenTofu modules and environments, Ansible roles and playbooks,
  PostgreSQL 18.7 with Patroni and pgBackRest backups, Prometheus,
  Alertmanager and Grafana, an external prober and daily SLO reports,
  migration and credential rotation tooling, drill tooling, runbooks and
  deploy documentation.
- Release images signed with a cosign key pair and verified by every playbook
  before a node pulls them; private GHCR images are pulled with a read token.
- Monitoring deploys without an alert receiver when no Discord webhook is set;
  firing alerts then show on the console's alerts page.

## Tracker releases

Every version is served unchanged at `https://in.<BASE_DOMAIN>/v/<version>/sdk.js`
and pinned by its SRI hash, listed in
[packages/tracker/releases/manifest.json](packages/tracker/releases/manifest.json).
How to check a file against its release:
[docs/security/verify-tracker.md](docs/security/verify-tracker.md).

### Tracker 1.3.1 - 2026-11-17

- With `data-auto-track="false"`, `umami.track()` before `umami.start()` sends
  nothing, fetches no challenge, reads no browser storage and runs no
  `track(fn)` callback, so consent mode measures no one before the site calls
  `start()`. 1.3.0 sent such a call at once.
- `umami.start({ automatic: false })` starts `umami.track()` (and the Wallets
  Kit capture, where turned on) without automatic pageviews, navigation
  tracking or click capture, for a site that sends only chosen events.
- Wire format version 1 is unchanged; the gateway needs no update.
- 9,694 bytes, 4,385 gzip (1.3.0: 9,615 and 4,348, +0.9% gzip).

### Tracker 1.3.0 - 2026-11-16

- Counts only pages a person can see: a prerendered page on activation, a
  page loaded in a background tab when it is first shown, a restore from the
  back/forward cache again, and single-page navigation as before.
- Marks every event of a page inside a frame with `fr: true`, and sends a
  never-visible notice for a page left without ever being shown.
- Reads neither the screen size nor the browser language, and sends the page
  title only with `data-send-title="true"`. Browser, operating system and
  device come from the user agent; desktop browsers are `desktop`, no longer
  split into `laptop` by screen width.
- 9,615 bytes, 4,348 gzip (1.2.0: 8,743 and 4,058, +7.1% gzip).

### Tracker 1.2.0 - 2026-11-13

- Replaces the proof of work before the gateway's per-proof limit, so a
  single-page app that stays open for many events keeps sending, and resends
  an event once with a fresh proof after the gateway reports the proof used up.
- Sends the tracker version with each event, so the gateway can refuse events
  of a revoked release.
- Ignores a history call that does not change the URL, so the pageview of a
  back or forward navigation in Next.js keeps the previous page as its
  referrer.
- Never starts the Stellar Wallets Kit storage reading when `umami.start()` is
  called for a visitor with Global Privacy Control, the opt-out flag or an
  honoured Do Not Track.
- Refuses event names and data values that Umami cannot store, before sending
  them.

### Tracker 1.1.0 - 2026-11-12

- Stellar Wallets Kit capture: `wallet_selected`, `wallet_connected` and
  `wallet_disconnected` events with the kit's wallet id, never an address,
  turned on with `data-wallets-kit="true"` or the `attachWalletsKit` adapter
  of the npm package.

### Tracker 1.0.0 - 2026-11-12

- First tracker release: page views, custom events, a consent mode that
  starts tracking on `umami.start()`, the server-issued proof of work, and a
  byte-identical rebuild of every committed release.

[Unreleased]: https://github.com/justmert/veristar/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/justmert/veristar/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/justmert/veristar/compare/v0.2.3...v0.3.0
[0.2.3]: https://github.com/justmert/veristar/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/justmert/veristar/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/justmert/veristar/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/justmert/veristar/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/justmert/veristar/releases/tag/v0.1.0
