# 02. Иерархия в БД, авторизация, RLS, передача актора

## 1. Выбор схемы иерархии (Р-02)

Сравнение по трём операциям, которые важны проекту. N — размер поддерева, D — глубина (здесь ≤ 4).

| Операция | ltree (выбрано) | Closure table | Materialized path (text) |
|---|---|---|---|
| Выборка поддерева | `WHERE path <@ $p`, GiST-индекс по `path` | `JOIN closure ON descendant WHERE ancestor = $id`, индекс по `(ancestor)` | `WHERE path LIKE $p || '%'`, нужен `text_pattern_ops` и экранирование `%`/`_` |
| Перенос узла | один `UPDATE` N строк поддерева (замена префикса пути) | удалить N×(D_old) и вставить N×(D_new) строк замыкания; легко ошибиться | один `UPDATE` N строк, как ltree, но конкатенация строк без проверки формата |
| «X в поддереве Y» | `x.path <@ y.path` — одно сравнение двух значений | поиск строки `(Y, X)` по PK | `starts_with(x.path, y.path || '.')`, граница метки обрабатывается вручную |
| Целостность | тип `ltree` проверяет формат метки; `nlevel` для CHECK глубины | нужен триггер, поддерживающий замыкание | только соглашение |

Выбор ltree: все три операции выражаются встроенными операторами с индексной поддержкой, перенос проще, чем в closure table, а формат пути проверяется типом. Минус ltree (зависимость от расширения contrib) для проекта несущественен: расширение есть в официальном образе Postgres и помечено trusted, его может создать владелец БД. Closure table отброшен из-за сложности переноса, текстовый путь — из-за ручного экранирования и отсутствия проверки формата.

Метка узла в пути — его UUID в hex без дефисов (32 символа `[0-9a-f]`), функция `tb_label(uuid)`. Путь корня — одна метка. Имена в путь не входят: переименование не трогает путь.

Утверждений о производительности спека не делает. В M2.3 исполнитель записывает в отчёт `EXPLAIN` трёх запросов §3 на seed-данных, только как наблюдение.

## 2. Схема таблиц (миграции M1.3, M2.1, M3.1)

Все таблицы в схеме `public`, владелец `tb_owner`. Время `timestamptz`, хранится UTC.

```sql
-- 0001_extensions_and_nodes.sql (M1.3)
CREATE EXTENSION IF NOT EXISTS ltree;

CREATE FUNCTION tb_label(id uuid) RETURNS text
  LANGUAGE sql IMMUTABLE STRICT AS $$ SELECT replace(id::text, '-', '') $$;

CREATE FUNCTION tb_type_level(t text) RETURNS int
  LANGUAGE sql IMMUTABLE STRICT AS $$
  SELECT CASE t WHEN 'platform' THEN 1 WHEN 'partner' THEN 2
                WHEN 'company' THEN 3 WHEN 'unit' THEN 4 END $$;

CREATE TABLE nodes (
  id          uuid PRIMARY KEY,
  type        text NOT NULL CHECK (type IN ('platform','partner','company','unit')),
  parent_id   uuid NULL REFERENCES nodes(id) ON DELETE RESTRICT,
  path        ltree NOT NULL UNIQUE,
  name        text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
  name_key    text NOT NULL,
  status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  created_at  timestamptz NOT NULL,
  updated_at  timestamptz NOT NULL,
  CHECK (nlevel(path) = tb_type_level(type)),
  CHECK ((type = 'platform') = (parent_id IS NULL)),
  CHECK (subpath(path, -1)::text = tb_label(id))
);
CREATE UNIQUE INDEX nodes_single_platform ON nodes ((true)) WHERE type = 'platform';
CREATE UNIQUE INDEX nodes_parent_name_key ON nodes (parent_id, name_key);
CREATE INDEX nodes_path_gist ON nodes USING GIST (path);
CREATE INDEX nodes_parent_id ON nodes (parent_id, name_key, id);
```

Триггер `nodes_parent_type_check` (BEFORE INSERT OR UPDATE OF parent_id, path): тип родителя = допустимый родитель, `subpath(NEW.path, 0, -1) = parent.path`. Нарушение -> `RAISE EXCEPTION USING ERRCODE = 'check_violation'` (INV-T2, INV-T3; вторая линия к домену, EC-27).

Функция для тестов `tb_tree_violations() RETURNS TABLE(node_id uuid, problem text)`: узлы, у которых путь не равен пути родителя плюс своя метка, или уровень не совпадает с типом. Используется в конкурентных тестах (EC-10, EC-11, EC-12).

```sql
-- 0002_users_roles.sql (M2.1)
CREATE TABLE users (
  id             uuid PRIMARY KEY,
  email          text NOT NULL CHECK (email = lower(email)),
  password_hash  text NOT NULL,
  status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  token_version  bigint NOT NULL DEFAULT 0,
  created_at     timestamptz NOT NULL
);
CREATE UNIQUE INDEX users_email ON users (email);

CREATE TABLE role_assignments (
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  node_id     uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  role        text NOT NULL CHECK (role IN ('viewer','analyst','admin','owner')),
  granted_by  uuid NULL REFERENCES users(id),
  granted_at  timestamptz NOT NULL,
  PRIMARY KEY (user_id, node_id)
);
CREATE INDEX role_assignments_node ON role_assignments (node_id);

CREATE TABLE refresh_tokens (
  id          uuid PRIMARY KEY,
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  family_id   uuid NOT NULL,
  token_hash  bytea NOT NULL UNIQUE,           -- sha256 от токена
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz NULL,
  revoked_at  timestamptz NULL,
  created_at  timestamptz NOT NULL
);
CREATE INDEX refresh_tokens_family ON refresh_tokens (family_id);
```

```sql
-- 0003_rls_core.sql (M2.1): функции tb_actor_*, tb_auth_*, политики nodes/role_assignments/users
-- 0004_settings.sql, 0005_ledger.sql, 0006_idempotency.sql, 0007_audit.sql (M3.1), каждая со своими политиками
CREATE TABLE node_settings (
  node_id    uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  key        text NOT NULL CHECK (key IN ('display_timezone','operations_enabled','max_operation_amount')),
  value      jsonb NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by uuid NOT NULL REFERENCES users(id),
  PRIMARY KEY (node_id, key)
);

CREATE TABLE balances (
  node_id    uuid PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
  amount     bigint NOT NULL DEFAULT 0 CHECK (amount >= 0),
  version    bigint NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL
);

CREATE TABLE operations (
  id                    uuid PRIMARY KEY,
  node_id               uuid NOT NULL REFERENCES nodes(id) ON DELETE RESTRICT,
  kind                  text NOT NULL CHECK (kind IN ('credit','debit','transfer_in','transfer_out')),
  amount                bigint NOT NULL CHECK (amount > 0),
  balance_after         bigint NOT NULL CHECK (balance_after >= 0),
  transfer_id           uuid NULL,
  counterparty_node_id  uuid NULL REFERENCES nodes(id) ON DELETE RESTRICT,
  comment               text NOT NULL DEFAULT '',
  actor_id              uuid NOT NULL REFERENCES users(id),
  created_at            timestamptz NOT NULL,
  CHECK ((kind IN ('transfer_in','transfer_out')) = (transfer_id IS NOT NULL))
);
CREATE INDEX operations_node_created ON operations (node_id, created_at DESC, id DESC);
CREATE INDEX operations_created ON operations (created_at DESC, id DESC);
CREATE INDEX operations_transfer ON operations (transfer_id) WHERE transfer_id IS NOT NULL;

CREATE TABLE idempotency_keys (
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key           text NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128),
  request_hash  bytea NOT NULL,
  operation_id  uuid NULL,       -- для credit/debit
  transfer_id   uuid NULL,       -- для transfer
  created_at    timestamptz NOT NULL,
  expires_at    timestamptz NOT NULL,
  PRIMARY KEY (user_id, key),
  CHECK ((operation_id IS NULL) <> (transfer_id IS NULL))
);

CREATE TABLE audit_log (
  id          uuid PRIMARY KEY,
  occurred_at timestamptz NOT NULL,
  actor_id    uuid NOT NULL,
  node_id     uuid NOT NULL,      -- без FK: удалённый узел не должен ломать журнал
  action      text NOT NULL,
  target_type text NOT NULL,
  target_id   uuid NOT NULL,
  before      jsonb NULL,
  after       jsonb NULL,
  request_id  text NOT NULL
);
CREATE INDEX audit_node_time ON audit_log (node_id, occurred_at DESC, id DESC);
CREATE INDEX audit_time ON audit_log (occurred_at DESC, id DESC);
CREATE TRIGGER audit_log_immutable BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION tb_raise_immutable();   -- RAISE EXCEPTION 'audit_log is append-only'
```

Последствие отсутствия FK у `audit_log.node_id`: записи об удалённом узле перестают быть видимыми (RLS видимости идёт через `nodes`). Удалять можно только узлы без детей и без операций, поэтому теряется немного; запись `node.deleted` пишется на родителя и остаётся видимой.

Каждая миграция имеет `-- +goose Down`, обратную `Up` полностью (EC-126).

## 3. Запросы иерархии (репозиторий `adapter/postgres/node_repo.go`)

Все запросы параметризованы (`$1`), сортировки только из белого списка (EC-64).

```sql
-- Поддерево (для отчётов и проверок)
SELECT id, type, parent_id, path, name, status FROM nodes WHERE path <@ $1::ltree;

-- Дети с курсором (keyset по name_key, id)
SELECT ... FROM nodes
WHERE parent_id = $1 AND (name_key, id) > ($2, $3)
ORDER BY name_key, id LIMIT $4;

-- Принадлежность X поддереву Y
SELECT x.path <@ y.path FROM nodes x, nodes y WHERE x.id = $1 AND y.id = $2;

-- Эффективная активность. Предки выше корня доступа актора скрыты RLS, поэтому
-- запрос обёрнут в SECURITY DEFINER-функцию tb_node_effective_active(node_id uuid) RETURNS boolean,
-- которая сначала проверяет видимость node_id актору (EXISTS по nodes с политикой через tb_actor_scopes()),
-- иначе возвращает NULL. Внутри:
SELECT bool_and(a.status = 'active') FROM nodes a WHERE a.path @> $1::ltree;

-- Перенос (под структурной блокировкой, §4); $1 old path узла, $2 new parent path, $3 node id, $4 new parent id
UPDATE nodes
SET path = $2::ltree || subpath(path, nlevel($1::ltree) - 1),
    parent_id = CASE WHEN id = $3 THEN $4 ELSE parent_id END,
    updated_at = $5
WHERE path <@ $1::ltree;
```

`subpath(path, nlevel(old) - 1)` оставляет метку переносимого узла и всё ниже, поэтому новый путь = путь нового родителя + хвост. Количество обновлённых строк должно равняться размеру поддерева, прочитанному под блокировкой; иначе ошибка и откат (защита от рассинхронизации).

## 4. Конкурентность структурных изменений (Р-03)

Создание узла, перенос и удаление выполняются в транзакции, которая первым шагом берёт `SELECT pg_advisory_xact_lock(hashtextextended('tb.tree', 0))`. После получения блокировки use case **перечитывает** пути узла, старого и нового родителя (данные до блокировки могли устареть) и только потом проверяет правила. Переименование, деактивация, настройки и операции баланса блокировку дерева не берут: они не меняют пути (переименование блокирует строку `FOR UPDATE` и полагается на unique-индекс имени).

Следствия (тесты): два параллельных переноса одного узла выполняются последовательно, итог = последний коммит, оба пишут аудит (EC-10); перенос узла и его предка одновременно не порождает рассогласованных путей (EC-11); создание потомка параллельно с переносом родителя даёт правильный путь (EC-12). Проверка во всех трёх: `tb_tree_violations()` пуст.

Операции баланса ссылаются на `node_id`, а не на путь, поэтому перенос их не задевает (EC-84).

## 5. Авторизация: две линии защиты

### 5.1 Линия 1: use case

Пакет `usecase/authz`:

```go
type Checker interface { // порт, реализует adapter/postgres
    EffectiveRole(ctx context.Context, tx port.Tx, actor user.ActorID, nodeID node.ID) (role.Role, bool, error)
}
func Require(ctx, c Checker, tx, actor, nodeID, perm role.Permission) (role.Role, error)
// found=false -> apperr.NotFound("node"); !role.Allows -> apperr.Forbidden
```

Запрос эффективной роли:

```sql
SELECT max(CASE ra.role WHEN 'viewer' THEN 1 WHEN 'analyst' THEN 2
                         WHEN 'admin' THEN 3 WHEN 'owner' THEN 4 END)
FROM role_assignments ra
JOIN nodes a ON a.id = ra.node_id
JOIN users u ON u.id = ra.user_id AND u.status = 'active'
WHERE ra.user_id = $1
  AND a.path @> (SELECT path FROM nodes WHERE id = $2);
```

NULL (нет строк) = узел не виден -> 404. Каждый use case, принимающий id узла, вызывает `Require` для каждого id до любых других проверок (перенос: узел, старый родитель, новый родитель; перевод: оба узла). Списки (`operations`, `audit`, отчёты) требуют обязательный `scopeNodeId`, проверяют право на нём и фильтруют `path <@ scope.path`.

### 5.2 Линия 2: row-level security

Роли БД (файл `backend/db/bootstrap/roles.sql`, выполняет суперпользователь: локально `make db-bootstrap` в контейнере, в CI отдельный шаг, в тестах `testkit`):

| Роль | Атрибуты | Назначение |
|---|---|---|
| `tb_owner` | LOGIN, NOSUPERUSER, NOBYPASSRLS, владелец БД и всех таблиц | миграции, seed |
| `tb_app` | LOGIN, NOSUPERUSER, NOBYPASSRLS, NOCREATEDB, NOCREATEROLE, не владелец таблиц | рантайм API |

RLS включается `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` (без FORCE): владелец `tb_owner` обходит политики своих таблиц, что нужно миграциям, seed и SECURITY DEFINER-функциям ниже. `tb_app` политикам подчиняется всегда. Тест T-ISO-15 проверяет атрибуты ролей и что RLS включён на всех таблицах из списка ниже (EC-60).

Функции (владелец `tb_owner`, `SET search_path = pg_catalog, public, pg_temp`):

```sql
CREATE FUNCTION tb_actor_id() RETURNS uuid LANGUAGE sql STABLE AS $$
  SELECT nullif(current_setting('app.actor_id', true), '')::uuid $$;

CREATE FUNCTION tb_actor_scopes() RETURNS ltree[] LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = public, pg_temp AS $$
  SELECT coalesce(array_agg(n.path), '{}')
  FROM role_assignments ra
  JOIN nodes n ON n.id = ra.node_id
  JOIN users u ON u.id = ra.user_id AND u.status = 'active'
  WHERE ra.user_id = tb_actor_id() $$;
```

Политики (`ltree <@ ltree[]` = «массив содержит предка или сам путь»):

| Таблица | SELECT | INSERT (WITH CHECK) | UPDATE (USING / WITH CHECK) | DELETE |
|---|---|---|---|---|
| `nodes` | `path <@ (SELECT tb_actor_scopes())` | то же | то же / то же | то же |
| `role_assignments` | `node_id IN (SELECT id FROM nodes) OR user_id = tb_actor_id()` | `node_id IN (SELECT id FROM nodes)` | то же | то же |
| `node_settings`, `balances` | `node_id IN (SELECT id FROM nodes)` | то же | то же | то же |
| `operations` | то же | то же | нет GRANT | нет GRANT |
| `audit_log` | то же | то же | нет GRANT + триггер | нет GRANT + триггер |
| `idempotency_keys` | `user_id = tb_actor_id()` | то же | то же | то же |
| `users` | `id = tb_actor_id() OR id IN (SELECT user_id FROM role_assignments)` | `true` (создание пользователя админом; без назначения он невидим) | `id = tb_actor_id() OR id IN (SELECT user_id FROM role_assignments WHERE node_id IN (SELECT id FROM nodes))` | нет GRANT |

`refresh_tokens` без RLS: в нём нет данных тенантов, доступ только через репозиторий аутентификации по хэшу токена. Аутентификация до появления актора (логин, проверка токена) идёт через SECURITY DEFINER-функции `tb_auth_user_by_email(text)` и `tb_auth_user_by_id(uuid)`, возвращающие `(id, password_hash, status, token_version)` (по email, для входа) и `(id, status, token_version)` (по id, для обновления токена: хеш пароля не нужен и не возвращается); `tb_app` имеет только EXECUTE на них.

Права на колонки `users` для `tb_app`: `SELECT` без `password_hash` (`SELECT *` падает с 42501), `INSERT` (создание участника с начальным хешем), `UPDATE` только `(status, token_version)`; `DELETE` нет. Хеш пароля после создания меняет единственная функция `tb_set_own_password(p_hash text)` (SECURITY DEFINER, строка выбирается по `tb_actor_id()`, аргументов-идентификаторов нет, без актора ничего не делает). Это закрывает смену пароля чужого пользователя через видимую строку в `users_update`.

Подзапрос `node_id IN (SELECT id FROM nodes)` сам подчиняется политике `nodes`, поэтому видимость всех таблиц выводится из видимости узлов. Рекурсии нет: `tb_actor_scopes()` читает `role_assignments` от имени владельца.

#### Известные ограничения RLS

- RLS изолирует по поддереву, но не проверяет права «роль × действие». Актор с ролью `viewer` на узле на уровне БД может вставить строку в `role_assignments` для видимого узла. Матрицу прав и `CanGrant`/`CanRevoke` проверяет use case (M2.3 и далее), это покрыто тестами уровня use case.
- `tb_user_id_by_email` позволяет любому актору с областью доступа узнать, существует ли email в системе (оракул существования учётной записи между тенантами, EC-109). Смягчение: ограничение частоты в API (M4) и одинаковое время ответа, не более.
- `tb_node_effective_active` может раскрыть факт неактивного предка выше корня доступа. Это осознанное поведение «эффективной активности» (EC-18, EC-20).
- `WITH CHECK` политики `nodes_update` дублируется SELECT-политикой: PostgreSQL проверяет новую строку UPDATE и по ней, поэтому одна из двух защит избыточна (defense in depth).

- `INSERT ... ON CONFLICT` по `users` невозможен под `tb_app` для нового пользователя: PostgreSQL проверяет новую строку по SELECT-политике, а ещё не назначенный пользователь ей не проходит. Создание пользователя идёт простым `INSERT` в savepoint без `RETURNING` (M2.6).

### 5.3 Передача актора в сессию и пул соединений (Р-13)

Порт `port.TxRunner`:

```go
type TxOptions struct { ReadOnly bool; Isolation Isolation /* ReadCommitted | RepeatableRead */ }
type TxRunner interface {
    InTx(ctx context.Context, actor user.ActorID, opts TxOptions, fn func(ctx context.Context, tx Tx) error) error
}
```

Реализация `adapter/postgres.TxRunner` на `pgxpool`:

1. `pool.BeginTx(ctx, opts)`.
2. `SELECT set_config('app.actor_id', $1, true)` — `is_local=true`: значение живёт до конца транзакции и сбрасывается при COMMIT/ROLLBACK, поэтому соединение возвращается в пул без контекста актора (EC-58).
3. `fn(ctx, tx)`; ошибка -> `ROLLBACK`; panic -> `ROLLBACK` в `defer`, затем re-panic (HTTP-middleware превращает в 500) (EC-59).
4. `COMMIT`; ошибка коммита возвращается как ошибка операции.

Все обращения к таблицам тенантов, включая чтения, идут через `InTx` (чтения с `ReadOnly: true`). Вне `InTx` у `tb_app` нет `app.actor_id`, `tb_actor_id()` = NULL, политики возвращают 0 строк и отклоняют вставки (fail closed, EC-57). После завершения транзакции `current_setting('app.actor_id', true)` на том же соединении возвращает пустую строку, `nullif` превращает её в NULL.

Параметры пула (`postgres.Config`): `MaxConns`, `statement_timeout=5s` и `idle_in_transaction_session_timeout=10s` через `RuntimeParams`. Потеря соединения посреди транзакции: pgx возвращает ошибку, транзакция на сервере откатывается при разрыве, соединение уничтожается пулом; use case возвращает `apperr.Unavailable` -> 503 `db_unavailable` (EC-83). Повтор с тем же Idempotency-Key безопасен, потому что ключ сохраняется в той же транзакции.

Ошибка RLS при записи (`new row violates row-level security policy`, SQLSTATE 42501) не должна возникать, если линия 1 корректна; адаптер маппит её в `apperr.NotFound` и логирует на уровне `error` как сигнал ошибки в use case (EC-56).

### 5.4 Согласованность прав в пределах запроса

READ COMMITTED: каждый оператор видит свой снимок. Если отзыв роли коммитится посреди чужой транзакции, линия 1 (проверка в начале) могла пропустить, а последующие операторы с RLS уже видят меньше: результат строже, не мягче (fail closed), запрос получает 404 или 0 строк. Запрос, полностью завершившийся до коммита отзыва, выполнен со старыми правами; это граница линеаризации, зафиксирована в EC-39.

## 6. 404 против 403 (Р-11)

| Ситуация | Ответ | Код |
|---|---|---|
| Узел (или объект узла) не существует | 404 | `not_found` |
| Узел существует, но вне поддеревьев актора | 404, тело байт-в-байт как для несуществующего (кроме `requestId`) | `not_found` |
| Узел в поддереве, роль не даёт права | 403 | `forbidden` |
| id не UUID | 400 | `invalid_id` (до любых обращений к БД; одинаково для всех акторов) |

Почему 404: 403 подтверждал бы, что объект с таким id существует в чужой ветке, это позволяет перечислять и сопоставлять идентификаторы между тенантами (EC-54). 404 совпадает с тем, что естественно даёт RLS (строки нет). Последствия: (1) клиент не отличает «нет доступа» от «нет объекта», фронт показывает общий экран «не найдено или нет доступа» (EC-118); (2) отладка сложнее, поэтому в лог `slog` (не в ответ) пишется причина `reason=out_of_scope|missing` с `request_id`; (3) 403 остаётся информативным внутри собственного поддерева, где существование объекта актору и так известно.

UUIDv7 содержит время создания и не является секретом; защита держится на проверках, а не на непредсказуемости id.

## 7. Аутентификация и свежесть прав (Р-08, Р-09)

- Пароли: argon2id, параметры `m=19456 KiB, t=2, p=1, keyLen=32, saltLen=16` (минимум рекомендации OWASP Password Storage Cheat Sheet; проверить актуальность в M2.4 и записать ссылку в отчёт), формат PHC-строки `$argon2id$v=19$m=...,t=...,p=...$salt$hash`. Сравнение `subtle.ConstantTimeCompare`. Для несуществующего email вычисляется хэш против фиктивного хэша, ответ одинаковый `invalid_credentials` (EC-107).
- Access token: JWT, `alg=EdDSA` (Ed25519), заголовок `kid`, claims `sub` (user id), `tv` (token_version), `iat`, `nbf`, `exp` (+10 мин), `iss=tenant-backoffice`, `aud=tenant-backoffice-api`, `jti`. Проверка: `jwt.WithValidMethods([]string{"EdDSA"})`, issuer, audience, leeway 30 с (EC-102, EC-103, EC-112).
- Ротация ключей: каталог `TB_JWT_KEYS_DIR` содержит один или два ключа; подписывает `TB_JWT_ACTIVE_KID`, проверка принимает любой kid из каталога. Процедура: добавить новый ключ, переключить активный, через `TB_ACCESS_TTL` удалить старый (EC-104). Ключи для разработки генерирует `make keys` (в `.gitignore`); CI генерирует на лету.
- Refresh token: 32 случайных байта (base64url), в БД только sha256; cookie `tb_refresh`, `HttpOnly; Secure` (кроме dev по флагу конфига); `SameSite=Strict; Path=/api/v1/auth`. При каждом refresh старый помечается `used_at`, выдаётся новый в той же `family_id`. Предъявление уже использованного -> вся семья `revoked_at`, 401 (EC-106). Logout отзывает семью. `/auth/refresh` дополнительно требует заголовок `X-TB-CSRF: 1` (простая защита от межсайтовой отправки формы).
- Проверка на каждом запросе (middleware `httpapi.Authn`): подпись и claims -> `tb_auth_user_by_id(sub)` -> пользователь `active` и `token_version == tv`, иначе 401 `token_invalid` (EC-105). `token_version` увеличивается при смене пароля, деактивации пользователя и logout-all.
- Роли в токене не хранятся: use case и RLS читают назначения из БД в каждой транзакции. Понижение роли действует со следующего запроса (EC-38) без инвалидации токена.
- Rate limit: логин 5 попыток в минуту на пару (IP, email) и 20 на IP; мутации 60 в минуту на пользователя; `golang.org/x/time/rate`, хранилище в памяти процесса с вытеснением неактивных ключей; ответ 429 `rate_limited` + `Retry-After` (EC-108, EC-116). Честно: один экземпляр API.

Схема учебная: нет MFA, восстановления пароля, хранения ключей в KMS. Это пишется в README.

#### Изменения схемы M3.1 (отклонения от первоначальных таблиц политик)

- `balances`: нет `DELETE` для `tb_app` (строка уходит только вместе с узлом); `UPDATE` только колонок `(amount, version, updated_at)`.
- `operations` и `audit_log`: `INSERT` дополнительно требует `actor_id = tb_actor_id()`; обе таблицы неизменяемы (триггер `tb_raise_immutable`, включая `TRUNCATE`; функция лежит в `0005`).
- `idempotency_keys`: `SELECT`/`INSERT`/`DELETE` без `UPDATE`, политика `user_id = tb_actor_id() AND cardinality(tb_actor_scopes()) > 0`.
- `node_settings`: `INSERT`/`UPDATE` требуют `updated_by = tb_actor_id()`.
- `tb_settings_chain` возвращает `(source_node_id, depth, key, value)`, `source_node_id = NULL` для предков выше корня доступа.
- Известные ограничения: баланс не связан в БД с суммой операций (согласованность обеспечивает use case в одной транзакции и тест инварианта); записи аудита удалённого узла остаются в БД, но невидимы через RLS (нет внешнего ключа на `nodes`); `counterparty_node_id` может указывать на узел, невидимый читателю после последующего переноса.

#### Реализация HTTP-слоя (M4.2)

- Ограничение частоты: собственная корзина токенов (без `x/time/rate`), часы подставляются в тестах, число ключей ограничено; вход ограничивается по паре (IP, email) и по IP, остальные запросы по пользователю (чтение 600 в минуту).
- Конфигурация: `TB_RATE_*`, `TB_HTTP_TRUST_FORWARDED_FOR` (при включении берётся последняя запись `X-Forwarded-For`, добавленная доверенным прокси), `TB_CURSOR_KEY_PREVIOUS` (ротация), `TB_CURSOR_TTL`, `TB_HTTP_MAX_BODY_BYTES`.
- Любой маршрут под `/api/v1` требует Bearer-токен, кроме `POST /auth/login` и `POST /auth/refresh` (cookie и CSRF проверяет обработчик); это закреплено тестом `TestEveryOperationExceptPublicRequiresBearer`.
- Cookie refresh: `HttpOnly`, `SameSite=Strict`, `Path=/api/v1/auth` фиксированы; `Secure` задаётся `TB_COOKIE_SECURE` (по умолчанию включён; compose для локальной работы по http ставит `false`). Refresh и logout требуют заголовок `X-TB-CSRF: 1`; проверка выполняется до использования токена, поэтому отказ не расходует refresh-токен.
