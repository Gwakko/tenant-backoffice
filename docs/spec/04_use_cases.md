# 04. Use cases

Пакеты `backend/internal/usecase/<feature>`. Конвенции — `00_overview.md` §5. Здесь: поля Input/Output, правила `Validate()`, шаги `Execute`, граница транзакции, аудит.

## 0. Общие порты (`usecase/port`)

```go
type Tx interface{ /* маркер; репозитории принимают tx и приводят к pgx.Tx в адаптере */ }
type TxRunner interface { InTx(ctx, actor user.ActorID, opts TxOptions, fn func(ctx context.Context, tx Tx) error) error }
type Clock interface { Now() time.Time }      // UTC
type IDGen interface { New() uuid.UUID }      // UUIDv7
type AuditWriter interface { Write(ctx context.Context, tx Tx, e audit.Entry) error }
type TreeLock interface { Lock(ctx context.Context, tx Tx) error } // pg_advisory_xact_lock
```

Репозитории — интерфейсы в пакете use case (потребитель): `NodeRepo`, `MemberRepo`, `UserRepo`, `SettingsRepo`, `BalanceRepo`, `OperationRepo`, `IdempotencyRepo`, `ReportRepo`, `AuditRepo`, `AuthUserRepo`, `RefreshTokenRepo`. Все методы получают `tx`.

Общий шаблон `Execute`:

```
1. in.Validate()                       -> apperr validation_failed (без БД)
2. runner.InTx(ctx, in.Actor, opts, func(tx) {
3.    authz.Require(... для каждого id) -> not_found | forbidden
4.    [TreeLock.Lock, если структурная операция; затем перечитать данные]
5.    загрузить сущности, доменная логика (чистые функции)
6.    записать изменения
7.    AuditWriter.Write (если было изменение)
8. })
9. собрать Output
```

Unit-тесты use cases используют in-memory фейки портов (`internal/usecase/<feature>/fakes_test.go` или общий `internal/testkit/fakes`), фейк `TxRunner` выполняет `fn` без БД, фейк `Checker` задаёт роли по карте.

## 1. Аутентификация (`usecase/auth`)

### UC-A1 `Login`
- Input: `Email string, Password string, IP string, UserAgent string`.
- Validate: email и пароль непустые, длина пароля ≤ 128 байт (иначе сразу `invalid_credentials`, не `validation_failed`, чтобы не давать оракул).
- Шаги (вне `InTx` с актором, через SECURITY DEFINER-функции): rate limit (адаптер в handler, не в use case); `AuthUserRepo.ByEmail`; нет пользователя -> сравнить с фиктивным хэшем; неверный пароль или `inactive` -> `invalid_credentials`; создать refresh (новая `family_id`); выпустить access.
- Output: `AccessToken string, ExpiresIn time.Duration, RefreshToken string, RefreshExpiresAt time.Time, UserID, Email`.
- Транзакций до проверки пароля нет: поиск пользователя идёт функциями `SECURITY DEFINER` без актора; после успешной проверки пароля транзакция открывается уже от имени найденного пользователя (только `refresh_tokens`). Аудит: нет (вход не изменяет данные тенанта; лог `slog` info без пароля).

### UC-A2 `Refresh`
- Input: `RefreshToken string`. Validate: непустой, base64url 43 символа.
- Шаги: `SELECT ... FOR UPDATE` по `token_hash`; нет / истёк / отозван -> `refresh_invalid`; `used_at` не NULL -> отозвать семью, `refresh_invalid` (EC-106); проверить пользователя `active`; пометить `used_at`, создать новый в той же семье, выпустить access.
- Output: как UC-A1.

### UC-A3 `Logout` — Input `RefreshToken`; отзывает семью; всегда успех (неизвестный токен — тоже 204).
### UC-A4 `ChangePassword`
- Input: `Actor, CurrentPassword, NewPassword, RequestID`. Validate: новый 12..128 байт, отличается от текущего.
- Шаги: проверить текущий; новый хэш; `token_version++`; отозвать все семьи пользователя. Аудит `auth.password_changed` на каждом корне доступа пользователя без `Before/After` (EC-100). Если корней нет — аудит не пишется (нет узла).

### UC-A5 `Authenticate` (используется middleware)
- Input: `AccessToken string`. Output: `Actor user.ActorID`.
- Шаги: проверка подписи и claims (адаптер `authn.Verifier`), `AuthUserRepo.ByID`, `active` и `tv` совпадает, иначе `token_invalid` (EC-105).

## 2. Узлы (`usecase/node`)

### UC-N1 `CreateNode`
- Input: `Actor, RequestID, ParentID node.ID, Type string, Name string`.
- Validate: `ParseType`, `ParseName`.
- Шаги: Require(`node.create`, ParentID) -> TreeLock -> перечитать родителя (path, effective active) -> `CountChildren(parent) < 1000` (EC-26) -> `node.NewChild` (тип, активность) -> вставка (unique `name_key` -> `name_taken`, EC-23) -> создать строку `balances` с 0 -> аудит `node.created` на новом узле (`after: type, name, parent_id`).
- Output: `Node node.View` (View = сущность + `EffectiveActive`, `ChildrenCount`, `Breadcrumbs`).
- Транзакция: READ COMMITTED, одна.

### UC-N2 `RenameNode`
- Input: `Actor, RequestID, NodeID, Name`. Validate: `ParseName`.
- Шаги: Require(`node.rename`) -> `SELECT ... FOR UPDATE` узла -> `Rename`; не изменилось -> вернуть без записи и аудита (EC-24) -> UPDATE (`name_taken` по unique) -> аудит `node.renamed` (`before.name`, `after.name`).

### UC-N3 `MoveNode`
- Input: `Actor, RequestID, NodeID, NewParentID`.
- Validate: оба UUID; `NodeID != NewParentID` проверяется в домене как `move_cycle` (EC-05), а не в Validate, чтобы порядок ошибок был единым.
- Шаги: Require(`node.read`, NodeID) -> узел корень? -> `root_immutable` (EC-08) -> Require(`node.move`, текущий родитель) (EC-45: нет права -> `forbidden`, т.к. узел виден) -> Require(`node.move`, NewParentID) (вне поддерева -> 404, EC-49) -> TreeLock -> перечитать узел, старого и нового родителя, размер поддерева -> `PlanMove` (`move_cycle` EC-04, `invalid_parent_type` EC-06, `node_inactive` EC-13, NoOp EC-07 -> вернуть без записи) -> лимит детей нового родителя -> проверка имени у нового родителя (`name_taken`, EC-25) -> UPDATE поддерева (§3 файла 02), сверка числа строк -> три записи аудита (`node.moved`, `node.child_moved_out`, `node.child_moved_in`, EC-98).
- Output: `Node node.View`.

### UC-N4 `DeactivateNode` / UC-N5 `ActivateNode`
- Input: `Actor, RequestID, NodeID`.
- Шаги: Require(`node.deactivate`) -> корень при деактивации -> `root_immutable` -> FOR UPDATE -> `Deactivate`/`Activate`; без изменения -> без аудита (EC-19) -> аудит `node.deactivated`/`node.activated`. Активация при неактивном предке разрешена, `EffectiveActive=false` в ответе (EC-20).

### UC-N6 `DeleteNode`
- Input: `Actor, RequestID, NodeID`.
- Шаги: Require(`node.read`, NodeID) -> корень -> `root_immutable` -> Require(`node.delete`, родитель) -> TreeLock -> перечитать -> `DeleteStats` (дети, баланс, есть ли операции) -> `CanDelete` (EC-14, EC-15, EC-16) -> DELETE `balances`, DELETE узла (назначения и настройки — каскад, EC-17) -> аудит `node.deleted` на родителе (`before: node_id, name, type`).
- Output: пусто (`DeleteNodeOutput struct{}`).

### UC-N7 `GetNode` — Input `Actor, NodeID`; READ ONLY; Require(`node.read`); Output `node.View` с breadcrumbs от корня доступа актора.
### UC-N8 `ListChildren` — Input `Actor, NodeID, Status, Query, Limit, Cursor`; Validate: limit 1..200, status из списка, query ≤ 100 рун; Output `Items []node.Summary, NextCursor string`. Пустое поддерево -> пустой список, `NextCursor=""` (EC-28).
### UC-N9 `ListMoveTargets` — Input `Actor, NodeID, Query, Limit, Cursor`; узлы типа `AllowedParent(type)`, видимые актору, где эффективная роль даёт `node.move`, кроме текущего родителя и неактивных.
### UC-N10 `ListAccessRoots` — для `GET /me`; Input `Actor`; Output `[]AccessRoot{NodeID, Name, Type, Role}`; пользователь с ролями в несвязанных ветках получает несколько корней (EC-32).
### UC-N11 `GetPermissions` — Input `Actor, NodeID`; Output `Role, Permissions []role.Permission`.

## 3. Участники (`usecase/member`)

### UC-M1 `AddMember`
- Input: `Actor, RequestID, NodeID, Email, Role string, InitialPassword string`.
- Validate: `ParseEmail` (EC-110), роль из списка; если `InitialPassword != ""` — 12..128 байт (EC-111).
- Шаги: Require(`member.manage`) -> `CanGrant(actorRole, role)` иначе `role_not_grantable` (EC-33) -> поиск пользователя через `tb_user_id_by_email`; если его нет и пароль задан: простой `INSERT` в savepoint (без `RETURNING` и без `ON CONFLICT`: оба требуют, чтобы новая строка проходила SELECT-политику, а ещё не назначенный пользователь ей не проходит); нарушение уникальности `users_email` -> повторный поиск (EC-109) -> если пользователь новый, а пароль пустой -> `validation_failed` (поле `initialPassword`) -> после вставки назначения читается `users.status` (до назначения чужой пользователь невидим): `inactive` -> `user_inactive`, транзакция откатывается (EC-41) -> upsert назначения (та же роль -> no-op без аудита, EC-40; другая -> замена с проверкой `CanRevoke` старой) -> аудит `user.created` (если создан, `after: email`) и `member.granted`/`member.changed` (`after: user_id, role`). Понижение роли существующего owner корня через повторное назначение проверяется так же, как в UC-M2 (`last_root_owner`, EC-148).
- Output: `Member{UserID, Email, Role, NodeID, GrantedAt}`.
- Хэш пароля вычисляется до `InTx` (дорогая операция вне транзакции), только если пароль передан.

### UC-M2 `ChangeMemberRole`
- Input: `Actor, RequestID, NodeID, UserID, Role`.
- Шаги: Require(`member.manage`) -> назначение на этом узле есть, иначе `not_found` -> `CanRevoke(actorRole, old)` и `CanGrant(actorRole, new)` -> если узел корень и old=`owner`, new≠`owner`: проверка последнего owner (см. UC-M3) -> UPDATE -> аудит `member.changed` (`before.role`, `after.role`).

### UC-M3 `RemoveMember`
- Input: `Actor, RequestID, NodeID, UserID`.
- Шаги: Require(`member.manage`) -> `CanRevoke` -> если узел корень и роль `owner`: `SELECT ... FROM role_assignments WHERE node_id = root AND role = 'owner' FOR UPDATE` (блокирует все owner-строки корня, сериализует параллельные снятия, EC-36), активных owner после снятия ≥1, иначе `last_root_owner` (EC-35) -> DELETE -> аудит `member.revoked`. Актор может снять собственное назначение; после этого узел ему не виден (EC-42).

### UC-M4 `ListMembers` — Input `Actor, NodeID, Inherited bool, Limit, Cursor`; Require(`member.read`); для каждого назначения на предке показывается только, если предок виден (EC-61).

## 4. Настройки (`usecase/settings`)

### UC-S1 `GetSettings` — Input `Actor, NodeID`; Require(`settings.read`); загрузить переопределения по цепочке от корня до узла. Предки выше корня доступа актора RLS не видны, поэтому цепочка читается SECURITY DEFINER-функцией `tb_settings_chain(node_id uuid)` (проверяет, что `node_id` виден актору, иначе пустой результат) -> `settings.Resolve`. В Output `SourceNodeID = nil`, если источник значения не виден актору (идентификатор чужого предка не раскрывается). Эта же функция используется UC-B1..B3 для эффективных `operations_enabled`/`max_operation_amount`.
### UC-S2 `SetSetting`
- Input: `Actor, RequestID, NodeID, Key string, Value settings.RawValue` (`RawValue` — простая структура `{Kind, Str, Bool}`; JSON распаковывает handler).
- Validate: ключ известен (`unknown_setting`), тип значения соответствует ключу.
- Шаги: Require(`settings.write`) -> доменная валидация (`invalid_setting_value`) -> upsert; значение не изменилось -> без аудита -> аудит `settings.set` (`before.value`, `after.value`).
### UC-S3 `UnsetSetting` — аналогично, аудит `settings.unset`; отсутствие переопределения -> no-op.

## 5. Балансы (`usecase/balance`)

### UC-B1 `Credit` / UC-B2 `Debit`
- Input: `Actor, RequestID, NodeID, Amount string, Comment string, IdempotencyKey string, RequestHash []byte`.
- Validate: `ParseAmount` (EC-76), `Positive` (EC-75), комментарий ≤ 200 рун, ключ 1..128 символов `[A-Za-z0-9_\-:.]` (EC-70), хэш 32 байта.
- Шаги (READ COMMITTED):
  1. Require(`balance.credit`|`balance.debit`).
  2. Идемпотентность (`03_api.md` §4 п. 3): replay -> вернуть Output с `Replayed=true` без дальнейших шагов.
  3. Узел effective active (EC-81), эффективные `operations_enabled`, `max_operation_amount` (EC-77).
  4. `SELECT amount FROM balances WHERE node_id = $1 FOR UPDATE` (EC-73).
  5. `balance.Apply` -> `insufficient_funds` (EC-74) / `balance_overflow` (EC-77).
  6. INSERT `operations` (balance_after), UPDATE `balances` (`version++`), UPDATE строки ключа (`operation_id`).
  7. Аудит `balance.credited`/`balance.debited` (`after: operation_id, amount, balance_after`).
- Output: `Operation balance.Operation, Replayed bool`.

### UC-B3 `Transfer`
- Input: `Actor, RequestID, FromNodeID, ToNodeID, Amount, Comment, IdempotencyKey, RequestHash`.
- Validate: как UC-B1 + `From != To` -> `same_node` (EC-78).
- Шаги: Require(`balance.transfer`) для обоих (вне поддерева любой -> 404, EC-50) -> идемпотентность -> активность и настройки обоих -> блокировка балансов `FOR UPDATE` в порядке возрастания `node_id` (EC-79) -> Apply на источнике и получателе -> две операции с общим `transfer_id` -> аудит `balance.transferred` на обоих узлах. Любая ошибка откатывает всё (EC-80).
- Output: `TransferID, Out, In balance.Operation, Replayed bool`.

### UC-B4 `GetBalance`, UC-B5 `ListOperations`, UC-B6 `GetOperation`
- `ListOperations` Input: `Actor, ScopeNodeID, NodeID *node.ID, Kinds []string, From, To *time.Time, Limit, Cursor`. Validate: kinds из списка, `From < To`, limit. Шаги: Require(`operation.read`, scope) -> если NodeID задан: он в поддереве scope, иначе `not_found` (EC-53) -> keyset-запрос с `JOIN nodes ON path <@ scope.path`. READ ONLY.

## 6. Отчёты (`usecase/report`) — подробно в `05_reports.md`

UC-R1 `SubtreeSummary`, UC-R2 `ChildrenBreakdown`. Транзакция `REPEATABLE READ READ ONLY`.

## 7. Аудит (`usecase/audit`)

UC-AU1 `ListAudit` — Input `Actor, ScopeNodeID, NodeID *node.ID, Action *string, ActorID *user.ID, From, To *time.Time, Limit, Cursor`. Require(`audit.read`, scope); видимость через RLS + фильтр `path <@ scope.path`. READ ONLY.

## 8. Сводка: транзакции и аудит

| Use case | Изоляция | TreeLock | Блокировки строк | Аудит |
|---|---|---|---|---|
| CreateNode | RC | да | — | `node.created` |
| RenameNode | RC | нет | узел FOR UPDATE | `node.renamed` |
| MoveNode | RC | да | — (под TreeLock) | `node.moved` + `child_moved_out` + `child_moved_in` |
| Deactivate/Activate | RC | нет | узел FOR UPDATE | `node.deactivated`/`activated` |
| DeleteNode | RC | да | — | `node.deleted` (на родителе) |
| AddMember | RC | нет | назначение (upsert) | `user.created`?, `member.granted`/`changed` |
| ChangeMemberRole / RemoveMember | RC | нет | owner-строки корня FOR UPDATE (если корень) | `member.changed`/`revoked` |
| SetSetting / UnsetSetting | RC | нет | строка настройки (upsert) | `settings.set`/`unset` |
| Credit / Debit | RC | нет | баланс FOR UPDATE | `balance.credited`/`debited` |
| Transfer | RC | нет | 2 баланса FOR UPDATE по порядку id | `balance.transferred` ×2 |
| ChangePassword | RC | нет | пользователь FOR UPDATE | `auth.password_changed` |
| Чтения, списки | RC READ ONLY | нет | — | — |
| Отчёты | RR READ ONLY | нет | — | — |

## Решения M3 (отклонения от первоначального текста)

- Строка `balances` создаётся лениво при первой блокировке, а не при создании узла (UC-N1); при удалении узла уходит каскадом.
- Строка ключа идемпотентности вставляется сразу с `operation_id`/`transfer_id` (`UPDATE` на `idempotency_keys` не выдан); успешно кэшируется только результат успешной операции, неуспешная попытка откатывает и ключ.
- UC-B3: `forbidden`, если исходный узел виден, а права на перевод нет; право проверяется на обоих узлах; блокировки балансов по возрастанию id.
- Реальная нехватка средств проверяется в use case до записи (`insufficient_funds`, 409): нарушение `CHECK` в адаптере не должно выглядеть как 404.
- UC-AU1: `NodeID` вне области доступа даёт пустую страницу; `actorEmail` пуст, если читатель не видит пользователя; `AuditWriter` не пишет запись с равными `before` и `after`; `child_moved_out`/`child_moved_in` несут `node_id` и `name` перемещённого узла и не содержат второго родителя.
- UC-S1…S3: результат `Item{Key, Own, Effective, SourceNodeID}`; `SourceNodeID = nil`, если источник выше корня доступа; значение настройки типизировано (`RawValue`), неверный тип JSON даёт `invalid_setting_value`.
