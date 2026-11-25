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
	test-unit test-int test-iso test-api test-web migrate-roundtrip ec-check check-hosts build \
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
migrate seed gen gen-check test-int test-iso test-api migrate-roundtrip ec-check lint-web typecheck build:
	@echo "$@: not implemented yet"; exit 1

# Nothing to run yet; succeed until the web client and Playwright suite exist.
test-web e2e e2e-inner:
	@echo "$@: not implemented yet"

lint: lint-go lint-web

lint-go:
	cd backend && $(GO) golangci-lint run ./...

test-unit:
	cd backend && $(GO) go test -race $(GOTESTFLAGS) ./...

# Hosts must come from the environment, never hard-coded into code or scripts.
check-hosts:
	@if grep -rnE '@(postgres|redis)[:/]' backend --include='*.go' --include='*.sh' --include='*.yaml'; then \
		echo "check-hosts: hard-coded service host found"; exit 1; fi

ci: .env
	docker compose run --rm -T go-tools make ci-go
	docker compose run --rm -T web-tools make ci-web

ci-inner: ci-go ci-web

ci-go: gen-check lint-go check-hosts test-unit migrate-roundtrip test-int test-iso test-api ec-check

ci-web: typecheck lint-web test-web build
