# 03. REST API

Источник правды — `api/openapi.yaml` (OpenAPI 3.0.3, Р-10). Этот файл задаёт содержание, M4.1 переносит его в OpenAPI. При расхождении после M4.1 прав OpenAPI, а этот файл обновляется.

Генерация: Go — `oapi-codegen` с `generate: {std-http-server: true, strict-server: true, models: true}` в `backend/internal/adapter/httpapi/gen`; фронт — `openapi-typescript api/openapi.yaml -o web/src/api/schema.d.ts`. `make gen-check` регенерирует и падает на `git diff --exit-code` (T-CI-03, EC-128).

## 1. Общие правила

- База: `/api/v1`. JSON UTF-8. Health вне версии: `GET /healthz` (процесс жив), `GET /readyz` (БД отвечает, иначе 503) (EC-135).
- Аутентификация: `Authorization: Bearer <access JWT>` на всех `/api/v1/*`, кроме `POST /auth/login`, `POST /auth/refresh`.
- Идентификаторы — UUID в канонической форме; невалидный -> 400 `invalid_id` (EC-115).
- Деньги — строка цифр минимальных единиц (`"1050"`), Р-07. Число JSON в поле суммы -> 400 `invalid_amount` (EC-76).
- Время — RFC 3339 UTC с `Z` в ответах; во входах принимается RFC 3339 с любым смещением.
- Тело запроса ≤ 64 KiB (`http.MaxBytesReader`), иначе 413 `payload_too_large` (EC-113). Неизвестные поля -> 400 `validation_failed` (EC-114). `Content-Type` не `application/json` на запросах с телом -> 415 `unsupported_media_type`.
- Заголовок ответа `X-Request-Id` (входящий принимается, если `^[A-Za-z0-9-]{8,64}$`, иначе генерируется).
- Rate limit -> 429 `rate_limited` + `Retry-After` (секунды).

## 2. Формат ошибок

### 2.1 Тело (RFC 9457 problem details, `application/problem+json`)

```json
{
  "type": "https://tenant-backoffice.invalid/problems/validation_failed",
  "title": "Validation failed",
  "status": 400,
  "code": "validation_failed",
  "detail": "One or more fields are invalid.",
  "errors": [{ "field": "name", "code": "invalid_name", "message": "Name must be 1..100 characters." }],
  "requestId": "01J..."
}
```

`errors` есть только у 400/422 с ошибками полей. `detail` не содержит внутренних сообщений БД, стектрейсов, идентификаторов чужих объектов.

### 2.2 Маппинг в `httpapi/errors.go`

Единственное место маппинга `apperr.Code -> HTTP`. Неизвестная ошибка -> 500 `internal`, сообщение в лог с `request_id`, в ответе общий текст.

### 2.3 Коды

| Код | HTTP | Когда |
|---|---|---|
| `validation_failed` | 400 | `Input.Validate()` или декодирование; детали в `errors[]` |
| `invalid_id` | 400 | id в пути или фильтре не UUID |
| `invalid_amount` | 400 | сумма не строка цифр / > MaxInt64 |
| `amount_must_be_positive` | 400 | сумма 0 |
| `invalid_cursor` | 400 | курсор повреждён, подделан или от других фильтров/scope |
| `invalid_period` | 400 | `from >= to`, период > 366 дней, неизвестная `granularity`, невалидный `tz` |
| `same_node` | 400 | перевод на тот же узел |
| `invalid_password` | 400 | пароль не соответствует правилам (длина, UTF-8) |
| `invalid_node_type` | 400 | недопустимый тип узла или тип родителя |
| `invalid_name` | 400 | имя узла не проходит нормализацию и проверки длины |
| `invalid_email` | 400 | email не проходит проверку формата |
| `invalid_role` | 400 | неизвестная роль |
| `idempotency_key_required` | 400 | нет `Idempotency-Key` у операции баланса |
| `idempotency_key_invalid` | 400 | длина вне 1..128 или символы вне `[A-Za-z0-9_\-:.]` |
| `invalid_credentials` | 401 | логин не удался |
| `token_missing`, `token_expired`, `token_invalid` | 401 | см. `02` §7 |
| `refresh_invalid` | 401 | refresh отсутствует, истёк, отозван, повторно использован |
| `forbidden` | 403 | объект виден, права нет |
| `not_found` | 404 | нет объекта или он вне поддерева (Р-11) |
| `name_taken` | 409 | имя занято у этого родителя |
| `node_inactive` | 409 | операция над неактивным узлом / под неактивного родителя |
| `operations_disabled` | 409 | эффективная `operations_enabled=false` |
| `insufficient_funds` | 409 | баланс стал бы < 0 |
| `balance_overflow` | 409 | баланс стал бы > MaxInt64 |
| `node_has_children`, `node_has_balance`, `node_has_history` | 409 | удаление невозможно |
| `children_limit` | 409 | у родителя уже 1000 детей |
| `last_root_owner` | 409 | снятие или понижение последнего owner корня |
| `user_inactive` | 409 | назначение роли неактивному пользователю |
| `invalid_parent_type` | 422 | тип родителя недопустим |
| `move_cycle` | 422 | перенос в себя или потомка |
| `root_immutable` | 422 | перенос/удаление корня |
| `role_not_grantable` | 403 | назначить/снять роль выше своей (отдельный код внутри 403 для UI) |
| `unknown_setting` | 404 | ключ настройки не из списка |
| `invalid_setting_value` | 422 | значение настройки не прошло валидацию |
| `amount_exceeds_limit` | 422 | сумма > эффективного `max_operation_amount` |
| `idempotency_key_reused` | 422 | ключ уже использован с другим запросом |
| `payload_too_large` | 413 | |
| `unsupported_media_type` | 415 | |
| `rate_limited` | 429 | |
| `db_unavailable` | 503 | БД недоступна / потеря соединения |
| `internal` | 500 | прочее |

## 3. Пагинация (курсорная)

- Параметры: `limit` (1..200, по умолчанию 50; вне диапазона -> 400 `validation_failed`, EC-94), `cursor` (непрозрачная строка).
- Ответ: `{ "items": [...], "nextCursor": "..." | null }`. Общего количества нет (дорого и не нужно).
- Курсор: base64url(JSON `{"v":1,"k":[<значения ключа сортировки последнего элемента>],"f":"<hex sha256 от канонизированных фильтров и scopeNodeId>"}`) + `.` + base64url(HMAC-SHA256 по первой части, ключ `TB_CURSOR_KEY` из конфига). Проверка: подпись, версия, совпадение `f` с текущими фильтрами; иначе 400 `invalid_cursor` (EC-62, EC-93). Подпись не даёт доступа к данным (доступ проверяется заново), она лишь делает ошибки клиента явными.
- Порядок: операции и аудит `(created_at DESC, id DESC)`; дети узла и участники `(name_key|email, id) ASC`. Keyset (`WHERE (created_at, id) < ($1, $2)`) не дублирует и не пропускает существующие строки при вставках между страницами; строки, вставленные новее первой страницы, в текущий обход не попадают (EC-92).

## 4. Идемпотентность (Р-14)

Применяется к `POST /nodes/{id}/credits`, `POST /nodes/{id}/debits`, `POST /transfers`.

1. Заголовок `Idempotency-Key` обязателен (EC-70). Область ключа — пользователь: одинаковые ключи разных пользователей независимы (EC-68).
2. `request_hash = sha256(method + "\n" + route template + "\n" + path params + "\n" + канонический JSON входного DTO)`; канонический JSON = поля в алфавитном порядке, без пробелов, сумма как строка. Другой маршрут с тем же телом даёт другой хэш (EC-69).
3. В транзакции операции, после проверки прав и до изменения баланса: `INSERT INTO idempotency_keys ... ON CONFLICT (user_id, key) DO NOTHING RETURNING ...`.
   - Вставка прошла -> выполнить операцию, записать `operation_id`/`transfer_id` в строку ключа, COMMIT, ответ 201.
   - Конфликт -> Postgres дожидается завершения конкурирующей транзакции с тем же ключом (EC-67). Затем `SELECT` строки: истекла (`expires_at <= now`) -> удалить и вставить заново (EC-72); хэш другой -> 422 `idempotency_key_reused` (EC-66); хэш совпадает -> прочитать операцию и вернуть тот же статус 201 и то же тело, заголовок `Idempotent-Replayed: true` (EC-65).
4. Если операция завершилась ошибкой (бизнес-ошибка, 5xx), транзакция откатывается вместе со строкой ключа: повтор с тем же ключом выполняется заново (EC-71). Решение: кэшируются только успешные результаты, потому что неуспешная попытка ничего не изменила и повтор безопасен.
5. TTL 24 ч; просроченные строки заменяются лениво (п. 3). Фоновая очистка таблицы вне рамки (упомянуть в README).

## 5. Эндпоинты

Обозначения прав — `01_domain.md` §3.3. «Узел» в колонке права — на каком узле проверяется. Все ответы с ошибками в формате §2. Общие для всех: 400 `invalid_id`/`validation_failed`, 401, 404 (вне поддерева), 429, 503.

### 5.1 Аутентификация и профиль

| Метод, путь | Право | Вход | Выход | Особые ошибки |
|---|---|---|---|---|
| `POST /auth/login` | нет | `{email, password}` | 200 `{accessToken, expiresIn, user:{id,email}}` + `Set-Cookie: tb_refresh` | 401 `invalid_credentials`, 429 |
| `POST /auth/refresh` | cookie + `X-TB-CSRF: 1` | — | 200 как login, новая cookie | 401 `refresh_invalid` |
| `POST /auth/logout` | токен | — | 204, cookie очищена, семья отозвана | |
| `POST /auth/password` | токен | `{currentPassword, newPassword}` | 204; `token_version++`, все refresh-семьи отозваны | 401 `invalid_credentials`, 400 `invalid_password` |
| `GET /me` | токен | — | 200 `{id, email, accessRoots:[{nodeId, name, type, role}]}` | |
| `GET /me/permissions?nodeId=` | `node.read` на узле | — | 200 `{nodeId, role, permissions:[...]}` | |

### 5.2 Узлы

| Метод, путь | Право (узел) | Вход | Выход | Особые ошибки |
|---|---|---|---|---|
| `GET /nodes/{id}` | `node.read` (id) | — | 200 `Node` = `{id, type, parentId, name, status, effectiveActive, childrenCount, breadcrumbs:[{id,name,type}], createdAt, updatedAt}`; breadcrumbs начинаются с корня доступа актора (предки вне поддерева не раскрываются) | |
| `GET /nodes/{id}/children` | `node.read` (id) | `limit, cursor, status=active\|inactive\|all (all), q` (поиск по подстроке имени, `%`/`_` экранируются) | 200 page of `NodeSummary {id,type,name,status,childrenCount}` | |
| `POST /nodes/{id}/children` | `node.create` (id) | `{type, name}` | 201 `Node` | 409 `name_taken`, `node_inactive`, `children_limit`; 422 `invalid_parent_type` |
| `PATCH /nodes/{id}` | `node.rename` (id) | `{name}` | 200 `Node` | 409 `name_taken` |
| `POST /nodes/{id}/move` | `node.move` (текущий родитель и `newParentId`) | `{newParentId}` | 200 `Node` | 409 `name_taken`, `node_inactive`, `children_limit`; 422 `move_cycle`, `invalid_parent_type`, `root_immutable` |
| `GET /nodes/{id}/move-targets` | `node.move` (текущий родитель) | `limit, cursor, q` | 200 page of `NodeSummary` — узлы допустимого типа в поддеревьях актора, где у него `node.move`, кроме текущего родителя | 422 `root_immutable` |
| `POST /nodes/{id}/deactivate` | `node.deactivate` (id) | — | 200 `Node` | 422 `root_immutable` |
| `POST /nodes/{id}/activate` | `node.deactivate` (id) | — | 200 `Node` | |
| `DELETE /nodes/{id}` | `node.delete` (родитель) | — | 204 | 409 `node_has_children`, `node_has_balance`, `node_has_history`; 422 `root_immutable` |

### 5.3 Настройки

| Метод, путь | Право | Вход | Выход | Ошибки |
|---|---|---|---|---|
| `GET /nodes/{id}/settings` | `settings.read` | — | 200 `{items:[{key, own: value\|null, effective: value, sourceNodeId}]}` | |
| `PUT /nodes/{id}/settings/{key}` | `settings.write` | `{value}` (тип по ключу; `max_operation_amount` строкой) | 200 та же форма для ключа | 404 `unknown_setting`, 422 `invalid_setting_value` |
| `DELETE /nodes/{id}/settings/{key}` | `settings.write` | — | 200 | 404 `unknown_setting` |

### 5.4 Участники (роли)

| Метод, путь | Право | Вход | Выход | Ошибки |
|---|---|---|---|---|
| `GET /nodes/{id}/members` | `member.read` | `limit, cursor, inherited=false\|true` | 200 page of `{userId, email, role, nodeId, inheritedFrom: nodeId\|null, grantedAt}`; `inherited=true` добавляет назначения на предках в пределах поддеревьев актора | |
| `POST /nodes/{id}/members` | `member.manage` + `CanGrant` | `{email, role, initialPassword?}` | 201 `Member`. Если пользователя с email нет — создаётся (нужен `initialPassword`, иначе 400); если есть — получает назначение, `initialPassword` игнорируется, в ответе нет признака «создан/существовал» | 403 `role_not_grantable`, 409 `user_inactive` |
| `PUT /nodes/{id}/members/{userId}` | `member.manage` + `CanGrant(new)` и `CanRevoke(old)` | `{role}` | 200 `Member` | 403 `role_not_grantable`, 409 `last_root_owner`; 404 если назначения на этом узле нет |
| `DELETE /nodes/{id}/members/{userId}` | `member.manage` + `CanRevoke` | — | 204 | 403 `role_not_grantable`, 404, 409 `last_root_owner` |

### 5.5 Балансы и операции

| Метод, путь | Право | Вход | Выход | Ошибки |
|---|---|---|---|---|
| `GET /nodes/{id}/balance` | `balance.read` | — | 200 `{nodeId, amount, currency, updatedAt}` | |
| `POST /nodes/{id}/credits` | `balance.credit` | `Idempotency-Key`; `{amount, comment?}` | 201 `Operation` = `{id, nodeId, kind, amount, balanceAfter, transferId, counterpartyNodeId, comment, actorId, createdAt}` | 409 `node_inactive`, `operations_disabled`, `balance_overflow`; 422 `amount_exceeds_limit`, `idempotency_key_reused` |
| `POST /nodes/{id}/debits` | `balance.debit` | то же | 201 `Operation` | + 409 `insufficient_funds` |
| `POST /transfers` | `balance.transfer` на обоих | `Idempotency-Key`; `{fromNodeId, toNodeId, amount, comment?}` | 201 `{transferId, out: Operation, in: Operation}` | 400 `same_node`; как debit |
| `GET /operations` | `operation.read` на `scopeNodeId` | `scopeNodeId` (обязателен), `nodeId?` (должен быть в поддереве scope, иначе 404), `kind?` (повторяемый), `from?`, `to?`, `limit`, `cursor` | 200 page of `Operation` + `nodeName` | |
| `GET /operations/{id}` | `operation.read` на узле операции | — | 200 `Operation` | |

### 5.6 Отчёты (`05_reports.md`)

| Метод, путь | Право | Вход | Выход |
|---|---|---|---|
| `GET /nodes/{id}/reports/summary` | `report.read` | `from` (дата `YYYY-MM-DD`), `to` (дата, исключительно), `tz?`, `granularity=day\|week\|month`, `kind?` | 200 `{scopeNodeId, tz, totals, series:[{periodStart, count, credits, debits, net}]}` |
| `GET /nodes/{id}/reports/children` | `report.read` | `from`, `to`, `tz?`, `includeInactive=true`, `limit`, `cursor` | 200 page of `{nodeId, name, type, active, count, credits, debits, net, balance}` |

### 5.7 Аудит

| Метод, путь | Право | Вход | Выход |
|---|---|---|---|
| `GET /audit` | `audit.read` на `scopeNodeId` | `scopeNodeId` (обязателен), `nodeId?`, `action?`, `actorId?`, `from?`, `to?`, `limit`, `cursor` | 200 page of `{id, occurredAt, actorId, actorEmail, nodeId, nodeName, action, targetType, targetId, before, after, requestId}` |

`actorEmail` возвращается, только если пользователь-актор виден запрашивающему (RLS `users`), иначе `null` (EC-61).

## 6. Пример OpenAPI-фрагмента (стиль для M4.1)

```yaml
components:
  schemas:
    MoneyAmount:
      type: string
      pattern: '^(0|[1-9][0-9]{0,18})$'
      description: Integer amount in minor units, as a string.
  parameters:
    IdempotencyKey:
      name: Idempotency-Key
      in: header
      required: true
      schema: { type: string, minLength: 1, maxLength: 128, pattern: '^[A-Za-z0-9_\-:.]+$' }
  responses:
    Problem:
      description: Error
      content:
        application/problem+json:
          schema: { $ref: '#/components/schemas/Problem' }
```

Валидация по `pattern` из OpenAPI в рантайме не включается (нет middleware-валидатора): источник проверок — `Input.Validate()` и домен; OpenAPI документирует их. Тест T-CI-03 проверяет, что `api/openapi.yaml` валиден (`kin-openapi` loader + `Validate`).

Примечание M4.2: курсор содержит поле срока жизни `e`; код `not_implemented` (501) временный и существует только пока обработчики не реализованы.

Примечание M4.3: ошибки полей узла (`invalid_node_type`, `invalid_name`) приходят как `validation_failed` с причиной в `errors[]`, а не как верхнеуровневый код; `move-targets` для корня возвращает пустую страницу; refresh без заголовка CSRF даёт 401 `refresh_invalid`, logout без CSRF 401 `token_invalid`. В контракте исправлено имя заголовка ответа `Set-Cookie` (генератор писал `SetCookie`, и браузер не получал cookie).
