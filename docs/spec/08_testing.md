# 08. Тест-план

## 1. Уровни и наборы

| Набор | Префикс | Где | Инструмент | Зависимости | Make-цель |
|---|---|---|---|---|---|
| unit | T-U | `backend/internal/domain/**`, `backend/internal/usecase/**` (`*_test.go` без build-тега) | `go test -race` + testify | нет; фейки портов | `make test-unit` |
| architecture | T-ARCH | `backend/archtest/`, `.golangci.yml`, `web/eslint.config.js` | go/parser, golangci-lint, eslint | нет | `make lint`, `make test-unit` |
| integration | T-I | `backend/internal/adapter/postgres/**`, `backend/internal/app/**` (`//go:build integration`) | `go test -race -tags integration` | Postgres из окружения | `make test-int` |
| isolation | T-ISO | `backend/internal/isolation/` (`//go:build integration`) | то же + HTTP-клиент `httptest` | Postgres | `make test-iso` |
| api | T-A | `backend/internal/adapter/httpapi/apitest/` (`//go:build integration`) | `httptest.Server` с собранным `app.BuildAPI` | Postgres | `make test-api` |
| web | T-W | `web/src/**/*.test.ts(x)` | Vitest + Testing Library + MSW (jsdom) | нет | `make test-web` |
| e2e | T-E | `web/e2e/*.spec.ts` | Playwright (chromium) | весь стек compose + seed | `make e2e` |
| infra | T-CI | `backend/tools/eccheck/`, `backend/cmd/migrate/` тесты, скрипты `Makefile` | go test, shell | Postgres (T-CI-01, -02, -06) | входят в `make ci` |

Маркировка (для `eccheck`, `11_coverage.md`):
- Go: строка-комментарий `// Test-ID: T-I-04` непосредственно над `func TestXxx` (или над `t.Run` подтеста). Один тест может иметь несколько строк `Test-ID`.
- Vitest/Playwright: название теста начинается с `[T-W-03]` / `[T-E-02]`.

## 2. Правила тестов

- Не мокать то, чем не владеем: Postgres всегда настоящий в T-I/T-ISO/T-A. Порты в unit — in-memory фейки с поведением (не mock-проверки вызовов), кроме проверки «хэшер вызван» (EC-107).
- Время — фейковый `port.Clock` (`testkit.FixedClock`), никогда `time.Sleep` для бизнес-логики. ID — `testkit.SeqIDGen` в unit, UUIDv7 в интеграции.
- Тестовая БД (Р-12): `internal/testkit/db.go`:
  1. читает `TB_TEST_DB_ADMIN_URL` (нет -> `t.Fatal("TB_TEST_DB_ADMIN_URL is required")`, не Skip, EC-129);
  2. создаёт роли `tb_owner`/`tb_app` (если нет) со случайными паролями на прогон (`ALTER ROLE ... PASSWORD`), выполняя `db/bootstrap/roles.sql`;
  3. под advisory-блокировкой создаёт шаблон `tb_tpl_<sha256 миграций[:12]>` и применяет миграции goose под `tb_owner`;
  4. `TestMain` пакета создаёт `tb_t_<pkg>_<rand>` `TEMPLATE` шаблона, отдаёт пулы `Owner` и `App`, в конце `DROP DATABASE ... WITH (FORCE)` (EC-134).
- Изоляция между тестами внутри пакета: каждый тест создаёт своё поддерево с уникальными именами (фабрика `testkit.Tree`), не полагается на пустую БД. Конкурентные тесты используют барьер (`sync.WaitGroup` + канал старта) и два отдельных соединения.
- Конкурентные тесты (T-I-04, -05, -06, -15, -19, -23, -24, -28, -29, -42) повторяют сценарий 20 раз внутри теста и проверяют инвариант после каждого повтора (`tb_tree_violations()` пуст, INV-L1). Критерий стабильности в §6.
- Каждый тест изоляции делает снимок затронутых таблиц под `tb_owner` до и после запроса и сравнивает (`testkit.Snapshot`).
- Фабрика сценария изоляции `isolation.World`: корень, ветки A и B (`partner`), в каждой `company` и 2 `unit`, операции и аудит в обеих; пользователи `ownerRoot`, `adminA`, `analystA`, `viewerA`, `adminB`, `multi` (`admin` в A1, `viewer` в B1).

## 3. Список тестов

Формат: ID — расположение и имя — что проверяет — EC.

### 3.1 Unit (T-U)

| ID | Расположение | Проверяет | EC |
|---|---|---|---|
| T-U-01 | `domain/node/type_test.go TestAllowedParents` | таблица всех пар (родитель, ребёнок) 4×4 | EC-01, EC-02 |
| T-U-02 | `usecase/node/create_test.go TestCreatePlatformRejected` | создать `platform` нельзя | EC-03 |
| T-U-03 | `domain/node/move_test.go TestPlanMoveCycle` | в себя и в потомка -> `move_cycle`, порядок ошибок | EC-04, EC-05 |
| T-U-04 | `domain/node/move_test.go TestPlanMoveWrongType` | | EC-06 |
| T-U-05 | `usecase/node/move_test.go TestMoveToSameParentIsNoop` | нет записи и аудита | EC-07 |
| T-U-06 | `domain/node/move_test.go TestRootImmutable` + `usecase/node/delete_test.go TestDeleteRoot` | | EC-08 |
| T-U-07 | `domain/node/move_test.go TestMoveUnderInactive` | | EC-13 |
| T-U-08 | `domain/node/delete_test.go TestCanDelete` (табличный) | дети, баланс, история | EC-14, EC-15, EC-16 |
| T-U-09 | `usecase/balance/credit_test.go TestInactiveAncestorBlocks` + `domain/node TestEffectiveActive` | | EC-18, EC-20 |
| T-U-10 | `domain/node/status_test.go TestDeactivateTwice` | `changed=false` | EC-19 |
| T-U-11 | `domain/node/name_test.go TestNameWhitespace` | | EC-21 |
| T-U-12 | `domain/node/name_test.go TestNameUnicode` | NFC, Cf, руны, невалидный UTF-8 | EC-22 |
| T-U-13 | `usecase/node/rename_test.go TestRenameSameNameNoop` | | EC-24 |
| T-U-14 | `domain/role/effective_test.go TestMaxRankWins` | | EC-29 |
| T-U-15 | `domain/role/effective_test.go TestNoUpwardNoSideways` | | EC-30, EC-31 |
| T-U-16 | `domain/role/grant_test.go TestCanGrantCanRevoke` (табличный 4×4) | | EC-33 |
| T-U-17 | `usecase/member/remove_test.go TestLastOwner` | корень -> ошибка; некорневой -> ok | EC-35, EC-37 |
| T-U-18 | `usecase/member/add_test.go TestInactiveUser` | | EC-41 |
| T-U-19 | `domain/role/matrix_test.go TestPermissionMatrix` | 4×17, ожидание записано литералом таблицы | EC-44 |
| T-U-20 | `usecase/node/move_test.go TestMoveRequiresOldParentPermission` + delete-аналог | | EC-45 |
| T-U-21 | `adapter/httpapi/idempotency_test.go TestRequestHashIncludesRoute` | (unit адаптера, без БД) | EC-69 |
| T-U-22 | `domain/balance/apply_test.go TestInsufficientFunds` | | EC-74 |
| T-U-23 | `domain/money/amount_test.go TestPositive` | | EC-75 |
| T-U-24 | `domain/money/amount_test.go TestOverflow` + `usecase/balance TestLimitExceeded` | | EC-77 |
| T-U-25 | `usecase/balance/transfer_test.go TestSameNode` | | EC-78 |
| T-U-26 | `usecase/balance/credit_test.go TestOperationsDisabledOrInactive` | | EC-81 |
| T-U-27 | `usecase/report/validate_test.go TestInvalidPeriod` | | EC-90 |
| T-U-28 | `adapter/httpapi/cursor_test.go TestCursorTampering` | подпись, версия, фильтры | EC-93 |
| T-U-29 | `usecase/*/noop_audit_test.go TestNoopWritesNoAudit` (rename, deactivate, member, setting) | фейк AuditWriter пуст | EC-101 |
| T-U-30 | `adapter/authn/jwt_test.go TestExpired` | | EC-102 |
| T-U-31 | `adapter/authn/jwt_test.go TestForged` | none, HS256-confusion, payload, iss/aud | EC-103 |
| T-U-32 | `adapter/authn/jwt_test.go TestKeyRotation` | | EC-104 |
| T-U-33 | `usecase/auth/login_test.go TestUniformFailure` | | EC-107 |
| T-U-34 | `domain/user/email_test.go TestParseEmail` | | EC-110 |
| T-U-35 | `domain/user/password_test.go TestPasswordLength` | | EC-111 |
| T-U-36 | `adapter/authn/jwt_test.go TestClockSkew` | | EC-112 |
| T-U-37 | `domain/settings/validate_test.go TestInvalidValues` | | EC-139 |
| T-U-38 | `domain/settings/resolve_test.go TestConstraintsAcrossChain` | AND, min | EC-140, EC-141 |
| T-U-39 | `usecase/settings/unset_test.go TestUnsetFallsBack` | | EC-142 |
| T-U-40 | `usecase/authz/authz_test.go TestRequireNotFoundVersusForbidden` | нет роли (скрытый и несуществующий узел) -> одинаковый `not_found`; роль без права -> `forbidden`; вся матрица роль × право через `Require` | EC-45, EC-54 |

Unit-тесты адаптеров без БД (`adapter/authn`, `adapter/httpapi` курсор и хэш) входят в `make test-unit`.

### 3.2 Integration (T-I)

Все в `backend/internal/adapter/postgres/` (если не указано иное), `//go:build integration`.

| ID | Имя | Проверяет | EC |
|---|---|---|---|
| T-I-01 | `node_repo_test.go TestSinglePlatformIndex` | | EC-03 |
| T-I-02 | `move_test.go TestMoveCycleRejectedOnDB` (use case + реальные репозитории) | | EC-04 |
| T-I-03 | `move_test.go TestMoveRewritesSubtreePaths` | | EC-09 |
| T-I-04 | `move_concurrency_test.go TestParallelMovesSameNode` | | EC-10 |
| T-I-05 | `move_concurrency_test.go TestMoveNodeAndAncestor` | | EC-11 |
| T-I-06 | `move_concurrency_test.go TestCreateDuringParentMove` | | EC-12 |
| T-I-07 | `node_repo_test.go TestDeleteCascades` | | EC-17 |
| T-I-08 | `balance_test.go TestDeactivatedSubtreeBlocksOperations` | | EC-18 |
| T-I-09 | `node_repo_test.go TestNameKeyUnique` | | EC-23 |
| T-I-10 | `move_test.go TestMoveNameConflict` | | EC-25 |
| T-I-11 | `node_repo_test.go TestChildrenLimit` (1000 детей вставляются пачкой под `tb_owner`) | | EC-26 |
| T-I-12 | `schema_test.go TestTreeConstraints`, `TestTreeConstraintsViaRepo` | CHECK и триггер; те же нарушения через `NodeRepo` (ответ `not_found` + лог `error`); перенос не нарушает дерево | EC-27 |
| T-I-13 | `authz_test.go TestMultiBranchUser` | | EC-32 |
| T-I-14 | `member_test.go TestLastRootOwner` | | EC-35 |
| T-I-15 | `member_concurrency_test.go TestOwnersRemoveEachOther` | | EC-36 |
| T-I-16 | `authz_test.go TestRevokeDuringTransaction` | | EC-39 |
| T-I-17 | `member_test.go TestDuplicateAssignmentNoop` | | EC-40 |
| T-I-18 | `filters_test.go TestFiltersAreParameterized` | | EC-64 |
| T-I-19 | `idempotency_test.go TestConcurrentSameKey` | | EC-67 |
| T-I-20 | `idempotency_test.go TestKeyScopedPerUser` | | EC-68 |
| T-I-21 | `idempotency_test.go TestFailedAttemptNotStored` | | EC-71 |
| T-I-22 | `idempotency_test.go TestExpiredKeyReplaced` | | EC-72 |
| T-I-23 | `balance_concurrency_test.go TestParallelDebitsLowBalance` | | EC-73 |
| T-I-24 | `balance_concurrency_test.go TestOppositeTransfersNoDeadlock` | | EC-79 |
| T-I-25 | `balance_test.go TestTransferAtomic` | | EC-80 |
| T-I-26 | `balance_concurrency_test.go TestLedgerInvariant` | | EC-82 |
| T-I-27 | `connection_loss_test.go TestTerminateBackendMidTransaction` (тестовый хук `TxRunner` `BeforeCommit` вызывает `pg_terminate_backend(pid)` через соединение суперпользователя из `TB_TEST_DB_ADMIN_URL`) | | EC-83 |
| T-I-28 | `balance_concurrency_test.go TestTransferDuringMove` | | EC-84 |
| T-I-29 | `report_test.go TestReportSnapshotDuringMove` | | EC-85 |
| T-I-30 | `report_test.go TestInactiveChildInReport` | | EC-86 |
| T-I-31 | `report_test.go TestEmptyPeriodsZeroFilled` | | EC-87 |
| T-I-32 | `report_tz_test.go TestDayBoundaryInTimezone` | | EC-88 |
| T-I-33 | `report_tz_test.go TestDSTDays` | | EC-89 |
| T-I-34 | `report_test.go TestSumBeyondInt64` | | EC-91 |
| T-I-35 | `pagination_test.go TestKeysetStableUnderInserts` | | EC-92 |
| T-I-36 | `report_test.go TestAttributionFollowsMove` | | EC-95 |
| T-I-37 | `audit_test.go TestAuditRolledBackWithChange` | | EC-96 |
| T-I-38 | `audit_test.go TestAuditImmutable` | | EC-97 |
| T-I-39 | `audit_test.go TestMoveWritesThreeEntries` | | EC-98 |
| T-I-40 | `audit_test.go TestNoSecretsInAudit` | | EC-100 |
| T-I-41 | `refresh_test.go TestRefreshReuseRevokesFamily` | | EC-106 |
| T-I-42 | `member_concurrency_test.go TestSameEmailRace` | | EC-109 |
| T-I-43 | `internal/app/startup_test.go TestStartupWaitsForDBAndReadyz` (неверный порт из окружения + таймаут 2 с) | | EC-135 |
| T-I-44 | `internal/app/seed_test.go TestSeedIdempotent` | | EC-136 |
| T-I-45 | `schema_test.go TestNegativeBalanceRejected` | | EC-138 |
| T-I-46 | `settings_test.go TestSettingsChainAndHiddenSource` | | EC-140, EC-143 |

Сценарные тесты без EC (обязательны, но в `eccheck` не участвуют): T-I-50 `TestRepositoriesRoundTrip` (CRUD каждого репозитория и маппинг row↔entity), дополняется каждой задачей, добавляющей репозиторий или порт БД; на M2.3 сюда же относится `TestTreeLockSerialises` (вторая транзакция ждёт `TreeLock`, пока первая не завершится).

### 3.3 Изоляция тенантов (T-ISO) — главный критерий

`backend/internal/isolation/`, фабрика `World` (§2). T-ISO-01…10, 16…19 идут через HTTP (`httptest` + реальный `app.BuildAPI`), T-ISO-11…15 и 20…22 — на уровне репозиториев и пула.

| ID | Имя | Проверяет | EC |
|---|---|---|---|
| T-ISO-01 | `nodes_test.go TestGetForeignNodeIs404` (тело сравнивается с 404 случайного UUID) | | EC-46 |
| T-ISO-02 | `nodes_test.go TestListsExcludeForeignNodes` | | EC-47 |
| T-ISO-03 | `nodes_test.go TestMutateForeignNode` (табличный по 7 эндпоинтам) | | EC-48 |
| T-ISO-04 | `move_test.go TestMoveAcrossBranches` | | EC-49 |
| T-ISO-05 | `balance_test.go TestForeignBalanceOps` | | EC-50 |
| T-ISO-06 | `reports_test.go TestForeignReports` | | EC-51 |
| T-ISO-07 | `audit_test.go TestForeignAudit` | | EC-52 |
| T-ISO-08 | `operations_test.go TestForeignOperations` | | EC-53 |
| T-ISO-09 | `enumeration_test.go TestIDEnumeration` (100 id: 50 реальных B, 50 случайных) | | EC-54 |
| T-ISO-10 | `members_test.go TestForeignMembers` | | EC-34, EC-55 |
| T-ISO-11 | `rls_test.go TestRLSWithoutUseCaseChecks`, `adapters_test.go TestRLSThroughRepositories` | сырой SQL и настоящие репозитории (`NodeRepo`, `Checker`) под актором A против B; 42501/23514 -> `not_found` + лог `error` | EC-56 |
| T-ISO-12 | `rls_test.go TestNoActorSeesNothing` | | EC-57 |
| T-ISO-13 | `adapters_test.go TestPoolDoesNotLeakActor` (`pgxpool` MaxConns=1, настоящий `TxRunner`) | | EC-58 |
| T-ISO-14 | `adapters_test.go TestErrorAndPanicResetActor` | ошибка, panic, отмена контекста, ошибка оператора | EC-59 |
| T-ISO-15 | `rls_test.go TestRolesAndPoliciesConfigured` (запросы к `pg_roles`, `pg_class`, `pg_policies`) | | EC-60 |
| T-ISO-16 | `members_test.go TestMultiBranchUserSeenFromA` | | EC-61 |
| T-ISO-17 | `pagination_test.go TestCursorBoundToScope` | | EC-62 |
| T-ISO-18 | `move_test.go TestCrossBranchMoveByRootOwner` | | EC-63 |
| T-ISO-19 | `nodes_test.go TestDescendantRoleDoesNotSeeAncestor` | | EC-30 |
| T-ISO-20 | `rls_writes_test.go TestNodeWritesCannotEscapeBranch`: прямой `UPDATE nodes SET path` в чужую ветку и на путь предка выше корня доступа (42501), смена `parent_id` на узел чужой ветки (23514 или 42501), `DELETE` узлов чужой ветки (0 строк); данные не меняются. Примечание: `WITH CHECK` политики `nodes_update` совпадает с `USING` политики SELECT, а PostgreSQL применяет SELECT-политику и к новой строке UPDATE, поэтому ослабление одного `WITH CHECK` не наблюдаемо (эквивалентная мутация); тест фиксирует поведение в целом | | EC-144 |
| T-ISO-21 | `rls_writes_test.go TestUserPasswordWritePath`: прямой `UPDATE users SET password_hash` под `tb_app` даёт 42501; `UPDATE` разрешён только на `status`, `token_version`; `tb_set_own_password` меняет хеш только актора и не трогает других (в том числе видимых), без актора ничего не меняет; `SELECT *` по `users` падает; `INSERT` пользователя с начальным хешем работает | | EC-145 |
| T-ISO-22 | `rls_writes_test.go TestAuthByIDHasNoPasswordHash`: у `tb_auth_user_by_id` нет колонки `password_hash` (`id, status, token_version`), у `tb_auth_user_by_email` она есть | | EC-146 |

### 3.4 API (T-A)

`backend/internal/adapter/httpapi/apitest/`, общий хелпер `apitest.Client` (логин, Bearer, Idempotency-Key, разбор problem+json).

| ID | Имя | EC |
|---|---|---|
| T-A-01 | `nodes_test.go TestCreateInvalidParentType` | EC-01 |
| T-A-02 | `nodes_test.go TestMoveInvalidType` | EC-06 |
| T-A-03 | `nodes_test.go TestDeleteWithChildren` | EC-14 |
| T-A-04 | `nodes_test.go TestEmptyChildren` | EC-28 |
| T-A-05 | `me_test.go TestAccessRootsMultiBranch` | EC-32 |
| T-A-06 | `authz_test.go TestDowngradeAppliesNextRequest` | EC-38 |
| T-A-07 | `members_test.go TestSelfRevoke` | EC-42 |
| T-A-08 | `authz_test.go TestViewerCannotWrite` | EC-43 |
| T-A-09 | `filters_test.go TestInjectionInQueryParams` | EC-64 |
| T-A-10 | `idempotency_test.go TestReplaySameBody` | EC-65 |
| T-A-11 | `idempotency_test.go TestReuseDifferentBody` | EC-66 |
| T-A-12 | `idempotency_test.go TestKeyMissingOrInvalid` | EC-70 |
| T-A-13 | `balance_test.go TestAmountFormat` | EC-76 |
| T-A-14 | `pagination_test.go TestLimitValidation` | EC-94 |
| T-A-15 | `audit_test.go TestFailuresNotAuditedButLogged` (slog-хендлер теста собирает записи) | EC-99 |
| T-A-16 | `auth_test.go TestStaleTokenVersion` | EC-105 |
| T-A-17 | `auth_test.go TestLoginRateLimit` | EC-108 |
| T-A-18 | `limits_test.go TestBodyTooLarge` | EC-113 |
| T-A-19 | `limits_test.go TestUnknownFields` | EC-114 |
| T-A-20 | `limits_test.go TestInvalidUUID` | EC-115 |
| T-A-21 | `limits_test.go TestWriteRateLimit` | EC-116 |
| T-A-22 | `settings_test.go TestSettingsValidation` | EC-139 |

Сценарные без EC: T-A-30 `TestHappyPathFullScenario` (логин -> создать дерево -> роли -> начисление/перевод -> отчёт -> аудит).

### 3.5 Web (T-W)

См. `06_frontend.md` §7: T-W-01 … T-W-09 (EC-117 … EC-125).

### 3.6 E2E (T-E)

`web/e2e/`, демо-данные из `backend/seed/demo.json`, логины читаются из него же в `e2e/fixtures.ts`.

| ID | Сценарий | EC |
|---|---|---|
| T-E-01 | логин admin -> дерево -> карточка узла; токена нет в storage | EC-124 |
| T-E-02 | перенос `company` через диалог, дерево обновлено | EC-122 |
| T-E-03 | начисление и списание; двойной клик даёт одну операцию в истории | EC-120 |
| T-E-04 | пользователь ветки A открывает URL узла ветки B -> «Not found or no access» | EC-118 |
| T-E-05 | два контекста браузера: owner понижает admin до viewer; во втором кнопки исчезают после refetch, попытка мутации -> сообщение 403 | EC-38 |
| T-E-06 | фильтры истории и отчёт; назад в браузере восстанавливает фильтры | EC-123 |

### 3.7 Архитектура (T-ARCH)

| ID | Где | Проверяет | EC |
|---|---|---|---|
| T-ARCH-01 | `archtest/layers_test.go TestLayerImports` | разбор импортов `go/parser` всех пакетов: `domain` импортирует только пакеты из белого списка `domainAllowed` (любой другой импорт, включая прочую stdlib, это нарушение с именем пакета и файла): `errors`, `fmt`, `strings`, `strconv`, `unicode`, `unicode/utf8`, `time`, `sort`, `slices`, `maps`, `math`, `cmp`, `github.com/google/uuid`, `golang.org/x/text/cases`, `golang.org/x/text/unicode/norm`, подпакеты `internal/domain`; расширение: добавить пакет в `domainAllowed` и в правило `domain` в `.golangci.yml` с обоснованием; `usecase` не импортирует `adapter`, `app`, `pgx`, `net/http`; `adapter/X` не импортирует `adapter/Y` (кроме `httpapi -> httpapi/gen`); только `app` и `cmd` импортируют `adapter`; `cmd` импортирует только `app`, `config`. Самотест на фикстурах `archtest/testdata` | EC-130 |
| T-ARCH-02 | `.golangci.yml` `forbidigo` (`os.Getenv`, `os.LookupEnv`, `fmt.Print*`, `log.Print*`) с исключением `internal/config`, `cmd` (только `os.Exit`); `depguard` (`allow`-список `domain`, зеркало `domainAllowed`) дублирует часть T-ARCH-01 | EC-131 |
| T-ARCH-03 | `archtest/tags_test.go TestNoTagsInDomainAndUseCase` | структуры в `domain/**`, `usecase/**` без тегов полей | EC-132 |
| T-ARCH-04 | `make lint-web`: eslint (`no-explicit-any`) + `grep -rn "eslint-disable" web/src` пусто + `tsc --noEmit` | EC-137 |
| T-ARCH-05 | `archtest/float_test.go TestNoFloatInDomainAndUseCase` | нет `float32/float64` в `domain`, `usecase` | EC-130 |

Набор линтеров golangci-lint v2 (проверить имена командой `golangci-lint linters` в M1.4): `errcheck, govet, staticcheck, unused, ineffassign, revive, gosec, bodyclose, sqlclosecheck, rowserrcheck, errorlint, noctx, contextcheck, exhaustive, forbidigo, depguard, gocritic, misspell, nolintlint, usestdlibvars`; форматтеры `gofumpt, goimports`. `//nolint` только с указанием линтера и причины (`nolintlint` `require-explanation`, `require-specific`).

### 3.7a Каркас (T-CFG, T-HEALTH; добавлено при M1.1)

Эти тесты появились при реализации каркаса и не привязаны к EC (общие свойства загрузки конфигурации и проб живости). Входят в `make test-unit`.

| ID | Что проверяет |
|---|---|
| T-CFG-01 | `config.Load` без обязательной переменной возвращает ошибку с именем переменной |
| T-CFG-02 | значения по умолчанию (`TB_LOG_LEVEL`, `TB_LOG_FORMAT`) |
| T-CFG-03 | недопустимый уровень или формат журнала отвергаются |
| T-HEALTH-01 | `GET /healthz` отвечает 200 `{"status":"ok"}` |
| T-HEALTH-02 | `GET /readyz`: без проверок готовности 503, с падающей проверкой 503, с успешной 200 |

### 3.8 Инфраструктура (T-CI)

| ID | Где | Проверяет | EC |
|---|---|---|---|
| T-CI-01 | `cmd/migrate/roundtrip_test.go` (`integration`) + `make migrate-roundtrip` | up -> down-to 0 -> up на новой БД, после down нет объектов (запрос к `pg_class` по схеме `public`, кроме `goose_db_version`), seed после повторного up | EC-126 |
| T-CI-02 | `cmd/migrate/failing_test.go` с каталогом `testdata/failing/` | версия не изменилась, таблица из первого оператора не создана | EC-127 |
| T-CI-03 | `make gen-check` + `internal/adapter/httpapi/openapi_test.go TestOpenAPIValid` (kin-openapi) | | EC-128 |
| T-CI-04 | `make check-hosts`: `grep -rnE "localhost:[0-9]+|127\.0\.0\.1" backend --include=*_test.go web/e2e web/src` пусто + `testkit` `TestRequiresAdminURL` | EC-129 |
| T-CI-05 | `tools/eccheck/main_test.go` на фикстурах (EC без T, T без теста, нормальный случай) | EC-133 |
| T-CI-06 | `internal/testkit/db_test.go TestParallelDatabases` | две БД из шаблона параллельно, данные не пересекаются, удалены после теста | EC-134 |

## 4. Соответствие EC -> T

Полная матрица — `11_coverage.md` (генерируется `eccheck -markdown`). Источник соответствия — колонка «Тест» в `07_edge_cases.md`.

## 5. Как запускать и что входит в `make ci`

Принцип (урок прошлых проектов): хосты, порты и URL берутся только из окружения. Локально всё идёт в контейнерах compose (`go-tools`, `web-tools`), Postgres — сервис `postgres`; на раннере GitHub Actions процессы идут на хосте, Postgres — service container на `127.0.0.1:5432`. Одна и та же цель `ci-inner` работает в обоих случаях, меняются только переменные окружения.

| Цель | Что делает |
|---|---|
| `make ci` | локально: `docker compose run --rm go-tools make ci-go` и `docker compose run --rm web-tools make ci-web` (Postgres поднимается зависимостью) |
| `make ci-inner` | `ci-go` + `ci-web` в текущем окружении (используется в Actions) |
| `make ci-go` | `gen-check`, `lint-go`, `check-hosts`, `test-unit` (с T-ARCH), `migrate-roundtrip`, `test-int`, `test-iso`, `test-api`, `ec-check` |
| `make ci-web` | `npm ci`, `typecheck`, `lint-web`, `test-web`, `build` |
| `make e2e` | локально: `docker compose up -d --build postgres api web` + `make seed` + `docker compose --profile e2e run --rm playwright npx playwright test` (`E2E_BASE_URL=http://web`) |
| `make e2e-inner` | на раннере: стек compose уже поднят, `npx playwright test` на хосте (`E2E_BASE_URL=http://127.0.0.1:8088`) |

`make ci` не включает e2e (нужен весь стек и браузеры); e2e — отдельная цель и отдельное задание CI, оба обязательны для зелёного статуса.

GitHub Actions (`.github/workflows/ci.yml`):

- Задание `ci`: `timeout-minutes: 20`; service `postgres:18-alpine` с healthcheck `pg_isready`, порт `5432:5432`; env `TB_TEST_DB_ADMIN_URL=postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable`; шаги: checkout, setup-go (`go-version-file: backend/go.mod`, кэш), setup-node 24 (кэш npm), golangci-lint-action (v2.15.0), `make ci-inner`.
- Задание `e2e`: `needs: ci`, `timeout-minutes: 25`; `make keys`, `docker compose up -d --build --wait postgres api web` (web опубликован на `127.0.0.1:8088`), `make seed` (через compose), `npx playwright install --with-deps chromium`, `make e2e-inner`; при падении — загрузка `playwright-report` как артефакта.
- Конкурентность: `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`. `permissions: contents: read`.

## 6. Критерии приёмки проекта

1. `make ci` зелёный локально (в контейнерах) и задание `ci` зелёное в GitHub Actions; `make e2e` и задание `e2e` зелёные.
2. `eccheck` строгий режим: 0 EC без тестов, 0 ссылок на несуществующие T (`11_coverage.md`).
3. Набор `make test-iso` зелёный; каждый T-ISO проверяет и ответ, и неизменность данных.
4. Конкурентные тесты стабильны: `for i in $(seq 10); do make test-int GOTESTFLAGS='-run Concurrency|Parallel|Race -count=1' || exit 1; done` без падений (каждый тест внутри уже повторяет сценарий 20 раз).
5. golangci-lint 0 замечаний без исключений по путям, кроме сгенерированного `gen/`; `//nolint` только с причиной.
6. `tsc --noEmit` и eslint без ошибок, в `web/src` нет `any` и `eslint-disable`.
7. README: «pet project, verified locally and in CI, not production», схема слоёв, раздел «What is not here», без цифр производительности.
