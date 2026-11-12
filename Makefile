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
        build test test-integration vet lint fmt tidy image \
        db-migrate db-roles db-it sqlc

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

# runs the live tests against the dev stack; needs make dev-up and umami-bootstrap
test-integration:
	@set -a && . ./$(DEV_ENV) && set +a && UMAMI_IT_URL=$$UMAMI_URL $(GO) test -race -count=1 ./internal/umami/...

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
