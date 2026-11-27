.DEFAULT_GOAL := help

# Settings come from .env (copied from .env.example on first use) and keys/cursor.env.
-include .env
-include keys/cursor.env
export

# Tool runners. Locally the targets run inside compose containers; CI runs them
# on the host with: make ci-inner GO= NPM= PSQL="psql -h 127.0.0.1"
GO   ?= docker compose run --rm -T go-tools
NPM  ?= docker compose run --rm -T web-tools
PSQL ?= docker compose exec -T postgres psql

TB_DB_NAME  ?= tenant_backoffice
TB_JWT_ACTIVE_KID ?= k2026a
GOTESTFLAGS ?=

.PHONY: help env up down logs db-bootstrap keys migrate seed gen gen-check lint lint-go lint-web typecheck \
	test-unit test-int test-iso test-api test-web migrate-roundtrip ec-check ec-report check-hosts build \
	ci ci-inner ci-go ci-web e2e e2e-inner

help:
	@grep -E '^[a-z-]+:' Makefile | cut -d: -f1 | sort

.env:
	cp .env.example .env
	@echo "created .env from .env.example"

env: .env

up: .env
	docker compose up -d --build --wait postgres api web

down:
	docker compose --profile tools --profile e2e down

logs:
	docker compose logs -f --tail=100

# Creates roles and the application database. Run as the Postgres superuser; idempotent.
db-bootstrap: .env
	$(PSQL) -U postgres -v ON_ERROR_STOP=1 \
		-v owner_password='$(TB_OWNER_PASSWORD)' -v app_password='$(TB_APP_PASSWORD)' -v db_name='$(TB_DB_NAME)' \
		-f - < backend/db/bootstrap/roles.sql

# Ed25519 JWT signing key and the pagination cursor HMAC key (local dev only; both git-ignored).
keys:
	mkdir -p keys
	test -f keys/$(TB_JWT_ACTIVE_KID).pem || openssl genpkey -algorithm ed25519 -out keys/$(TB_JWT_ACTIVE_KID).pem
	chmod 0644 keys/$(TB_JWT_ACTIVE_KID).pem
	test -f keys/cursor.env || echo "TB_CURSOR_KEY=$$(openssl rand -base64 32)" > keys/cursor.env

# Targets below stay stubs (fail loudly) until the milestone that owns them.
seed test-api lint-web typecheck build:
	@echo "$@: not implemented yet"; exit 1

# Generates Go DTOs and the strict server interface from api/openapi.yaml (oapi-codegen, version pinned
# by the `tool` directive in backend/go.mod).
GEN_DIR := internal/adapter/httpapi/gen

gen:
	cd backend && $(GO) go generate ./$(GEN_DIR)/...

# T-CI-03: regenerates into a scratch file and fails when the committed api.gen.go differs.
gen-check:
	cd backend && trap 'rm -f gen.check.tmp' EXIT; \
		$(GO) go tool oapi-codegen -config $(GEN_DIR)/config.yaml -o gen.check.tmp ../api/openapi.yaml && \
		{ diff -u $(GEN_DIR)/api.gen.go gen.check.tmp || { echo "gen-check: api.gen.go is stale, run make gen"; exit 1; }; } && \
		echo "gen-check: ok"

# Applies migrations to the application database as tb_owner (TB_DB_MIGRATE_URL).
migrate:
	cd backend && $(GO) go run ./cmd/migrate up

# up -> down to 0 -> up on a scratch database created through TB_TEST_DB_ADMIN_URL (T-CI-01).
migrate-roundtrip:
	cd backend && $(GO) go run ./cmd/migrate roundtrip

test-int:
	cd backend && $(GO) go test -race -tags integration $(GOTESTFLAGS) ./...

# Tenant isolation suite (T-ISO): real Postgres, connected as tb_app.
test-iso:
	cd backend && $(GO) go test -race -count=1 -tags integration $(GOTESTFLAGS) ./internal/isolation/...

# Nothing to run yet; succeed until the web client and Playwright suite exist.
test-web e2e e2e-inner:
	@echo "$@: not implemented yet"

lint: lint-go lint-web

lint-go:
	cd backend && $(GO) golangci-lint run ./...

test-unit:
	cd backend && $(GO) go test -race $(GOTESTFLAGS) ./...

# Edge-case coverage (T-CI-05, spec 11). Soft mode: report only, never fails on gaps.
# Strict mode (no -soft) becomes part of ci-go in M6.3.
ec-check:
	cd backend && $(GO) go run ./tools/eccheck -soft

ec-report:
	cd backend && $(GO) go run ./tools/eccheck -soft -markdown

# Hosts must come from the environment, never hard-coded into code or scripts (T-CI-04).
# Service hostnames in connection strings, plus localhost/127.0.0.1 literals in tests and web sources.
CHECK_HOSTS_PATHS := $(wildcard web/e2e web/src)
check-hosts:
	@if grep -rnE '@(postgres|redis)[:/]' backend --include='*.go' --include='*.sh' --include='*.yaml'; then \
		echo "check-hosts: hard-coded service host found"; exit 1; fi
	@if grep -rnE 'localhost:[0-9]+|127\.0\.0\.1' backend --include='*_test.go'; then \
		echo "check-hosts: hard-coded address in a Go test"; exit 1; fi
	@if [ -n "$(CHECK_HOSTS_PATHS)" ] && grep -rnE 'localhost:[0-9]+|127\.0\.0\.1' $(CHECK_HOSTS_PATHS); then \
		echo "check-hosts: hard-coded address in web sources"; exit 1; fi
	@echo "check-hosts: ok"

# Local CI: Go targets run inside go-tools (on the host, containers are not nested);
# web targets run on the host and go through $(NPM) into web-tools.
ci: .env
	docker compose run --rm -T go-tools make -C /workspace ci-go GO= NPM= PSQL=
	$(MAKE) ci-web

# Runs in the current environment (used on the Actions runner, with GO= NPM= PSQL=).
ci-inner: ci-go ci-web

# Goal list per spec 08 section 5: gen-check lint-go check-hosts test-unit migrate-roundtrip test-int test-iso test-api ec-check.
# test-api (M4.x) is still a stub; ec-check (M1.7, soft) stays out of ci-go until M6.3 makes it strict; they are added as they land.
ci-go: gen-check lint-go check-hosts test-unit migrate-roundtrip test-int test-iso

# Goal list per spec: npm ci typecheck lint-web test-web build. Only test-web exists until the web client (M5.x).
ci-web: test-web
