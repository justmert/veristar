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
	@echo "build test test-containers test-integration vet lint fmt tidy image CMD=<name>"

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

# unit tests only, no containers: every package whose tests do not pull in
# testcontainers (scripts/go-test-packages.sh explains the split). the
# packages that start postgresql, nats, umami or caddy run in test-containers.
test:
	@pkgs="$$(GO=$(GO) scripts/go-test-packages.sh unit)" && $(GO) test -race $$pkgs

# the default-build packages whose tests start containers through
# testcontainers. -p 1 keeps one package's containers up at a time on a
# shared machine. build-tagged suites have their own targets below.
.PHONY: test-containers
test-containers:
	@pkgs="$$(GO=$(GO) scripts/go-test-packages.sh containers)" && \
		$(GO) test -race -count=1 -p 1 -timeout 90m $$pkgs

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
		pnpm --filter @veristar/umami-compat test:live

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	$(GOLANGCI_LINT) fmt ./...

tidy:
	$(GO) mod tidy

# documentation checks (the design): every relative link in docs/ and the
# root and example readmes resolves, and every code block a guide includes
# from examples/ equals its source. docs-update rewrites the included blocks
# from their sources after an example changed.
.PHONY: docs-check docs-update
docs-check:
	$(GO) test -count=1 ./tests/docs/

docs-update:
	$(GO) test -count=1 -run TestIncludedSnippets ./tests/docs/ -args -update

CMD ?= platformctl
image:
	$(DOCKER) build -f deploy/images/go-service.Dockerfile \
		--build-arg CMD=$(CMD) --build-arg VERSION=$(VERSION) \
		-t veristar/$(CMD):$(VERSION) .

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

# the whole tenant isolation gate (the design), required on every pull
# request and for every release. one target, so a gate cannot be half run:
# tests/isolation/... (build tag isolation) is umami cross-team through the
# web caddy, every console, operator and machine api route against the real
# router, and the behavioural database role matrix; internal/db holds the
# catalog grant matrix and the row-level security tests; the web caddy deny
# list and method allowlist are the edge rules the gate must notice losing.
# -p 1 keeps one suite's containers up at a time on a shared machine.
.PHONY: isolation
isolation:
	$(GO) test -tags isolation -race -count=1 -p 1 -timeout 60m -v ./tests/isolation/...
	$(GO) test -race -count=1 -v ./internal/db/
	$(GO) test -race -count=1 -run 'TestWebCaddyfileValidates' ./deploy/caddy/
	$(GO) test -tags caddyweb -race -count=1 -timeout 20m -v \
		-run 'TestWebCaddyLive/(deny_list|api_method_allowlist|sso_rules)$$' ./deploy/caddy/

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
# hardening, session security, and the this stage api traffic dashboard on
# real telemetry of the go example app. the control plane is built with the
# oauthfixture tag; postgresql, nats, umami, the replayer, the gateway and
# collector images, the metricswriter, a stock collector as the tenant's own
# backend and the edge, web and app caddies run in containers the test owns. needs
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

# this stage api path (tests/otelapps, build tag apipath): the four example apps
# export through the edge caddy (otlp.<BASE_DOMAIN>) to the built
# scf-otelcol with real nats, with tokens created through the control plane
# binary (oauthfixture build) on postgresql and umami. checks what reaches
# API_METRICS against the load plan, the tenant backend's full copy, and
# refusal within 5 s after revocation and suspension. needs docker.
.PHONY: apipath
apipath: collector-build
	$(GO) test -tags apipath -count=1 -timeout 90m -run TestAPIPath -v ./tests/otelapps/

# this stage metrics writer. metricswriter-up runs it on the dev stack (needs
# dev-up, platformctl nats apply, db-migrate and db-roles); the image has no
# shell, so readiness is polled on the loopback ops port. metricswriter-it is
# the exactness suite: the built scf-otelcol and metricswriter binaries with
# real nats and postgresql, driven by the pinned go otlp exporters
# (internal/metricswriter, build tag metricswriterit).
.PHONY: metricswriter-up metricswriter-it test-metricswriter
metricswriter-up: dev-env
	$(COMPOSE) --profile core up -d --build metricswriter
	@for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do \
		curl -fsS http://127.0.0.1:9096/readyz && exit 0; sleep 1; done; \
		echo "metricswriter not ready on 127.0.0.1:9096/readyz" >&2; exit 1

metricswriter-it: collector-build
	$(GO) test -tags metricswriterit -race -count=1 -timeout 25m -run TestMetricsWriter -v ./internal/metricswriter/

test-metricswriter:
	$(GO) test -race -count=1 ./internal/metricswriter/ ./cmd/metricswriter/

# this stage volume run (tests/apivolume, build tag apivolume): 1,000,000
# requests across 10 tenants and 30 services through the built scf-otelcol,
# metricswriter and jobs binaries with real nats and postgresql. loads
# .env.dev only for the umami keys `jobs run` reads. VOLUME_MINUTES (1 to 60,
# default 20) spreads the requests; results go to docs/reference/capacity.md.
.PHONY: api-volume
api-volume: collector-build
	@set -a && . ./$(DEV_ENV) && set +a && \
		$(GO) test -tags apivolume -count=1 -timeout 120m -v ./tests/apivolume/

# this stage system-wide privacy audit (tests/privacy, build tag privacy): web
# and api traffic from marker addresses on their own docker networks through
# the edge caddy, then both databases, every jetstream stream and kv bucket,
# every service and caddy log and every file the containers wrote are
# scanned for the markers. needs docker.
.PHONY: privacy
privacy:
	DBIP_MONTH=$(DBIP_MONTH) $(GO) test -tags privacy -count=1 -timeout 30m -v ./tests/privacy/

# the db-ip country file the gateway needs in the suites below (make geo-fetch)
DBIP_MMDB ?= $(CURDIR)/.scratch/scratch/geo/dbip-country-lite.mmdb

# this stage enrollment modes (tests/enrollment, build tag enrollmentit): the
# control plane routes in process with the oauth fixture, the reconciler,
# real postgresql, nats and umami, and the gateway and replayer binaries.
# needs docker and the db-ip file.
.PHONY: enrollment-it
enrollment-it:
	DBIP_MMDB=$(DBIP_MMDB) $(GO) test -tags enrollmentit -race -count=1 -timeout 30m -v ./tests/enrollment/

# this stage example apps (tests/otelapps, build tag otelapps): each app
# dual-exports through deploy/compose/examples.yml to a stock collector as
# the tenant backend and a platform stand-in, and both copies must equal the
# load plan; TestCollectorFanout checks the tenant collector guide's config.
# needs docker. OTELAPPS_APPS and OTELAPPS_MODES narrow the run.
.PHONY: otelapps
otelapps:
	$(GO) test -tags otelapps -count=1 -timeout 60m -v ./tests/otelapps/

# recovery after a full nats restart (tests/recovery, build tag recoveryit):
# gateway, replayer, jobs and metricswriter recreate the memory buckets and
# data flows again, on one server and on a 3-node cluster. needs make dev-up,
# umami-bootstrap and the db-ip file.
.PHONY: recovery-it
recovery-it:
	@set -a && . ./$(DEV_ENV) && set +a && UMAMI_IT_URL=$$UMAMI_URL DBIP_MMDB=$(DBIP_MMDB) \
		$(GO) test -tags recoveryit -race -count=1 -timeout 30m -v ./tests/recovery/

# this stage opentofu (infra/tofu, docs/deploy/opentofu.md). credentials and the
# state passphrase come only from the env's sops dotenv file; non-secret inputs
# from infra/tofu/envs/<env>/terraform.tfvars. both are missing until the operator
# supplies inputs I4, I5, I6, I13 and I14, and the targets stop with the name
# of what is missing instead of guessing.
TOFU     ?= tofu
SOPS     ?= sops
TOFU_DIR := infra/tofu/envs
# one shared provider cache so both envs reuse the same provider binaries
export TF_PLUGIN_CACHE_DIR ?= $(HOME)/.cache/opentofu/plugins

define tofu_env
	@test -f $(TOFU_DIR)/$(1)/terraform.tfvars || { echo "$(TOFU_DIR)/$(1)/terraform.tfvars is missing (inputs I4, I5, I13)" >&2; exit 1; }
	@test -f infra/secrets/$(1)/tofu.enc.env || { echo "infra/secrets/$(1)/tofu.enc.env is missing (inputs I5, I6, I14)" >&2; exit 1; }
	@mkdir -p $(TF_PLUGIN_CACHE_DIR)
	$(SOPS) exec-env infra/secrets/$(1)/tofu.enc.env '$(2)'
endef

tofu_in = $(TOFU) -chdir=$(TOFU_DIR)/$(1)
tofu_init = $(call tofu_in,$(1)) init -input=false
tofu_inventory = mkdir -p infra/ansible/inventories/$(1) && $(call tofu_in,$(1)) output -json ansible_inventory > infra/ansible/inventories/$(1)/hosts.json.tmp && mv infra/ansible/inventories/$(1)/hosts.json.tmp infra/ansible/inventories/$(1)/hosts.json

.PHONY: state-bootstrap tofu-check \
        staging-plan staging-apply staging-inventory staging-destroy \
        production-plan production-apply production-inventory production-destroy

state-bootstrap:
	@case "$(ENV)" in staging|production) ;; *) echo "usage: make state-bootstrap ENV=staging|production" >&2; exit 2;; esac
	@test -f infra/secrets/$(ENV)/tofu.enc.env || { echo "infra/secrets/$(ENV)/tofu.enc.env is missing (inputs I5, I14)" >&2; exit 1; }
	$(SOPS) exec-env infra/secrets/$(ENV)/tofu.enc.env './scripts/tofu-state-bootstrap.sh $(ENV)'

# the plan file is encrypted by the env's encryption block
staging-plan production-plan: %-plan:
	$(call tofu_env,$*,$(call tofu_init,$*) && $(call tofu_in,$*) plan -input=false -out=tfplan)

staging-apply production-apply: %-apply:
	@test -f $(TOFU_DIR)/$*/tfplan || { echo "run make $*-plan first" >&2; exit 1; }
	$(call tofu_env,$*,$(call tofu_init,$*) && $(call tofu_in,$*) apply -input=false tfplan && rm -f $(TOFU_DIR)/$*/tfplan && $(call tofu_inventory,$*) && ./scripts/tofu-state-backup.sh $*)

# the state bucket keeps no history; every apply copies the encrypted state
# into the versioned, object-locked backup bucket (docs/deploy/opentofu.md
# section 6). STATE_ARGS=list or STATE_ARGS="restore <key>" for the rest.
.PHONY: staging-state-backup production-state-backup
staging-state-backup production-state-backup: %-state-backup:
	$(call tofu_env,$*,./scripts/tofu-state-backup.sh $* $(STATE_ARGS))

staging-inventory production-inventory: %-inventory:
	$(call tofu_env,$*,$(call tofu_init,$*) && $(call tofu_inventory,$*))

staging-destroy:
	$(call tofu_env,staging,$(call tofu_init,staging) && $(call tofu_in,staging) destroy -input=false)

# production servers and addresses carry delete protection, so this fails
# until protect is turned off in envs/production/env.tf and applied
production-destroy:
	@test "$(CONFIRM)" = production || { echo "set CONFIRM=production to destroy production" >&2; exit 1; }
	$(call tofu_env,production,$(call tofu_init,production) && $(call tofu_in,production) destroy -input=false)

# local checks that need no account: formatting, provider schema validation
# and the inventory contract tests (tests/infra) against mocked providers
tofu-check:
	@mkdir -p $(TF_PLUGIN_CACHE_DIR)
	$(TOFU) fmt -recursive -check infra/tofu tests/infra
	@for e in staging production; do \
		$(TOFU) -chdir=$(TOFU_DIR)/$$e init -backend=false -input=false >/dev/null && \
		$(TOFU) -chdir=$(TOFU_DIR)/$$e validate && \
		$(TOFU) -chdir=$(TOFU_DIR)/$$e test -test-directory=../../../../tests/infra \
			-var-file=../../../../tests/infra/sample.tfvars \
			-filter=../../../../tests/infra/$$e.tftest.hcl || exit 1; \
	done

# ansible (this stage stream b, docs/deploy/ansible.md). the controller tools
# live in their own venv, made with uv from infra/ansible/requirements.txt.
# staging-deploy and production-deploy converge every node (site.yml) at the
# release SA_IMAGE_TAG; ROLLING=1 runs deploy.yml instead, one node at a time
# gated on the port-80 health. the inventory comes from make <env>-apply.
ANSIBLE_VENV ?= $(HOME)/.local/share/veristar/ansible-venv
ANSIBLE_BIN := $(ANSIBLE_VENV)/bin

.PHONY: ansible-venv ansible-lint staging-deploy production-deploy \
        local-converge-up local-converge-images local-converge local-converge-e2e local-converge-down

ansible-venv:
	uv venv --python 3.13 $(ANSIBLE_VENV)
	uv pip install --python $(ANSIBLE_VENV)/bin/python -r infra/ansible/requirements.txt
	cd infra/ansible && $(ANSIBLE_BIN)/ansible-playbook playbooks/controller-tools.yml

ansible-lint:
	cd infra/ansible && $(ANSIBLE_BIN)/yamllint -s . && $(ANSIBLE_BIN)/ansible-lint

staging-deploy production-deploy: %-deploy:
	@test -f infra/ansible/inventories/$*/hosts.json || { echo "no inventory: run make $*-apply first" >&2; exit 1; }
	@test -n "$(SA_IMAGE_TAG)" || { echo "set SA_IMAGE_TAG to the release to deploy" >&2; exit 1; }
	@test -f "$(SA_RELEASE_MANIFEST)" || { echo "set SA_RELEASE_MANIFEST=<release-images.json of the release>: site.yml and deploy.yml both verify the image signatures first (this stage, playbooks/verify-release.yml)" >&2; exit 1; }
	cd infra/ansible && $(ANSIBLE_BIN)/ansible-playbook -i inventories/$*/ \
		playbooks/$(if $(ROLLING),deploy.yml,site.yml) -e sa_image_tag=$(SA_IMAGE_TAG) \
		-e sa_release_manifest=$(abspath $(SA_RELEASE_MANIFEST))

# the local converge (docs/deploy/local-converge.md): 3 edge and 1 core
# debian 13 systemd containers on this machine
local-converge-up:
	cd infra/ansible && $(ANSIBLE_BIN)/ansible-playbook playbooks/controller-tools.yml
	ANSIBLE_VENV=$(ANSIBLE_VENV) infra/local/up.sh
	$(MAKE) internal-ca ENV=local

# the internal ca of the private network: issues what is
# missing or due into infra/secrets/<ENV>/ca.enc.yaml and tls.enc.yaml on
# this machine only. CA_ROTATE=new or retire rotates the ca
# (docs/runbooks/secret-rotation.md, "internal ca")
.PHONY: internal-ca
internal-ca:
	@test -n "$(ENV)" || { echo "set ENV to local, staging or production" >&2; exit 1; }
	@test -f infra/ansible/inventories/$(ENV)/hosts.json || { echo "no inventory for $(ENV)" >&2; exit 1; }
	cd infra/ansible && $(ANSIBLE_BIN)/ansible-playbook -i inventories/$(ENV)/ playbooks/internal-ca.yml \
		$(if $(CA_ROTATE),-e ca_rotate=$(CA_ROTATE))

local-converge-images:
	infra/local/build-images.sh

# ANSIBLE_EXTRA passes extra ansible-playbook arguments, such as the test
# webhook receiver of the observability play (docs/deploy/local-converge.md)
local-converge:
	cd infra/ansible && $(ANSIBLE_BIN)/ansible-playbook -i inventories/local/ playbooks/site.yml $(ANSIBLE_EXTRA)

local-converge-e2e:
	SA_LC_INVENTORY=$(CURDIR)/infra/ansible/inventories/local/hosts.json \
		$(GO) test -tags localconverge -count=1 -v ./tests/localconverge/

local-converge-down:
	infra/local/down.sh

# the umami patch path (the design, docs/runbooks/security-patch.md): pins
# umami VERSION by the digest the registry serves now, then runs the dev stack
# migration, umami-compat, the isolation gate and the end-to-end suites,
# timed, stopping at the first failure. needs everything those targets need.
.PHONY: umami-upgrade
umami-upgrade:
	VERSION=$(VERSION) scripts/umami-upgrade.sh

# deploy-time image verification (the design, tests/supplychain): the
# verification tasks of deploy.yml against a real registry and real cosign
# signatures made with a TEST-ONLY ephemeral key. needs docker, cosign 3.1.4
# and the ansible venv.
.PHONY: supplychain-it
supplychain-it:
	$(GO) test -tags supplychain -count=1 -v ./tests/supplychain/

# data migration between two environments (the design,
# docs/handover/rebuild.md section 5). MIGRATE_ENV is an env file naming the
# server and its admin login (scripts/migrate.sh), MIGRATE_DIR the dump
# directory. METHOD=pgbackrest first restores the source's backup set
# RESTORE_SET onto the restore node of inventory ENV (restore.yml, with
# RESTORE_EXTRA for restore_source), then exports from that standalone
# server; METHOD=dump exports straight from a stopped source, the path to a
# different provider. migrate-import replaces both databases of a stopped,
# bootstrapped target, checks the schema and its row-level security, and
# rebuilds the nats registries from the cp tables (jobs run reconcile); the
# env file then also needs what platformctl db migrate and jobs read.
.PHONY: migrate-export migrate-import verify-migration
METHOD ?= pgbackrest
migrate-export:
	@test -n "$(MIGRATE_ENV)" -a -n "$(MIGRATE_DIR)" || { echo "usage: make migrate-export METHOD=pgbackrest|dump MIGRATE_ENV=<env file> MIGRATE_DIR=<new dir> [ENV=<inventory> RESTORE_SET=<label>]" >&2; exit 2; }
	@case "$(METHOD)" in \
	pgbackrest) test -n "$(ENV)" -a -n "$(RESTORE_SET)" || { echo "METHOD=pgbackrest needs ENV (the new environment's inventory) and RESTORE_SET (the source backup label)" >&2; exit 2; } ;; \
	dump) ;; \
	*) echo "METHOD must be pgbackrest or dump" >&2; exit 2 ;; \
	esac
	$(if $(filter pgbackrest,$(METHOD)),cd infra/ansible && $(ANSIBLE_BIN)/ansible-playbook -i inventories/$(ENV)/ playbooks/restore.yml -e restore_set=$(RESTORE_SET) $(RESTORE_EXTRA))
	scripts/migrate.sh export $(MIGRATE_ENV) $(MIGRATE_DIR)

migrate-import: build
	@test -n "$(MIGRATE_ENV)" -a -n "$(MIGRATE_DIR)" || { echo "usage: make migrate-import MIGRATE_ENV=<env file> MIGRATE_DIR=<dir from migrate-export>" >&2; exit 2; }
	scripts/migrate.sh import $(MIGRATE_ENV) $(MIGRATE_DIR)
	@set -a && . $(abspath $(MIGRATE_ENV)) && set +a && $(BIN_DIR)/platformctl db migrate && $(BIN_DIR)/jobs run reconcile

# SOURCE_ENV and TARGET_ENV: env files as migrate-export reads, plus
# UMAMI_URL and UMAMI_ADMIN_PASSWORD of each side
verify-migration: build
	@test -n "$(SOURCE_ENV)" -a -n "$(TARGET_ENV)" || { echo "usage: make verify-migration SOURCE_ENV=<env file> TARGET_ENV=<env file>" >&2; exit 2; }
	$(BIN_DIR)/platformctl verify-migration --source $(SOURCE_ENV) --target $(TARGET_ENV)

# the design local rehearsal (tests/handover, build tag handover):
# environment a populated through the real pipeline, migrated into a
# separate environment b by the pgbackrest and the pg_dump paths, checked
# with verify-migration, signed in to through the oauth fixture, and the old
# snippet delivering into b after the names move (transfer path) in three
# engines. needs docker, node 26 with pnpm and the playwright browsers.
.PHONY: handover-it
handover-it:
	DBIP_MONTH=$(DBIP_MONTH) $(GO) test -tags handover -count=1 -timeout 90m -v ./tests/handover/

# rotate-all.yml on the minimal local converge (tests/handover/rotate-local.sh):
# converge, traffic, new secrets for a new age identity, rotate-all with the
# previous-credential checks, the end-to-end check again, then teardown
.PHONY: rotate-local
rotate-local:
	ANSIBLE_VENV=$(ANSIBLE_VENV) tests/handover/rotate-local.sh

# the operations checks of the review notes on the minimal
# local converge with the s3 stand-in and the probe node
# (tests/localconverge/ops-local.sh): bucket with object lock, stanza and wal
# archiving in object storage mode, patroni failsafe, etcd rejoin of a
# rebuilt edge node, the probe and the watchdog. then teardown (KEEP=1 keeps it)
.PHONY: ops-local
ops-local:
	ANSIBLE_VENV=$(ANSIBLE_VENV) tests/localconverge/ops-local.sh

# repository checks that need only a pinned binary (ops review, O-10).
# scripts/supply-chain/install-tools.sh installs each at its pin with its
# checksum; the ci jobs of the same names run exactly these targets.
GITLEAKS   ?= gitleaks
PROMTOOL   ?= promtool
ACTIONLINT ?= actionlint
RULES_DIR  := infra/ansible/roles/observability/files/rules

.PHONY: gitleaks promtool actionlint ci-local
# the whole history, as the required ci check scans it; reviewed false
# positives are listed with their reason in .gitleaksignore
gitleaks:
	$(GITLEAKS) git --redact --no-banner --exit-code 1 .

promtool:
	cd $(RULES_DIR) && $(PROMTOOL) check rules *.yml && $(PROMTOOL) test rules tests/*.yml

# actionlint runs shellcheck on every run: block when shellcheck is on PATH
actionlint:
	$(ACTIONLINT) .github/workflows/*.yml

# the jobs of .github/workflows/ci.yml on this machine, with the same make
# targets (scripts/ci-local.sh), for as long as no workflow can run on
# github (ops review). CI_JOBS picks jobs, default all; jobs that start
# container stacks refuse to start while the load average is at or above
# CI_LOCAL_MAX_LOAD (default 20), since the host is shared.
ci-local:
	scripts/ci-local.sh $(CI_JOBS)
