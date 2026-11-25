# 00. Обзор: цель, границы, стек, структура, решения

## 1. Цель

`tenant-backoffice` — публичный учебный пример back office с иерархией тенантов и ролей. Дерево узлов организации фиксированных типов, роли пользователей назначаются на узел и действуют на всё его поддерево, данные и действия актора ограничены его поддеревьями. На узлах ведутся балансы (начисление, списание, перевод) с историей и идемпотентностью, есть отчёты по поддереву, настройки узла с наследованием и неизменяемый аудит.

Проект демонстрирует инженерные навыки: чистая архитектура на Go, PostgreSQL (ltree, row-level security, конкурентность), REST с OpenAPI, React 19 + TypeScript strict, тесты всех уровней с отдельным набором «изоляция тенантов», воспроизводимый CI.

Честность. Это пет-проект: проверен локально и в CI, не production, не опыт эксплуатации. В README и коде нет цифр производительности, нагрузки и «эксплуатации». Домен нейтральный (узлы организации и балансы в минимальных единицах), без отраслевой лексики. В файлах репозитория нет упоминаний компаний, вакансий, людей. Лицензия MIT.

Главный критерий приёмки проекта: набор тестов изоляции (`08_testing.md` §3.3, T-ISO-*) зелёный, и каждый EC из `07_edge_cases.md` имеет автоматический тест (`11_coverage.md`).

## 2. Границы: что НЕ делаем

| Не делаем | Почему |
|---|---|
| Конфигурируемые типы узлов и произвольную глубину | Фиксированный enum из 4 типов (см. §6, Р-01) |
| Мультивалютность, курсы, двойную запись с внешними счетами | Одна валюта на инсталляцию, баланс-агрегат + журнал операций (Р-05) |
| Внешние провайдеры входа (OAuth, SSO), MFA, восстановление пароля по email | Учебная схема аутентификации (Р-08); пароль задаёт админ при создании пользователя, пользователь меняет сам |
| Приглашения по email, отправку писем | Не нужно для демонстрации прав |
| Горизонтальное масштабирование API, распределённый rate limit | Один экземпляр API; rate limit в памяти процесса (честно описано) |
| Drag-and-drop дерева | Перенос через диалог выбора нового родителя (тестируемо, доступно с клавиатуры) |
| Экспорт отчётов (CSV/PDF), графики | Таблицы достаточно |
| i18n фронта | Интерфейс на английском |
| Деплой, Kubernetes, мониторинг, трейсинг | Только Docker Compose локально и GitHub Actions |
| Мягкое удаление пользователей, GDPR-процедуры | Вне рамки; есть деактивация пользователя |

## 3. Стек и версии

Версии Go-модулей и npm-пакетов проверены 2026-11-25 командами `go list -m -f '{{.Version}} {{.Time}}' <mod>@latest` и `npm view <pkg> version`. Теги Docker-образов проверить в M1.2 командой `docker manifest inspect <image:tag>` (на момент написания спеки Docker daemon был недоступен); если тега нет, взять ближайший патч той же минорной версии и записать в `docs/reports/M1.2.md`.

### 3.1 Бэкенд

| Компонент | Версия | Обоснование |
|---|---|---|
| Go | 1.27.3 (toolchain), `go 1.27.0` в `go.mod` | Последняя стабильная 1.27.x (проверено `go version`) |
| `github.com/jackc/pgx/v5` | v5.13.0 | Нативный драйвер Postgres, `pgxpool`, `pgtype`; `database/sql` не нужен |
| `github.com/pressly/goose/v3` | v3.30.1 | SQL-миграции с `-- +goose Up/Down`, транзакция на миграцию, библиотечный API для теста up/down |
| `github.com/oapi-codegen/oapi-codegen/v2` | v2.9.0 (как `tool` в `go.mod`) | Генерация strict-server интерфейса и DTO из OpenAPI |
| `github.com/oapi-codegen/runtime` | v1.9.1 | Рантайм сгенерированного кода |
| `github.com/getkin/kin-openapi` | v0.153.0 | Валидация `api/openapi.yaml` в тесте T-CI-03 |
| `github.com/golang-jwt/jwt/v5` | v5.3.1 | JWT EdDSA, проверка `alg`, `kid` |
| `golang.org/x/crypto` | v0.59.0 | `argon2` (argon2id) |
| `golang.org/x/time` | v0.16.0 | `rate` для rate limit |
| `github.com/google/uuid` | v1.6.0 | UUIDv7 (`uuid.NewV7`) |
| `github.com/caarlos0/env/v11` | v11.4.2 | Разбор окружения только в `internal/config` |
| `github.com/stretchr/testify` | v1.12.1 | `require`/`assert` в тестах |
| `golangci-lint` | v2.15.0 (бинарник, не `go tool`) | Конфиг формата v2; ставится официальным скриптом или `golangci/golangci-lint-action` |
| HTTP-роутер | стандартный `net/http.ServeMux` (шаблоны методов и путей Go 1.22+) | Сгенерированный strict-server умеет `std-http`; лишний роутер не нужен (chi отброшен) |

Testcontainers-go (v0.45.0) рассмотрен и отброшен: локально тесты идут внутри контейнера `go-tools`, которому пришлось бы давать Docker-сокет; вместо этого Postgres приходит снаружи (сервис compose локально, service container в Actions), адрес из окружения (Р-12).

### 3.2 База данных

| Компонент | Версия | Обоснование |
|---|---|---|
| PostgreSQL | образ `postgres:18-alpine` (точный патч зафиксировать в M1.2) | Major 18 зрелый; ltree входит в contrib официального образа. 19 не берём: только что вышел, выигрыша для проекта нет |
| Расширения | `ltree`, `pgcrypto` не нужен (UUID генерирует приложение) | Только ltree |

### 3.3 Фронтенд

| Пакет | Версия | Примечание |
|---|---|---|
| Node.js | 24 LTS (локально проверено v24.21.0); образ `node:24-bookworm-slim` | |
| `react`, `react-dom` | 19.3.1 | |
| `typescript` | 6.0.3 | Не 7.0.2: `typescript-eslint` 8.74.0 требует `typescript >=4.8.4 <6.1.0` (проверено `npm view typescript-eslint peerDependencies`) |
| `vite` | 8.3.8 | |
| `@vitejs/plugin-react` | 6.3.0 | peer `vite ^8` |
| `@tanstack/react-query` | 5.108.4 | |
| `@tanstack/react-router` | 1.170.46 | |
| `@tanstack/router-plugin` | 1.169.0 | file-based routes; peer `@tanstack/react-router ^1.170.46` |
| `react-hook-form` | 7.92.0 | |
| `zod` | 4.6.7 | |
| `@hookform/resolvers` | 7.0.9 | peer `zod ^3.25 || ^4` |
| `openapi-typescript` | 7.13.0 | Типы из `api/openapi.yaml` |
| `openapi-fetch` | 0.17.0 | Типизированный клиент по этим типам |
| `tailwindcss`, `@tailwindcss/vite` | 4.3.5 | Минимальный UI без дизайн-системы (Р-15) |
| `vitest` | 5.1.1 | + `jsdom` 31.0.1, `@testing-library/react` 16.3.3, `@testing-library/user-event` 14.6.13 |
| `msw` | 3.0.7 | Моки HTTP в тестах компонентов |
| `@playwright/test` | 1.65.1 | Образ `mcr.microsoft.com/playwright:v1.65.1-noble` (проверить в M1.2) |
| `eslint` | 10.15.0 | + `typescript-eslint` 8.74.0, `eslint-plugin-react-hooks` 7.1.1 |
| `@types/node` | 24.19.2 | |

Перед фиксацией в M5.1 исполнитель повторяет `npm view <pkg> version` и `npm view <pkg> peerDependencies`; при конфликте пиров берёт последнюю совместимую версию и записывает в отчёт.

### 3.4 Инфраструктура

| Компонент | Значение |
|---|---|
| Образы сборки и тестов Go | `golang:1.27.3-bookworm` (тесты с `-race`, нужен gcc), `golang:1.27.3-alpine3.24` (сборка api) |
| Рантайм api | `gcr.io/distroless/static-debian12:nonroot` |
| Фронт в compose | сборка `node:24-bookworm-slim`, раздача `nginx:1.29-alpine` (проксирует `/api` на api, один origin) |
| CI | GitHub Actions, `ubuntu-latest`, `actions/checkout@v4`, `actions/setup-go@v5`, `actions/setup-node@v4`, `golangci/golangci-lint-action@v8` (мажоры проверить в M1.5) |

## 4. Структура репозитория

```
tenant-backoffice/
  api/openapi.yaml                 # источник правды API (Р-10)
  backend/
    go.mod                         # module: см. 10_open_questions Q1
    cmd/api/main.go                # тонкий: config.Load -> app.BuildAPI -> Run
    cmd/migrate/main.go            # goose up/down/status/check-roundtrip
    cmd/seed/main.go               # демо-дерево из seed/demo.json
    internal/
      config/                      # единственное место чтения окружения
      domain/
        node/ role/ money/ balance/ settings/ audit/ user/ apperr/
      usecase/
        port/                      # общие порты: TxRunner, Clock, IDGen, AuditWriter
        auth/ node/ member/ settings/ balance/ report/ audit/
      adapter/
        postgres/                  # pgxpool, TxRunner с актором, репозитории, rows
        httpapi/                   # handlers, dto (json-теги), middleware, errors
          gen/                     # сгенерировано oapi-codegen, не редактировать
        authn/                     # JWT EdDSA, argon2id, refresh-токены
        clock/ idgen/ ratelimit/
      app/                         # composition root: BuildAPI, BuildMigrate, BuildSeed
    migrations/                    # goose SQL, 0001_... .sql
    db/bootstrap/roles.sql         # роли БД (выполняет суперпользователь)
    seed/demo.json                 # демо-дерево, пользователи и тестовые пароли
    archtest/                      # тест слоёв и тегов (go/parser)
    tools/eccheck/                 # проверка покрытия EC -> T
    internal/testkit/              # хелперы тестов: БД из шаблона, фабрики, акторы
  web/
    package.json vite.config.ts tsconfig.json eslint.config.js
    src/
      api/schema.d.ts              # сгенерировано openapi-typescript
      api/client.ts                # openapi-fetch + refresh-логика
      routes/                      # TanStack Router file routes
      features/{auth,tree,node,members,balance,operations,reports,audit}/
      components/ui/               # Button, Input, Dialog(<dialog>), Table, Field
      lib/money.ts lib/problem.ts
    e2e/                           # Playwright
  docker/
    api.Dockerfile web.Dockerfile go-tools.Dockerfile nginx.conf
  compose.yaml                     # postgres, api, web, go-tools, web-tools, playwright (profile)
  Makefile
  .github/workflows/ci.yml
  .golangci.yml
  docs/spec/  docs/reports/
  README.md  LICENSE
```

## 5. Архитектура бэкенда и правила кода (обязательны для всех этапов)

Слои и направление зависимостей (только внутрь):

```
cmd -> app -> adapter -> usecase -> domain
                    \-> usecase/port
config <- app, cmd
```

- `domain`: только стандартная библиотека. Сущности и value objects без тегов, с конструкторами, которые проверяют инварианты. Нет `context`, нет SQL, нет времени «сейчас» (время передаётся аргументом).
- `usecase`: импортирует `domain` и `usecase/port`. Интерфейсы портов объявляет потребитель (пакет use case или `usecase/port` для общих).
- `adapter`: реализует порты, импортирует `usecase` и `domain`. Никогда не импортирует другой адаптер, кроме `adapter/httpapi/gen` внутри `httpapi`.
- `app`: composition root, единственное место, где создаются адаптеры и use cases. Построители по точке входа: `BuildAPI(cfg) (*API, error)`, `BuildMigrate(cfg)`, `BuildSeed(cfg)`.
- `cmd/*/main.go`: тонкий (до ~30 строк): `config.Load()`, построитель из `app`, запуск, код выхода.

Конвенции (дословно, проверяются ревью и T-ARCH-*):

1. Каждый use case: свой пакет-уровень тип `XxxUseCase` с методом `Execute(ctx context.Context, in XxxInput) (XxxOutput, error)`. `XxxInput` и `XxxOutput` — простые структуры без json/db-тегов. `func (in XxxInput) Validate() error` — только stateless-правила (формат, диапазоны, обязательность); правила, требующие БД, внутри `Execute`.
2. Актор — поле `Actor domain/user.ActorID` во Input тех use cases, которым он нужен (не берётся из `ctx` внутри use case). Request ID для аудита — поле `RequestID string` во Input.
3. Сущности и VO без тегов. Транспортные DTO с json-тегами только в `adapter/httpapi` (сгенерированные в `gen` и ручные в `dto`). Строки БД с db-тегами только в `adapter/postgres` (`nodeRow`, `operationRow`). Маппинг явный на каждой границе: `dto -> Input`, `Output -> dto`, `row -> entity`, `entity -> args`.
4. У каждого адаптера свой `Config` (простая структура) и `New(cfg Config, deps...) (*X, error)`. Окружение читает только `internal/config` (`config.Load() (Config, error)`), который собирает конфиги адаптеров.
5. DI-контейнер или сервис-локатор в use case не передаются; зависимости — поля структуры use case, задаются в конструкторе `NewXxxUseCase(deps...)`.
6. Логи только `log/slog` (JSON-хендлер в compose, text в тестах), логгер передаётся зависимостью. В логах нет паролей, токенов, хэшей, тел запросов операций целиком.
7. Ошибки: доменные ошибки `apperr.Error{Code, Message, Fields}` с кодами из `03_api.md` §2.3; адаптеры оборачивают инфраструктурные ошибки `fmt.Errorf("...: %w", err)`; маппинг кода в HTTP-статус только в `httpapi`.
8. Деньги: `money.Amount` (int64 минимальных единиц) с проверкой переполнения; float запрещён в `domain` и `usecase` (T-ARCH-05).
9. Контекст: каждый метод порта принимает `ctx` первым аргументом; `context.Background()` только в `cmd` и тестах.

## 6. Принятые решения (сводка)

| ID | Решение | Обоснование | Отброшено |
|---|---|---|---|
| Р-01 | Типы узлов фиксированы enum-ом: `platform -> partner -> company -> unit`, глубина ровно 4, тип однозначно задаёт уровень | Права, отчёты и проверки переносов становятся простыми и проверяемыми на уровне CHECK в БД; конфигурируемая мета-модель типов не добавляет демонстрационной ценности | Конфигурируемые типы (нужна таблица типов и правил связей, сложнее инварианты) |
| Р-02 | Иерархия в БД: `ltree` (материализованный путь из меток-UUID) | Поддерево и принадлежность — одно сравнение `<@` по GiST; перенос — один `UPDATE` поддерева; см. сравнение в `02` §1 | Closure table (перенос = удаление и вставка N×M строк), текстовый materialized path (LIKE с экранированием, нет типовой проверки) |
| Р-03 | Структурные изменения дерева (создание, перенос, удаление узла) сериализуются транзакционной advisory-блокировкой дерева | Пересчёт путей зависит от путей предков; глобальная сериализация структурных операций проста и корректна, их частота мала | Блокировки строк по цепочке предков (сложно, легко ошибиться) |
| Р-04 | Роли упорядочены по рангу `viewer < analyst < admin < owner`, одна роль на пару (пользователь, узел); эффективная роль на узле = максимум по назначениям на узле и его предках | Наследование вниз без исключений, понятная матрица | Запрещающие правила, «сужение» роли на потомке |
| Р-05 | Ledger: таблица `operations` (append-only) + `balances` как агрегат, обновляемый в той же транзакции; перевод = две связанные записи | Минимально и проверяемо инвариантом Σ; двойная запись нужна при внешних счетах, которых нет | Полная двойная запись; баланс как вычисляемая сумма (медленно, нет блокировки для проверки остатка) |
| Р-06 | Баланс не уходит в минус: проверка в домене + `CHECK (amount >= 0)` в БД | Две линии защиты, простая политика | Овердрафт с лимитом |
| Р-07 | Деньги: BIGINT минимальных единиц, в API строка цифр | int64 точен; JSON number теряет точность в JS выше 2^53; суммы отчётов (numeric) могут превысить int64 | float, decimal-строки с дробной частью |
| Р-08 | Аутентификация: учебная схема — access JWT EdDSA 10 мин с `kid` и ротацией ключей, opaque refresh-токен в httpOnly cookie с ротацией и обнаружением повторного использования, пароли argon2id | Честная, без внешних провайдеров | Сессии в БД без JWT (скрыло бы тему ротации ключей), HS256 (общий секрет) |
| Р-09 | Права читаются из БД на каждом запросе; токен несёт только `sub` и `tv` (token_version) | Понижение роли действует со следующего запроса без инвалидации токенов | Роли в claims (устаревают до 10 минут) |
| Р-10 | OpenAPI 3.0.3 `api/openapi.yaml` — источник правды; Go-интерфейс сервера и DTO генерирует oapi-codegen, типы фронта — openapi-typescript; CI проверяет, что сгенерированное актуально | Один контракт для двух сторон | Генерация спецификации из Go-кода (контракт зависит от реализации) |
| Р-11 | Объект вне поддерева актора -> 404, в поддереве без права -> 403 | Не раскрывать существование объектов других веток; см. `02` §6 | Всегда 403 |
| Р-12 | Тестовая БД приходит из окружения (`TB_TEST_DB_ADMIN_URL`), каждый тестовый пакет создаёт свою БД из шаблона с применёнными миграциями | Одинаково работает в контейнере локально и на раннере; параллельные пакеты изолированы | testcontainers (Docker-сокет в контейнере), одна общая БД (взаимные помехи) |
| Р-13 | Актор передаётся в сессию Postgres через `set_config('app.actor_id', $1, true)` в начале каждой транзакции; RLS-политики читают его; без актора политики ничего не пропускают | `is_local=true` сбрасывается в конце транзакции, пул безопасен; fail closed; см. `02` §5 | `SET ROLE` на пользователя БД (роли кластера на каждого пользователя), отдельные соединения на актора |
| Р-14 | Идемпотентность в той же транзакции, что и операция: строка ключа ссылается на результат; повтор восстанавливает ответ из операции | Нет состояния `in_progress`, конкурентный дубль ждёт на уникальном индексе | Отдельное хранилище ответов (сериализованный HTTP-ответ, двухфазная запись) |
| Р-15 | UI: Tailwind CSS 4 + несколько своих компонентов, нативный `<dialog>` | Честно скромно, мало зависимостей | shadcn/ui (Radix-зависимости ради 6 компонентов), MUI |
| Р-16 | Отчёты относят операции к узлам по текущему дереву и строятся в одной транзакции `REPEATABLE READ READ ONLY` | Согласованный снимок при параллельных переносах; правило атрибуции простое и явное | Снимок пути в каждой операции (история «как было»), отложено |

## 7. Окружение и запуск (контракт для M1)

Переменные окружения (читает только `internal/config`, префикс `TB_`):

| Переменная | Назначение | Пример в compose |
|---|---|---|
| `TB_HTTP_ADDR` | адрес API | `:8080` |
| `TB_DB_URL` | URL роли приложения `tb_app` | `postgres://tb_app:...@postgres:5432/tenant_backoffice?sslmode=disable` |
| `TB_DB_MIGRATE_URL` | URL роли-владельца `tb_owner` (миграции, seed) | |
| `TB_DB_CONNECT_TIMEOUT` | ожидание готовности БД при старте | `30s` |
| `TB_JWT_KEYS_DIR` | каталог с Ed25519-ключами `<kid>.pem`; активный ключ = `TB_JWT_ACTIVE_KID` | `/run/keys` |
| `TB_JWT_ACTIVE_KID` | kid для подписи | `k2026a` |
| `TB_ACCESS_TTL` / `TB_REFRESH_TTL` | 10m / 168h | |
| `TB_CURSOR_KEY` | ключ HMAC для курсоров пагинации (≥32 байт, base64) | генерирует `make keys` |
| `TB_CURRENCY` | ISO 4217 код инсталляции, только для отображения | `EUR` |
| `TB_LOG_LEVEL`, `TB_LOG_FORMAT` | `info`, `json`/`text` | |
| `TB_TEST_DB_ADMIN_URL` | только тесты: суперпользователь для создания тестовых БД | в CI `postgres://postgres:postgres@127.0.0.1:5432/postgres` |
| `E2E_BASE_URL` | только Playwright | локально `http://web`, в CI `http://127.0.0.1:8088` |

Make-цели (минимум): `up`, `down`, `logs`, `migrate`, `seed`, `gen`, `gen-check`, `lint`, `lint-go`, `lint-web`, `typecheck`, `test-unit`, `test-int`, `test-iso`, `test-api`, `test-web`, `migrate-roundtrip`, `ec-check`, `ci`, `ci-inner`, `e2e`, `e2e-inner`. Семантика `ci`/`ci-inner` — `08_testing.md` §5.
