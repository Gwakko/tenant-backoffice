# 09. Этапы и задачи для исполнителей

## Общие правила для каждой задачи (вставлять в промпт исполнителя)

- Читать только перечисленные во «Вход» файлы спецификации и указанные исходники; весь репозиторий не перечитывать.
- Соблюдать `00_overview.md` §5 (слои и Go-конвенции) и §3 (версии). Не добавлять зависимостей сверх §3 без записи в отчёт с причиной.
- Текст в репозитории (код, комментарии, README, сообщения ошибок) на английском. Не упоминать компании, вакансии, людей. Без эмодзи.
- В конце: выполнить команды «Проверка», записать отчёт `docs/reports/<ID>.md`: сделано / не сделано / отклонения от спеки с причиной / фактические версии / что проверить вручную. Не коммитить (коммитит основная сессия). Без AI-атрибуции.
- Если спецификация противоречит факту (API библиотеки другое, тег образа не существует), не выдумывать: сделать ближайшее по смыслу и описать в разделе «Отклонения».
- Тесты маркировать `// Test-ID: T-...` (Go) или `[T-...]` в названии (Vitest/Playwright), как в `08_testing.md` §1.
- Хосты и порты в тестах — только из окружения.

Проверочные команды предполагают `Makefile` из M1.2. Все `make`-цели запускаются с хоста и сами уходят в контейнеры.

## Граф зависимостей

```
M1.1 -> M1.2 -> M1.3 -> M1.6
          |       \-> M1.4 (параллельно с M1.3)
          \-> M1.5 (после M1.4)
M1.6 -> M1.7
M2.1 (после M1.3) ; M2.2 (после M1.6, параллельно с M2.1)
M2.3 (после M2.1, M2.2) -> M2.5 -> M2.6 -> M2.7
M2.4 (после M2.1; параллельно с M2.3)
M3.1 (после M2.1) ; M3.2 (после M1.6, параллельно с M3.1)
M3.3 (после M2.5, M3.1) -> M3.4 -> M3.6
M3.5 (после M3.3; параллельно с M3.4)
M4.1 (после M2.2; можно параллельно с M3.*)
M4.2 (после M4.1, M2.4) -> M4.3, M4.4 (параллельно) -> M4.6
M4.5 (после M3.4, M4.2)
M4.7 (после M3.5)
M5.1 (после M4.1) -> M5.2 -> M5.3, M5.4, M5.5 (параллельно) -> M5.6
M6.1 (после M4.*, M5.*) -> M6.2 ; M6.3 (после всех тестовых задач) ; M6.4 ; M6.5 последней
```

Параллельно можно запускать задачи, у которых нет пересечения по файлам «На выходе»; в графе они отмечены.

---

## M1. Каркас, compose, CI-каркас, миграции, домен узлов

### M1.1 Скелет Go-модуля и конфигурации
- **Цель:** модуль `backend/` компилируется, `cmd/api` поднимает HTTP с `/healthz` и `/readyz` (пока без БД `/readyz` = 503), конфиг из окружения, slog.
- **Вход:** `00_overview.md` §3.1, §4, §5, §7; `10_open_questions.md` Q1.
- **На выходе:** `backend/go.mod` (`go 1.27.0`, `tool` для oapi-codegen и goose), `backend/cmd/api/main.go`, `backend/internal/config/config.go` (+ тест), `backend/internal/app/api.go` (`BuildAPI`, `API.Run(ctx)` с graceful shutdown по SIGTERM), `backend/internal/adapter/httpapi/health.go`, `.gitignore`, `.editorconfig`.
- **Критерии приёмки:** `config.Load` возвращает ошибку с именем переменной при отсутствии обязательной; `main.go` ≤ 30 строк; `os.Getenv` только в `internal/config`.
- **Проверка:** `cd backend && go build ./... && go test ./internal/config/...`; `go run ./cmd/api` с `TB_HTTP_ADDR=:8080` -> `/healthz` 200 (проверить вручную или тестом `httptest`).
- **Не делать:** БД, роутинг API, генерацию.

### M1.2 Docker Compose, Dockerfile, Makefile, роли БД
- **Цель:** `make up` поднимает `postgres`, `api`; контейнеры `go-tools`, `web-tools` для make-целей; `make db-bootstrap` создаёт роли.
- **Вход:** `00_overview.md` §3.2, §3.4, §7; `02_hierarchy_and_authorization.md` §5.2 (роли); `08_testing.md` §5.
- **На выходе:** `compose.yaml` (сервисы `postgres` с healthcheck, `api`, `web` (заглушка nginx до M5), `go-tools`, `web-tools`, `playwright` в profile `e2e`; переменные с адресами сервисов), `docker/api.Dockerfile` (multi-stage, distroless nonroot), `docker/go-tools.Dockerfile` (golang bookworm + golangci-lint v2.15.0), `backend/db/bootstrap/roles.sql` (идемпотентный, пароли из psql-переменных), `Makefile` (цели из `00` §7, пока с заглушками там, где нет кода: заглушка печатает `not implemented yet` и завершается 0 только для `test-web`, `e2e`), `make keys` (Ed25519 через `openssl genpkey -algorithm ed25519` и `TB_CURSOR_KEY`), `.env.example`, `docs/reports/M1.2.md` с проверенными тегами образов (`docker manifest inspect`).
- **Критерии приёмки:** `make up` -> `api` healthy; `make db-bootstrap` дважды подряд без ошибок; в `compose.yaml` нет секретов, кроме локальных dev-значений в `.env.example`.
- **Проверка:** `make up && docker compose ps` (все healthy), `make db-bootstrap && make db-bootstrap`, `make down`.
- **Не делать:** CI, миграции.

### M1.3 Миграции goose и таблица узлов
- **Цель:** `cmd/migrate` (up, down, down-to, status, roundtrip), миграция `0001` (ltree, функции, `nodes`, триггер, `tb_tree_violations`), testkit БД из шаблона.
- **Вход:** `02` §2 (только `0001`), §3; `08` §2 (testkit), T-CI-01, T-CI-02, T-CI-06, T-I-12 (часть по `nodes`).
- **На выходе:** `backend/cmd/migrate/main.go`, `backend/internal/app/migrate.go`, `backend/migrations/0001_extensions_and_nodes.sql` (Up и Down), `backend/internal/testkit/db.go`, тесты T-CI-01, T-CI-02 (с `cmd/migrate/testdata/failing/`), T-CI-06, T-I-12 (CHECK/триггер на `nodes`), цель `make migrate-roundtrip`.
- **Критерии приёмки:** Down удаляет всё созданное в Up; миграции встроены через `embed.FS`; testkit падает без `TB_TEST_DB_ADMIN_URL`.
- **Проверка:** `make migrate && make migrate-roundtrip && make test-int`.
- **Не делать:** RLS, остальные таблицы.

### M1.4 Линтеры и архитектурные тесты
- **Цель:** `.golangci.yml` (v2) по `08` §3.7 (T-ARCH-02: `forbidigo`, `depguard`), `archtest` T-ARCH-01, T-ARCH-03, T-ARCH-05 с самотестами.
- **Вход:** `00` §5; `08` §3.7.
- **На выходе:** `.golangci.yml`, `backend/archtest/{layers,tags,float}_test.go`, `backend/archtest/testdata/**` (пакеты-нарушители, исключены из сборки через каталог `testdata`), цель `make lint-go`.
- **Критерии приёмки:** самотесты показывают, что каждое правило ловит нарушение; текущий код проходит.
- **Проверка:** `make lint-go && make test-unit`. Ручная: добавить `import "github.com/jackc/pgx/v5"` в файл `internal/domain/node` -> `make test-unit` падает; удалить.
- **Не делать:** правила фронта (M5.1).

### M1.5 CI-каркас
- **Цель:** `.github/workflows/ci.yml` с заданием `ci` (`08` §5), пока выполняющим доступные цели; задание `e2e` закомментировано до M6.2.
- **Вход:** `08` §5.
- **На выходе:** `.github/workflows/ci.yml`, цели `make ci-inner`, `make check-hosts` (T-CI-04, grep-часть; `TestRequiresAdminURL` в `internal/testkit`).
- **Критерии приёмки:** `timeout-minutes` задан; адреса только через env; Postgres service с healthcheck; `go-version-file`.
- **Проверка:** `docker run --rm -v "$PWD":/repo -w /repo rhysd/actionlint:latest` (тег зафиксировать в отчёте); прогон в Actions после push основной сессией.
- **Не делать:** деплой, публикацию образов.

### M1.6 Домен узлов
- **Цель:** `domain/node` (Type, Name, Node, PlanMove, CanDelete, статусы), `domain/apperr`.
- **Вход:** `01_domain.md` §1, §7; `03_api.md` §2.3 (коды); EC-01…EC-08, EC-13…EC-16, EC-19…EC-22, EC-24.
- **На выходе:** `backend/internal/domain/node/**`, `backend/internal/domain/apperr/**`, тесты T-U-01, T-U-03, T-U-04, T-U-06 (доменная часть), T-U-07, T-U-08, T-U-10, T-U-11, T-U-12, T-U-09 (`TestEffectiveActive`).
- **Критерии приёмки:** нет импортов кроме stdlib, `golang.org/x/text`, `github.com/google/uuid`; методы чистые.
- **Проверка:** `make lint-go && cd backend && go test -race ./internal/domain/...`.
- **Не делать:** репозитории, use cases.

### M1.7 Инструмент покрытия EC
- **Цель:** `tools/eccheck` в мягком режиме (отчёт без падения) по `11_coverage.md` §2.
- **Вход:** `11_coverage.md`; `08` §1 (маркировка).
- **На выходе:** `backend/tools/eccheck/{main.go,main_test.go,testdata/**}` (T-CI-05), цель `make ec-check` (мягкий режим `-soft`), `make ec-report` (`-markdown`).
- **Критерии приёмки:** строгий режим падает на фикстурах с непокрытым EC и с T без теста; мягкий печатает сводку.
- **Проверка:** `cd backend && go test ./tools/eccheck/... && make ec-check`.

## M2. Иерархия, права, изоляция (RLS), аутентификация

### M2.1 Миграции пользователей, ролей, RLS
- **Цель:** `0002_users_roles.sql`, функции `tb_actor_id`, `tb_actor_scopes`, `tb_auth_user_by_email`, `tb_auth_user_by_id`, `tb_user_id_by_email`, `tb_node_effective_active`, политики RLS и GRANT для `nodes`, `role_assignments`, `users`, `refresh_tokens`.
- **Вход:** `02` §2 (0002), §5.2, §5.3; `01` §2, §3.
- **На выходе:** `backend/migrations/0002_users_roles.sql`, `0003_rls_core.sql`; тест T-ISO-15 (частично: таблицы этой миграции), T-ISO-12.
- **Критерии приёмки:** Down полностью обратим (roundtrip зелёный); `tb_app` без RLS-обхода.
- **Проверка:** `make migrate-roundtrip && make test-iso`.

### M2.2 Домен ролей, пользователей, прав
- **Цель:** `domain/role` (Role, Permission, Allows, CanGrant, CanRevoke, Effective), `domain/user` (Email, пароль, ActorID).
- **Вход:** `01` §2, §3; EC-29…EC-31, EC-33, EC-44, EC-110, EC-111.
- **На выходе:** `backend/internal/domain/{role,user}/**`; T-U-14, T-U-15, T-U-16, T-U-19, T-U-34, T-U-35.
- **Проверка:** `cd backend && go test -race ./internal/domain/...`.

### M2.3 TxRunner с актором, авторизация, репозитории узлов
- **Цель:** `adapter/postgres` (пул, `TxRunner`, `TreeLock`, `Checker`, `NodeRepo`, rows и маппинг), `usecase/port`, `usecase/authz`.
- **Вход:** `02` §3, §4, §5.1, §5.3; `04` §0; T-ISO-11, T-ISO-13, T-ISO-14, T-I-01, T-I-09, T-I-11, T-I-12 (дополнить), T-I-13, T-I-16.
- **На выходе:** `backend/internal/usecase/port/**`, `backend/internal/usecase/authz/**`, `backend/internal/adapter/postgres/{pool,tx,treelock,authz,node_repo,rows}.go`, тестовый хук `BeforeCommit` (экспортируется только через `export_test.go`), тесты из «Вход», T-I-50 (дополняется каждой задачей, добавляющей репозиторий), `EXPLAIN` трёх запросов в отчёте.
- **Критерии приёмки:** все чтения через `InTx`; ошибка 42501 маппится в `not_found` с логом `error`.
- **Проверка:** `make test-int && make test-iso`.

### M2.4 Аутентификация
- **Цель:** `adapter/authn` (argon2id, JWT EdDSA с kid и ротацией, refresh-токены), `usecase/auth` UC-A1…A5, `RefreshTokenRepo`, `AuthUserRepo`.
- **Вход:** `02` §7; `04` §1; EC-102…EC-107, EC-112.
- **На выходе:** `backend/internal/adapter/authn/**`, `backend/internal/usecase/auth/**`, `backend/internal/adapter/postgres/{auth_user_repo,refresh_repo}.go`; T-U-30…T-U-33, T-U-36, T-I-41.
- **Критерии приёмки:** `alg` проверяется белым списком; пароли и токены не логируются; параметры argon2id в `authn.Config`.
- **Проверка:** `cd backend && go test -race ./internal/adapter/authn/... ./internal/usecase/auth/... && make test-int`.

### M2.5 Use cases узлов
- **Цель:** UC-N1…N11 (без аудита: `AuditWriter` — интерфейс с no-op реализацией до M3.3).
- **Вход:** `04` §2; `01` §1, §3.3; EC-03…EC-28 (части use case), EC-45.
- **На выходе:** `backend/internal/usecase/node/**`; T-U-02, T-U-05, T-U-06 (delete), T-U-13, T-U-20; T-I-02…T-I-07, T-I-10.
- **Проверка:** `cd backend && go test -race ./internal/usecase/node/... && make test-int`.

### M2.6 Use cases участников
- **Цель:** UC-M1…M4.
- **Вход:** `04` §3; EC-33…EC-37, EC-40…EC-42, EC-109.
- **На выходе:** `backend/internal/usecase/member/**`, `backend/internal/adapter/postgres/{member_repo,user_repo}.go`; T-U-17, T-U-18; T-I-14, T-I-15, T-I-17, T-I-42.
- **Проверка:** `cd backend && go test -race ./internal/usecase/member/... && make test-int`.

### M2.7 Изоляция: уровень репозиториев
- **Цель:** фабрика `isolation.World`, T-ISO-11…T-ISO-15 полностью, T-ISO-19 (на уровне use case, HTTP-вариант добавит M4.6).
- **Вход:** `02` §5; `07` группа C; `08` §2, §3.3.
- **На выходе:** `backend/internal/isolation/{world.go,harness_test.go,nodes_test.go,move_test.go,members_test.go,enumeration_test.go,pagination_test.go,layers_test.go,rls_test.go}`; T-ISO-01…04, 09, 10, 16…19 на уровне use case.
- **Критерии приёмки:** каждый тест сравнивает снимки данных до/после.
- **Проверка:** `make test-iso`.

## M3. Балансы, настройки, аудит

### M3.1 Миграции ledger, настроек, идемпотентности, аудита
- **Вход:** `02` §2 (0004…0007), §5.2 (политики и GRANT).
- **На выходе:** `backend/migrations/0004_settings.sql`, `0005_ledger.sql`, `0006_idempotency.sql`, `0007_audit.sql` (с RLS, GRANT, триггером, `tb_settings_chain`); дополнение T-ISO-15 всеми таблицами; T-I-38, T-I-45.
- **Проверка:** `make migrate-roundtrip && make test-int && make test-iso`.

### M3.2 Домен денег, баланса, настроек, аудита
- **Вход:** `01` §4, §5, §6; EC-74, EC-75, EC-77, EC-139…EC-141.
- **На выходе:** `backend/internal/domain/{money,balance,settings,audit}/**`; T-U-22, T-U-23, T-U-24 (доменная часть), T-U-37, T-U-38.
- **Проверка:** `cd backend && go test -race ./internal/domain/...`.

### M3.3 Аудит: writer и подключение к use cases M2
- **Цель:** `adapter/postgres/audit_repo.go` (`AuditWriter`, `AuditRepo`), запись аудита во всех use cases узлов и участников по `04` §8; UC-AU1.
- **Вход:** `01` §6; `04` §7, §8; EC-96…EC-101.
- **На выходе:** изменения в `usecase/node/**`, `usecase/member/**`, новый `usecase/audit/**`; T-U-29, T-I-37, T-I-39, T-I-40.
- **Проверка:** `cd backend && go test -race ./internal/usecase/... && make test-int`.

### M3.4 Операции баланса с идемпотентностью
- **Вход:** `03` §4; `04` §5; EC-65…EC-84.
- **На выходе:** `backend/internal/usecase/balance/**`, `backend/internal/adapter/postgres/{balance_repo,operation_repo,idempotency_repo}.go`; T-U-09 (use case часть), T-U-24 (лимит), T-U-25, T-U-26; T-I-08, T-I-19…T-I-22, T-I-25, T-I-27.
- **Критерии приёмки:** порядок блокировок по id; ключ сохраняется в той же транзакции; replay восстанавливает Output из операции.
- **Проверка:** `cd backend && go test -race ./internal/usecase/balance/... && make test-int`.

### M3.5 Настройки
- **Вход:** `01` §5; `04` §4; EC-139…EC-143.
- **На выходе:** `backend/internal/usecase/settings/**`, `backend/internal/adapter/postgres/settings_repo.go`; T-U-39, T-I-46.
- **Проверка:** `cd backend && go test -race ./internal/usecase/settings/... && make test-int`.

### M3.6 Конкурентные тесты баланса
- **Вход:** `08` §2 (повторы, барьер); EC-73, EC-79, EC-82, EC-84.
- **На выходе:** `backend/internal/adapter/postgres/balance_concurrency_test.go`: T-I-23, T-I-24, T-I-26, T-I-28.
- **Проверка:** критерий стабильности `08` §6 п. 4.
- **Примечание:** T-I-24 использует тестовую обёртку над репозиторием баланса (без production-хука): она удерживает транзакцию после первой блокировки, пока другая не возьмёт свою; мутация порядка блокировок падает 10 из 10 (взаимоблокировка, SQLSTATE 40P01). Сценарии повторяются 20 раз (EC-79 говорил о 50, принято 20 по `08` §2).

## M4. OpenAPI, HTTP, отчёты, seed

### M4.1 OpenAPI и генерация
- **Цель:** `api/openapi.yaml` по `03_api.md` целиком; `oapi-codegen` конфиг; `make gen`, `make gen-check`; T-CI-03.
- **Вход:** `03_api.md`; `05_reports.md` §6.
- **На выходе:** `api/openapi.yaml`, `backend/internal/adapter/httpapi/gen/{config.yaml,gen.go,api.gen.go}`, `backend/internal/adapter/httpapi/openapi_test.go`.
- **Критерии приёмки:** все эндпоинты и коды ошибок `03` описаны; деньги — строки с pattern; `Problem` схема.
- **Проверка:** `make gen && make gen-check && cd backend && go test ./internal/adapter/httpapi/...`.

### M4.2 HTTP-каркас и middleware
- **Цель:** сервер strict-handler, middleware: request id, recover, slog access-лог, body limit, Content-Type, authn, rate limit; ошибки problem+json; курсор (HMAC); хэш идемпотентности.
- **Вход:** `03` §1…§4; `02` §6, §7; EC-108, EC-113…EC-116, EC-93, EC-69.
- **На выходе:** `backend/internal/adapter/httpapi/{server,middleware,errors,cursor,idempotency}.go`, `backend/internal/adapter/ratelimit/**`; T-U-21, T-U-28; каркас `apitest.Client`.
- **Проверка:** `cd backend && go test -race ./internal/adapter/httpapi/... ./internal/adapter/ratelimit/...`.

### M4.3 Обработчики: auth, me, nodes, members, settings
- **Вход:** `03` §5.1…§5.4; `04` §1…§4.
- **На выходе:** `backend/internal/adapter/httpapi/handlers_{auth,me,nodes,members,settings}.go`, `dto_*.go` (маппинг dto↔Input/Output); T-A-01…T-A-08, T-A-16, T-A-17, T-A-22.
- **Проверка:** `make test-api`.

### M4.4 Обработчики: balance, operations, audit
- **Вход:** `03` §5.5, §5.7; `04` §5, §7.
- **На выходе:** `handlers_{balance,operations,audit}.go`; T-A-09…T-A-15, T-A-18…T-A-21.
- **Проверка:** `make test-api`.

### M4.5 Отчёты
- **Вход:** `05_reports.md`; `03` §5.6; EC-85…EC-95.
- **На выходе:** `backend/internal/usecase/report/**`, `backend/internal/adapter/postgres/report_repo.go`, `handlers_reports.go`; T-U-27; T-I-29…T-I-36.
- **Проверка:** `cd backend && go test -race ./internal/usecase/report/... && make test-int && make test-api`.

### M4.6 Изоляция через HTTP и сценарный API-тест
- **Вход:** `07` группа C; `08` §3.3, §3.4.
- **На выходе:** `backend/internal/isolation/*_test.go`: T-ISO-01…T-ISO-10, T-ISO-16…T-ISO-19 (HTTP); T-A-30; T-I-18 (если не сделан).
- **Критерии приёмки:** T-ISO-01 сравнивает тела 404 побайтно без `requestId`.
- **Проверка:** `make test-iso && make test-api`.

### M4.7 Seed и запуск
- **Цель:** `cmd/seed` загружает `backend/seed/demo.json` (дерево ≥ 2 partner × 2 company × 3 unit, пользователи всех ролей в разных ветках, включая «multi», операции за 60 дней для отчётов) upsert-ом по фиксированным UUID; тестовые пароли лежат только в этом файле; T-I-43, T-I-44.
- **Вход:** `00` §7; `08` §3.2 (T-I-43, -44), §3.6.
- **На выходе:** `backend/cmd/seed/main.go`, `backend/internal/app/seed.go`, `backend/seed/demo.json`, цель `make seed`, `backend/internal/app/startup_test.go`.
- **Проверка:** `make up && make migrate && make seed && make seed && make test-int`.

## M5. Фронтенд

### M5.1 Каркас web
- **Цель:** Vite + React 19 + TS strict, ESLint, Tailwind 4, TanStack Router (plugin) и Query, openapi-typescript + openapi-fetch, Vitest + MSW; `docker/web.Dockerfile`, `docker/nginx.conf` (proxy `/api` -> `api:8080`); T-ARCH-04.
- **Вход:** `00` §3.3; `06` §1, §2.
- **На выходе:** `web/{package.json,package-lock.json,vite.config.ts,tsconfig.json,eslint.config.js,index.html}`, `web/src/{main.tsx,api/*,routes/__root.tsx}`, Dockerfile и nginx, цели `make lint-web typecheck test-web` (реальные).
- **Критерии приёмки:** версии по `00` §3.3 после `npm view`; `npm ci` воспроизводим; `make gen-check` покрывает `schema.d.ts`.
- **Проверка:** `make ci-web`.

### M5.2 Сессия, клиент API, логин
- **Вход:** `06` §5, §6; `02` §7 (cookie, CSRF-заголовок); EC-119, EC-124.
- **На выходе:** `web/src/auth/**`, `web/src/api/client.ts`, `web/src/api/problem.ts`, `web/src/routes/login.tsx`, `index.tsx`; T-W-03, T-W-08.
- **Проверка:** `make test-web`.

### M5.3 Дерево и перенос
- **Вход:** `06` §3 (дерево, перенос); EC-122, EC-125.
- **На выходе:** `web/src/features/tree/**`, сайдбар в `__root.tsx`; T-W-06, T-W-09.
- **Проверка:** `make test-web`.

### M5.4 Карточка узла: обзор, настройки, участники, операции баланса
- **Вход:** `06` §3, §4, §5, §6; EC-117, EC-120, EC-121.
- **На выходе:** `web/src/routes/nodes.$nodeId*.tsx` (index, settings, members), `web/src/features/{node,members,balance}/**`, `web/src/lib/{money,idempotency}.ts`; T-W-01, T-W-04, T-W-05.
- **Проверка:** `make test-web`.

### M5.5 История операций, отчёт, аудит
- **Вход:** `06` §3; `05` §1, §6; EC-118, EC-123.
- **На выходе:** `web/src/routes/nodes.$nodeId.{operations,reports,audit}.tsx`, `web/src/features/{operations,reports,audit}/**`; T-W-02, T-W-07.
- **Проверка:** `make test-web`.

### M5.6 Проверка фронта целиком
- **Цель:** прогон `make ci-web`, исправление предупреждений, ручная проверка в браузере по сценариям T-E (список в отчёте).
- **Проверка:** `make ci-web && make up` (вручную `http://localhost:8088`).

## M6. E2E, README, лицензия, финальная проверка

### M6.1 Playwright e2e
- **Вход:** `08` §3.6, §5; `06` §7.
- **На выходе:** `web/playwright.config.ts` (`baseURL` из `E2E_BASE_URL`, без значения по умолчанию), `web/e2e/{fixtures.ts,*.spec.ts}` T-E-01…T-E-06, сервис `playwright` в compose, цели `make e2e`, `make e2e-inner`.
- **Проверка:** `make e2e`.

### M6.2 CI: задание e2e
- **Вход:** `08` §5.
- **На выходе:** задание `e2e` в `.github/workflows/ci.yml`.
- **Проверка:** actionlint; прогон в Actions после push основной сессией.

### M6.3 Строгое покрытие EC
- **Цель:** `make ec-check` в строгом режиме; `docs/spec/11_coverage.md` таблица обновлена `make ec-report`.
- **Проверка:** `make ec-check` (0 непокрытых).

### M6.4 README и лицензия
- **На выходе:** `README.md` (EN: что это, честная пометка «pet project, verified locally and in CI, not production», схема слоёв, решения Р-01…Р-16 кратко, запуск `make up migrate seed`, тестовые логины — ссылка на `backend/seed/demo.json`, раздел «What is not here» из `00` §2, ограничения: rate limit в памяти, атрибуция отчётов по текущему дереву, учебная аутентификация), `LICENSE` (MIT, держатель по `10_open_questions.md` Q2).
- **Критерии приёмки:** нет цифр производительности, компаний, имён людей; `grep -rniwE "casino|bet|bets|betting|gambling|wager|jackpot" README.md docs backend web/src` пусто.
- **Проверка:** чтение основной сессией.

### M6.5 Финальная проверка
- **Цель:** `make ci`, `make e2e`, `make ec-check` зелёные на чистом клоне; критерии `08` §6 по пунктам с отметкой в отчёте.
- **Проверка:** `git clone <repo> /tmp/tb && cd /tmp/tb && make keys && make ci && make e2e`.
