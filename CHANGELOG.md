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
  hosts. Removal now runs a `chain` purge step after `agg`.

- Roll out the jobs and the control plane before the gateways. The gateways
  now send evidence counters on `EDGE_STATS` that an older jobs daemon refuses
  as malformed.

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

[Unreleased]: https://github.com/justmert/veristar/compare/v0.2.3...HEAD
[0.2.3]: https://github.com/justmert/veristar/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/justmert/veristar/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/justmert/veristar/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/justmert/veristar/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/justmert/veristar/releases/tag/v0.1.0
