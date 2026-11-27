# tenant-backoffice

[![CI](https://github.com/Gwakko/tenant-backoffice/actions/workflows/ci.yml/badge.svg)](https://github.com/Gwakko/tenant-backoffice/actions/workflows/ci.yml)

A multi-tenant back office with a tenant hierarchy, role inheritance down the tree, and tenant isolation enforced in two independent layers: the use cases and PostgreSQL row-level security (RLS).

> **Status: pet project.** It is verified locally and in CI. It is not production software: see [Known limitations](#known-limitations) and [What is not here](#what-is-not-here).

## What it does

- A fixed four-level tree of nodes: `platform -> partner -> company -> unit`.
- Users hold one role per node (`viewer < analyst < admin < owner`). The effective role on a node is the highest role assigned on that node or any of its ancestors.
- Each node has settings, a balance with an append-only operation journal (deposit, withdrawal, transfer), an audit log, and subtree reports.
- Moving a node moves its whole subtree. A user can only see and change what is inside the subtree they have access to.

## Architecture

Backend (Go) dependencies point inward only:

```
cmd -> app -> adapter -> usecase -> domain
                    \-> usecase/port
config <- app, cmd
```

- `domain`: entities and value objects with invariant-checking constructors, no I/O, no struct tags.
- `usecase`: one type per use case with `Execute(ctx, Input) (Output, error)`; the acting user is an explicit field of the input.
- `adapter`: PostgreSQL repositories, the HTTP API, authentication, clock, id generation, rate limiting.
- `app`: the composition root; the only place where adapters are created and wired.
- `config`: the only package that reads the environment.

The layering rules are enforced by tests and linters, not only by convention (see [Testing](#testing)).

Repository layout:

```
api/openapi.yaml        API contract (source of truth)
backend/
  cmd/{api,migrate,seed}
  internal/{config,domain,usecase,adapter,app,isolation,testkit}
  migrations/           goose SQL migrations
  db/bootstrap/         database roles
  seed/demo.json        demo tree, users, and local test credentials
  archtest/             architecture tests
  tools/eccheck/        edge-case coverage check
web/                    React + TypeScript client (src/, e2e/)
docker/                 Dockerfiles and nginx config
compose.yaml  Makefile  .env.example
docs/spec/              design specification (Russian)
docs/reports/           per-milestone work reports
```

Stack: Go, PostgreSQL 18 (with `ltree`), React 19, TypeScript, Vite, TanStack Router and Query, Tailwind CSS. Exact versions are in [`docs/spec/00_overview.md`](docs/spec/00_overview.md), section 3.

## Key design decisions

Identifiers D-01 to D-16 correspond to Р-01 to Р-16 in the (Russian) specification. The full list with rejected alternatives is in [`docs/spec/00_overview.md`](docs/spec/00_overview.md), section 6.

- **Fixed node types (D-01).** Four types, depth exactly four, enforced by a database `CHECK`. No configurable type model.
- **Hierarchy as `ltree` (D-02).** Each node stores a materialized path of UUID labels; "is in the subtree" is one `<@` comparison backed by a GiST index, and a move is one subtree `UPDATE`.
- **Structural changes are serialized (D-03).** Create, move, and delete of nodes take a transaction-level advisory lock on the tree.
- **Role inheritance by rank (D-04).** One role per (user, node); the effective role is the maximum over the node and its ancestors. No deny rules.
- **Journal plus aggregate (D-05, D-06).** `operations` is append-only; `balances` is updated in the same transaction. A balance never goes negative: checked in the domain and by a `CHECK` in the database.
- **Money as integers (D-07).** Minor units in `BIGINT`, strings of digits in the API (JSON numbers lose precision in JavaScript). Floats are forbidden in `domain` and `usecase` by an architecture test.
- **Training-grade authentication (D-08).** argon2id passwords, short-lived EdDSA access JWT with a `kid` and key rotation, opaque refresh token in an HttpOnly cookie with rotation and reuse detection.
- **Permissions read on every request (D-09).** The token carries only the subject and a token version, so a role change applies from the next request.
- **Contract first (D-10).** `api/openapi.yaml` generates the Go server interface and DTOs (oapi-codegen) and the frontend types (openapi-typescript). CI fails if generated code is stale.
- **404 versus 403 (D-11).** An object outside the actor's subtree is "not found"; one inside the subtree without the needed role is "forbidden". Other branches are not revealed.
- **Test databases from the environment (D-12).** Each test package creates its own database from a migrated template; no shared database, no Docker socket inside containers.
- **Actor in the database session (D-13).** Every transaction starts with `set_config('app.actor_id', ..., true)`; RLS policies read it. The setting is transaction-local, so a pooled connection cannot leak an actor, and with no actor the policies return nothing.
- **Idempotency in the same transaction (D-14).** The idempotency key row references the operation result, so a retry restores the answer from the operation. There is no "in progress" state; a concurrent duplicate waits on the unique index.
- **Own UI components (D-15).** Tailwind plus a handful of components and the native `<dialog>`.
- **Reports on one snapshot (D-16).** Operations are attributed to nodes by the current tree, and each report runs in a single `REPEATABLE READ READ ONLY` transaction.

## Running locally

Requirements: Docker with Compose, `make`, `openssl`.

```bash
make keys          # local JWT signing key and cursor key (git-ignored)
make up            # build and start postgres, api, web (creates .env from .env.example)
make db-bootstrap  # create database roles and the application database
make migrate       # apply migrations as the owner role
make seed          # load the demo tree and users
```

`make db-bootstrap` and `make seed` are idempotent. The web interface is served on the host port `TB_WEB_HOST_PORT` and the API on `TB_API_HOST_PORT`; both are set in `.env` (copied from [`.env.example`](.env.example)). Stop everything with `make down`.

Demo accounts: see [`backend/seed/demo.json`](backend/seed/demo.json). They are local test credentials for the seeded data only.

All settings are `TB_*` environment variables read in `backend/internal/config`; the local values in `.env.example` are development values, not secrets.

## Testing

```bash
make ci          # the full local pipeline (the same targets CI runs)
make e2e         # Playwright end-to-end suite against the compose stack
make ec-check    # edge-case coverage: every catalogued case must map to a test
```

Individual targets: `test-unit`, `test-int`, `test-iso`, `test-api`, `test-web`, `lint`, `gen-check`, `migrate-roundtrip`.

What is covered:

- Unit tests for the domain and use cases.
- Integration tests against a real PostgreSQL (migrations, constraints, triggers, repositories, RLS).
- Tenant isolation tests at two levels: use cases and HTTP, plus RLS checked directly in the database as the application role.
- API tests: the real API over a real database.
- Frontend tests with Vitest and Testing Library.
- End-to-end tests with Playwright.
- Architecture tests (layer imports, struct tags, no floats) with self-tests, and linters for Go and TypeScript.
- A migration round trip (up, down to zero, up) and a check that generated code is current.
- `ec-check`, which verifies that each edge case listed in the specification has a corresponding test.

## What is not here

| Not done | Why |
|---|---|
| Configurable node types and arbitrary depth | Fixed four-type enum (D-01) |
| Multiple currencies, rates, double entry with external accounts | One currency per installation; balance aggregate plus journal (D-05) |
| External identity providers (OAuth, SSO), MFA, password reset by email | Training-grade authentication (D-08); an admin sets the initial password and the user changes it |
| Email invitations and any outgoing mail | Not needed to demonstrate permissions |
| Horizontal scaling of the API, distributed rate limiting | Single API instance; the rate limiter lives in process memory |
| Drag and drop in the tree | Moving is a dialog that picks the new parent (testable, keyboard accessible) |
| Report export (CSV/PDF), chart libraries | Tables plus one small inline SVG bar chart are enough |
| Frontend i18n | The interface is in English |
| Deployment, Kubernetes, monitoring, tracing | Docker Compose locally and GitHub Actions only |
| Soft deletion of users, GDPR procedures | Out of scope; users can be deactivated |

## Known limitations

- **Rate limiting is per process.** Limits are kept in memory of one API instance and are not distributed. Brute force of one email from many addresses is not covered: limits apply per (IP, email) pair and per IP.
- **Reports use the current tree.** An operation is attributed to the node's present position, so moving a node changes where its past operations appear in subtree reports. There is no "as it was" history.
- **Training-grade authentication.** argon2id, EdDSA JWT, refresh token in an HttpOnly cookie with a CSRF header. Logging out in one browser tab is not propagated to other tabs. The refresh cookie is `Secure` by default; the local compose setup turns that off (`TB_COOKIE_SECURE=false`) so it works over plain HTTP.
- **RLS isolates by subtree, not by action.** The database guarantees an actor never sees rows outside their subtree. It does not check role permissions for an action; the use-case layer does.
- **Email existence oracle.** The SECURITY DEFINER function `tb_user_id_by_email` lets the application resolve an email across tenants, which can reveal whether an email exists. Mitigation: rate limiting.
- **Audit of deleted nodes.** Audit rows of a deleted node stay in the database but are not visible through RLS.
- **Balance is not tied to the journal in the database.** The balance is a stored aggregate; its consistency with the sum of operations is maintained by the use case and verified by tests, not by a database constraint.
- **Demo data has no audit trail.** The seed loads nodes, users, roles, settings, and operations only; audit entries appear only as you use the application.

## License

[MIT](LICENSE)
