# Security patch

The path from a vulnerability signal to a patched production, inside the RFP's commitment that
critical and high vulnerabilities are patched within 24 hours (RFP 3.4, the design). The target
for the whole path, detection to production, is **8 hours**, which leaves 16 hours of margin.

## 1. Where signals come from

| Signal | Source | Lands as |
|---|---|---|
| Umami repository advisory, new Umami release | `security.yml` hourly, `scripts/supply-chain/advisories.py` | issue labelled `security-finding`, alert to `ALERT_WEBHOOK_URL` |
| Next.js, React (as built into the pinned Umami), nats-server, Caddy, etcd, collector modules | same, GitHub advisory database at our exact versions | same |
| New PostgreSQL minor of the pinned major | same, postgresql.org `versions.json` | same |
| Fixable critical or high in a deployed digest | `security.yml`, Trivy on `release-images.json` and the upstream pins | same |
| Called Go vulnerability, npm advisory | `security.yml`, govulncheck and `pnpm audit` | same |
| Private report | GitHub private vulnerability reporting ([SECURITY.md](../security/SECURITY.md)) | security advisory draft |
| Dependency update | Dependabot | pull request |

The clock starts when the first signal is filed, not when someone reads it.

Umami has published advisories without stating a fixed version. Triage therefore never waits for
upstream wording: an unreviewed advisory is a finding by itself, and the decision is ours.

## 2. Triage (target: 1 hour)

1. Open the issue. Read the advisory and identify the component and our pinned version
   (`infra/ansible/vars/pins.yml`, `deploy/images/*.Dockerfile`, `go.mod`, `pnpm-lock.yaml`).
2. Decide whether we are exposed. Umami's collect routes, its admin and user routes and its share,
   link, pixel and board routes are never reachable from the internet
   ([umami-contract.md](../reference/umami-contract.md), `deploy/caddy/web.Caddyfile`). A flaw only
   in a route on that deny list is mitigated already; record that and still upgrade on the normal
   path.
3. Severity: use the advisory's. If it has none, treat it as high until proven otherwise.
4. Write the decision in the issue: exposed or not, mitigation, upgrade target, owner.
5. For an Umami repository advisory, add its id with the decision to
   `docs/security/advisories-reviewed.json` in the patch pull request, so the hourly check stops
   reporting it.

## 3. Immediate mitigations (target: within the first hour, when exposed)

Pick the narrowest one that removes the exposure. Each one keeps ingestion running, because the
gateways answer from NATS and do not need Umami or PostgreSQL.

- **Block a route at Caddy.** Add the path to the deny list in `deploy/caddy/web.Caddyfile` (or
  `app.Caddyfile` for the control plane), run `make test-caddy-web`, and roll out with the
  normal deploy. The deny-list tests prove the block.
- **Take the dashboards offline, keep ingestion.** On the core nodes stop the `umami` service (and
  the `controlplane` if the flaw is there): `docker compose stop umami`. Events queue in the
  `WEB_EVENTS` stream (72 hour retention) and the replayer delivers them when Umami returns, with
  their original timestamps. Post a notice on the status page.
- **Rotate exposed credentials.** OTLP tokens and operator API keys can be revoked in the console;
  `UMAMI_USER_PASSWORD_KEY` rotation invalidates every Umami session at once.

## 4. Upgrade

### Umami

```
make umami-upgrade VERSION=<x.y.z>
```

`scripts/umami-upgrade.sh` resolves the index digest the registry serves for that version, moves
the pin in every file that holds it (`deploy/compose/dev.yml`, `infra/ansible/vars/pins.yml`,
`tests/observability/compose.yml`, `docs/reference/umami-contract.md`), then runs, timed, stopping at
the first failure:

1. the dev stack recreated on the new image, so Umami migrates an existing database, and
   `umami-bootstrap`;
2. `make umami-compat` (the this stage behaviour checks in Go and Playwright). For a new version its
   reviewed route list (`tests/umami-compat/testdata/openapi-routes.txt`) names the old version and
   the gate stays red until someone reviews the new OpenAPI contract against the deny list and
   updates the list. That review is part of the patch;
3. `make isolation` (the tenant isolation gate);
4. `make console-e2e` and `make webpath` (the end-to-end suites).

The log is in `.scratch/scratch/umami-upgrade/`. Commit the diff only after every gate passed.

### Our own images

Bump the dependency (`go get`, `pnpm update`, a base image digest), run `make test`, `make isolation`
and the image scan (`scripts/supply-chain/scan-image.sh <image> <dir>` on a local build).

### Upstream images other than Umami

Move the tag and digest in `infra/ansible/vars/pins.yml` (and `deploy/compose/dev.yml` where
present), resolved from the registry, and run the suites that start that image.

### PostgreSQL

Change `PG_BASE` and `PG_VERSION` in `deploy/images/postgres-ha.Dockerfile` and `postgres_version`
in `vars/pins.yml`, rebuild, then run `make test`, `make isolation` and the this stage recovery
drills before rollout.

## 5. Release and roll out (target: 4 hours)

1. Merge the pull request (all required checks green, one review).
2. Tag `v<release>`. `release-images.yml` builds, scans, signs and attests every image and attaches
   `release-images.json` to the release.
3. Staging: `make staging-deploy ROLLING=1 SA_IMAGE_TAG=<release>
   SA_RELEASE_MANIFEST=<downloaded release-images.json>`. The
   deploy verifies every signature first and refuses anything unsigned or mismatched. Check the
   status page and the alerts.
4. Production: the same command for production.
5. Close the issue with the elapsed time from the first signal.

## 6. Caddy runs as non-root

Since this stage the Caddy and console images run as uid 10001 (`deploy/images/caddy.Dockerfile`).
New named volumes get the right owner from the image. A node whose `caddydata` and `caddyconfig`
volumes were created by an older root image needs one change before the first deploy of such an
image, or Caddy cannot write its certificates:

```
docker run --rm -v stellar-analytics-edge_caddydata:/data -v stellar-analytics-edge_caddyconfig:/config \
  --user 0 --entrypoint chown <caddy image> -R 10001:10001 /data /config
```

(`stellar-analytics-core_*` on core nodes.)

## 7. Rehearsal record

See section 8 of [supply-chain.md](../security/supply-chain.md) for scan results. Patch path
rehearsals are recorded here with their elapsed times.
