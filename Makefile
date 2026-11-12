# go is expected on PATH at the version pinned in go.mod (1.27.4).
GO            ?= go
GOLANGCI_LINT ?= golangci-lint
DOCKER        ?= docker

COMPOSE_FILE  := deploy/compose/dev.yml
DEV_ENV       := deploy/compose/.env.dev
COMPOSE       := $(DOCKER) compose --env-file $(DEV_ENV) -f $(COMPOSE_FILE)

VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN_DIR       := dist/bin

.PHONY: help dev-env dev-up dev-down dev-reset dev-ps dev-logs umami-bootstrap \
        build test test-integration vet lint fmt tidy image geo-fetch \
        db-migrate db-roles db-it sqlc umami-compat gateway-up gateway-it

help:
	@echo "dev-up dev-down dev-reset dev-ps dev-logs umami-bootstrap"
	@echo "build test test-integration vet lint fmt tidy image CMD=<name>"

dev-env:
	@sh deploy/compose/gen-env.sh $(DEV_ENV)

# --wait returns only when every service reports healthy, and fails otherwise
dev-up: dev-env
	$(COMPOSE) up -d --wait --wait-timeout 300

dev-down:
	$(COMPOSE) down

# removes the volumes, so the database starts empty with the seeded umami
# admin again. .env.dev is kept; run umami-bootstrap afterwards.
dev-reset: dev-env
	$(COMPOSE) down --volumes
	$(COMPOSE) up -d --wait --wait-timeout 300

dev-ps:
	$(COMPOSE) ps

dev-logs:
	$(COMPOSE) logs -f --tail 100

# loads the dev secrets into the environment of this one command only
umami-bootstrap: build
	@set -a && . ./$(DEV_ENV) && set +a && $(BIN_DIR)/platformctl umami bootstrap

# platform database. migrate first: it creates the roles that db-roles gives
# passwords. both read the PLATFORM_DB_* keys from .env.dev.
db-migrate: build
	@set -a && . ./$(DEV_ENV) && set +a && $(BIN_DIR)/platformctl db migrate

db-roles: build
	@set -a && . ./$(DEV_ENV) && set +a && $(BIN_DIR)/platformctl db roles

# runs platformctl's db commands against the dev stack from a test
db-it: dev-env
	@set -a && . ./$(DEV_ENV) && set +a && PLATFORM_DB_IT=1 $(GO) test -race -count=1 -run TestDBDevStack -v ./cmd/platformctl/

# regenerates internal/db/*q from db/migrations and db/queries, with the
# pinned sqlc version
sqlc:
	$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

build:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/ ./cmd/...

test:
	$(GO) test -race ./...

# downloads the db-ip country file the geo tests and the gateway image use.
# GEO_MONTH (YYYY-MM) defaults to the current utc month. the tests find the
# file through DBIP_MMDB=.scratch/scratch/geo/dbip-country-lite.mmdb
geo-fetch:
	sh scripts/fetch-dbip.sh .scratch/scratch/geo $(GEO_MONTH)

# runs the live tests against the dev stack; needs make dev-up and umami-bootstrap
test-integration:
	@set -a && . ./$(DEV_ENV) && set +a && UMAMI_IT_URL=$$UMAMI_URL $(GO) test -race -count=1 ./internal/umami/...

# the umami upgrade gate (plan phases 5 and 18): api checks in go, then the
# ui checks in playwright, both against the dev stack after umami-bootstrap
umami-compat:
	@set -a && . ./$(DEV_ENV) && set +a && UMAMI_IT_URL=$$UMAMI_URL \
		$(GO) test -race -count=1 -tags umamicompat -v ./tests/umami-compat/
	@set -a && . ./$(DEV_ENV) && set +a && UMAMI_IT_URL=$$UMAMI_URL \
		pnpm --filter @stellar-analytics/umami-compat test:live

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	$(GOLANGCI_LINT) fmt ./...

tidy:
	$(GO) mod tidy

CMD ?= platformctl
image:
	$(DOCKER) build -f deploy/images/go-service.Dockerfile \
		--build-arg CMD=$(CMD) --build-arg VERSION=$(VERSION) \
		-t stellar-analytics/$(CMD):$(VERSION) .

# jobs: the daemon and one-off task runs against the dev stack, and the tests
# of the jobs packages, the stats publisher and the edge caddy config (they
# start real nats, postgres and caddy containers)
.PHONY: jobs-daemon jobs-run test-jobs
JOBS_OPS_ADDR ?= 127.0.0.1:9104
TASK ?= edgestats

jobs-daemon: build
	@set -a && . ./$(DEV_ENV) && set +a && JOBS_OPS_ADDR=$(JOBS_OPS_ADDR) $(BIN_DIR)/jobs daemon

jobs-run: build
	@set -a && . ./$(DEV_ENV) && set +a && $(BIN_DIR)/jobs run $(TASK)

test-jobs:
	$(GO) test -race -count=1 ./cmd/jobs/ ./internal/jobs/... ./deploy/caddy/
	$(GO) test -race -count=1 -run 'TestStats' ./internal/gateway/

# collect gateway on the dev stack. needs dev-up and the nats topology
# (platformctl nats apply). the image has no shell for a compose health
# check, so readiness is polled on the loopback ops port.
DBIP_MONTH ?= $(shell date -u +%Y-%m)
gateway-up: dev-env
	DBIP_MONTH=$(DBIP_MONTH) $(COMPOSE) --profile edge up -d --build gateway
	@for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do \
		curl -fsS http://127.0.0.1:9090/readyz && exit 0; sleep 1; done; \
		echo "gateway not ready on 127.0.0.1:9090/readyz" >&2; exit 1

# this stage verification: real gateway image, nats, caddy and browsers
# (tests/gateway, build tag gatewayit). needs docker, pnpm with the
# playwright browsers, and DBIP_MMDB pointing at a fetched file (geo-fetch).
gateway-it:
	DBIP_MONTH=$(DBIP_MONTH) $(GO) test -tags gatewayit -race -count=1 -timeout 60m -v ./tests/gateway/

# web.<BASE_DOMAIN> caddy: caddy validate with the pinned
# image, then real requests through caddy in front of a private umami and
# postgres started from the dev.yml digests (build tag caddyweb), including the
# tenant dashboard in chromium (tests/caddy-web script test:live, which the go
# test runs; needs pnpm with the playwright browsers). screenshots land in
# .scratch/scratch/caddy-web.
.PHONY: test-caddy-web isolation-umami
test-caddy-web:
	$(GO) test -race -count=1 -run 'TestWebCaddyfileValidates' ./deploy/caddy/
	$(GO) test -tags caddyweb -race -count=1 -timeout 20m -run 'TestWebCaddyLive' -v ./deploy/caddy/

# tenant isolation inside umami: user b against every operation of the
# openapi contract with team a's ids, privately and through the web caddy
# (build tag isolation). starts its own umami and postgres.
isolation-umami:
	$(GO) test -tags isolation -race -count=1 -timeout 20m -v ./tests/isolation/umami/

# this stage end-to-end web path (tests/webpath, build tag webpath): real
# browsers on the example sites through the edge caddy, gateway, nats,
# replayer and umami, all in containers the test owns (images from the
# dev.yml digests), so umami, postgresql and replayers can be stopped and
# killed without touching the dev stack. needs docker, node 26 with pnpm and
# the playwright browsers, and the e2e fixture ports free. takes about an
# hour: the second visit of the visit check waits out a 31 minute gap.
.PHONY: webpath
webpath:
	DBIP_MONTH=$(DBIP_MONTH) $(GO) test -tags webpath -count=1 -timeout 120m -v ./tests/webpath/

# this stage browser flows (tests/e2e-console, build tag consolee2e): sign-in
# through the TEST-ONLY github oauth fixture, enrollment, sites, snippet,
# traffic through the real gateway, umami through sso, isolation, sso
# hardening and session security. the control plane is built with the
# oauthfixture tag; postgresql, nats, umami, the replayer, the gateway image
# and the edge, web and app caddies run in containers the test owns. needs
# docker, node 26 with pnpm and the playwright browsers. screenshots and, on
# failure, container logs land in .scratch/scratch/console-e2e.
# CONSOLE_E2E_ARGS passes extra playwright arguments (for example --grep).
.PHONY: console-e2e
console-e2e:
	DBIP_MONTH=$(DBIP_MONTH) $(GO) test -tags consolee2e -count=1 -timeout 60m -v ./tests/e2e-console/

# this stage otlp collector. collector-build generates and compiles the
# scf-otelcol distribution with the pinned ocb 0.165.0 and strict version
# checking (dist/scf-otelcol/scf-otelcol); collector-it runs the binary
# against a real nats server with the production user permissions, driven
# by the pinned go otlp exporters (tests/collector, build tag collectorit).
.PHONY: collector-build collector-it
OCB_VERSION := v0.165.0
collector-build:
	@mkdir -p dist
	$(GO) run go.opentelemetry.io/collector/cmd/builder@$(OCB_VERSION) \
		--config collector/builder-config.yaml --skip-strict-versioning=false
	./dist/scf-otelcol/scf-otelcol components >/dev/null

collector-it: collector-build
	$(GO) test -tags collectorit -race -count=1 -p 2 -timeout 20m -v ./tests/collector/
