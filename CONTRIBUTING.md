# Contributing

This page covers the toolchain, the checks a change must pass, and the
conventions of the repository. Report security problems privately, as described in
[docs/security/SECURITY.md](docs/security/SECURITY.md).

## Before you start

- For anything larger than a small fix, open an issue first and agree on the approach with the
  maintainers.
- Read [docs/architecture.md](docs/architecture.md). Umami runs as its official image,
  unmodified ([ADR 0002](docs/adr/0002-overlay-not-fork.md)); a change that needs Umami's own
  code changed is a change to the design, not a patch.

## Toolchain

Versions are pinned, and the pins are the ones in the repository files:

| Tool | Version | Pinned in |
|---|---|---|
| Go | 1.27.4 | `go.mod` |
| golangci-lint | v2.15.0 | `.github/workflows/ci.yml` |
| Node.js | 26.13.1 | `package.json` `engines` |
| pnpm | 12.18.0 | `package.json` `packageManager` |
| Docker Engine and Compose | for the development stack and the integration suites | |

Dependencies follow the latest stable release, checked on the registry at the time they are
added or raised. New frontends use Vite and React.

## Development stack

The development stack runs PostgreSQL, NATS, Umami and the platform services in Docker
Compose (`deploy/compose/dev.yml`). The Makefile wraps it:

| Target | Does |
|---|---|
| `make dev-up` | generates `deploy/compose/.env.dev` with fresh secrets if it is missing, then starts the stack and waits until every service is healthy |
| `make umami-bootstrap` | replaces Umami's seeded admin and prepares it for the platform |
| `make db-migrate`, `make db-roles` | migrates the platform database and gives the roles their passwords |
| `make dev-ps`, `make dev-logs`, `make dev-down`, `make dev-reset` | status, logs, stop, and start again with empty volumes |

From a fresh clone, the whole stack with the console comes up in this order:

```sh
pnpm install --frozen-lockfile
make dev-up                   # postgresql, nats and umami
make umami-bootstrap
make db-migrate db-roles
set -a && . deploy/compose/.env.dev && set +a && dist/bin/platformctl nats apply
```

The control plane signs users in with GitHub. Create a GitHub OAuth App with the callback URL
`http://localhost:5173/api/auth/github/callback`, then add `GITHUB_CLIENT_ID`,
`GITHUB_CLIENT_SECRET` and `OPERATOR_GITHUB_IDS` (your numeric GitHub id) to
`deploy/compose/.env.dev`. Start the platform services and the console:

```sh
docker compose --env-file deploy/compose/.env.dev -f deploy/compose/dev.yml \
  --profile core up -d --build controlplane replayer metricswriter
make gateway-up               # the collect gateway, profile edge
CONTROLPLANE_URL=http://localhost:8085 pnpm --filter ./apps/console dev
```

Open the console at `http://localhost:5173`. The Vite dev server proxies `/api` to the control
plane, so the console, the session cookie and the OAuth callback share that one origin, which is
the `CONSOLE_ORIGIN` of the dev stack. Run the console on another port and the control plane
refuses its requests with `origin_mismatch`.

## Checks

Every change must pass these locally; CI runs the same:

```sh
make vet
make lint                     # golangci-lint
make test                     # unit tests only, starts no container
make test-containers          # the packages whose tests start postgresql, nats, umami or caddy
pnpm install --frozen-lockfile
pnpm check                    # biome and type checks
pnpm test                     # vitest and node test suites
make e2e-matrix               # the browser matrix of tests/e2e, browsers in a container
```

`make test` runs every package whose tests do not pull in testcontainers; `make
test-containers` runs the others, one package at a time (`-p 1`), and needs Docker.
`scripts/go-test-packages.sh` derives the split from each package's test imports, so a new
container test lands in `test-containers` without a build tag. The gateway geo tests need the
DB-IP country file: run `make geo-fetch`, which prints the path of the file it writes, then
`DBIP_MMDB=<that path> make test-containers`.

Suites that start real services, run when the change touches their area:

| Target | Covers |
|---|---|
| `make isolation` | the tenant isolation gate: every route and the database role matrix. Required for any change to the control plane, the database or Caddy |
| `make webpath` | the full web path in Chromium, Firefox and WebKit, about an hour |
| `make console-e2e` | the console in a browser |
| `make collector-it`, `make apipath` | the OTLP collector and the API path |
| `make privacy` | the privacy audit: no address or user agent anywhere it must not be |
| `make umami-compat` | Umami behaviour the platform depends on; required for an Umami upgrade |
| `make test-caddy-closed` | no public Caddy site reaches Umami |
| `make gateway-it` | the collect gateway image with NATS, Caddy and browsers |
| `make metricswriter-it`, `make api-volume` | metrics writer exactness and the 1,000,000 request volume run |
| `make enrollment-it` | enrollment modes end to end |
| `make otelapps` | the example apps' dual export and the tenant collector guide |
| `make recovery-it` | recovery after a full NATS restart; needs `make dev-up` and `umami-bootstrap` |
| `make handover-it` | the data migration and transfer rehearsal |
| `make promtool` | the alert and recording rule tests |

`make e2e-matrix`, `make webpath`, `make console-e2e`, `make handover-it` and the browser matrix of `make gateway-it` run their
browsers in the official Playwright image, pinned in `tests/fixtures/browserbox` to the
workspace's `@playwright/test` version, while the test runner stays on the host. Chromium aborts
navigations in flight whenever the host's network interfaces change, and in a container of its
own it sees none of those changes. The browsers reach the suites' proxies on the same loopback
ports as before. `SA_E2E_BROWSERS=host` runs the browsers on the host instead, for local
debugging; that mode needs the Playwright browsers installed (`pnpm exec playwright install`).

`.github/workflows/ci.yml` runs `test`, `test-containers`, `isolation`, `supplychain-it`,
`test-integration`, `promtool` and `docs-check`, plus the lint, licence, secret scan, OpenTofu
and Ansible checks, on every change. `.github/workflows/e2e.yml` runs every other
suite above, plus `db-it`, `local-converge` and `rotate-local`, each night and on demand, one
matrix job per suite.

The checks that run on every push and pull request are listed in
[docs/security/branch-protection.md](docs/security/branch-protection.md).

## Conventions

- **Tests use real systems.** Integration suites run against real PostgreSQL, NATS, Umami and
  browsers. Fixtures exist only for tests and live in `tests/`, `testdata/` or `*_test.go`.
- **No silent fallbacks.** When something fails, the code says so; it does not switch to a
  degraded path on its own.
- **Comments** are lowercase, plain, and explain why, not what.
- **Writing**: plain prose, and no em dash anywhere (code, comments, docs, commit messages).
- **Commit messages**: one short lowercase line describing the change.
- **Docs follow the code.** A change in behaviour updates the document that describes it in
  the same pull request. A statement in the docs must match the code or a recorded test or
  measurement.
- **Tracker releases never change in place.** A change to `packages/tracker` ships as a new
  version ([docs/security/verify-tracker.md](docs/security/verify-tracker.md)).
- **Secrets** never enter the repository unencrypted; `gitleaks` runs in CI.

## Licence of contributions

The repository is licensed under the Apache License 2.0, and `packages/tracker` under the MIT
License ([NOTICE](NOTICE)). Contributions are accepted under the licence of the directory they
change.
