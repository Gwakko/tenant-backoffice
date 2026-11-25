# 01. Предметная модель и инварианты

Пакеты `backend/internal/domain/*`. Только стандартная библиотека, без тегов, без `context`, без «текущего времени» (время передаётся аргументом `now time.Time`).

## 1. Узлы

### 1.1 Типы и допустимые связи

| Тип (`node.Type`) | Уровень | Допустимый родитель | Допустимые дети | Количество |
|---|---|---|---|---|
| `platform` | 1 | нет (корень) | `partner` | ровно один в инсталляции |
| `partner` | 2 | `platform` | `company` | |
| `company` | 3 | `partner` | `unit` | |
| `unit` | 4 | `company` | нет (лист) | |

Решение (Р-01): enum фиксирован в коде и в БД (`CHECK`), тип однозначно задаёт глубину. Поэтому перенос допустим только под узел типа «уровень − 1»; цикл при переносе исключён правилом типов, но проверяется явно и раньше (понятная ошибка `move_cycle`, EC-04).

API домена:

```go
type Type string // "platform" | "partner" | "company" | "unit"
func ParseType(s string) (Type, error)        // ошибка: invalid_node_type
func (t Type) Level() int                      // 1..4
func (t Type) AllowedParent() (Type, bool)     // false для platform
func (t Type) CanHaveChild(c Type) bool
```

### 1.2 Сущность `node.Node`

| Поле | Тип | Правила |
|---|---|---|
| `ID` | `node.ID` (UUIDv7, генерирует порт `IDGen`) | неизменяем |
| `Type` | `node.Type` | неизменяем после создания |
| `ParentID` | `*node.ID` | nil только у `platform` |
| `Path` | `node.Path` (срез ID от корня до самого узла) | длина = `Type.Level()` |
| `Name` | `node.Name` | см. 1.3 |
| `Status` | `active` / `inactive` | собственный статус |
| `CreatedAt`, `UpdatedAt` | `time.Time` (UTC) | |

Производное: `EffectiveActive = Status == active && все предки active` (вычисляется в репозитории запросом по пути, в домене принимается как поле `AncestorsActive bool`).

Методы (все чистые, возвращают новую копию или ошибку):

- `NewChild(parent Node, id ID, t Type, name Name, now) (Node, error)` — `invalid_parent_type`, `node_inactive` (если родитель не effective active).
- `Rename(name Name, now) (Node, changed bool)` — одинаковое нормализованное имя -> `changed=false` (EC-24).
- `PlanMove(newParent Node) (MovePlan, error)` — проверки по порядку: корень (`root_immutable`), цикл (`newParent.Path` содержит `n.ID` -> `move_cycle`), тип (`invalid_parent_type`), новый родитель не effective active (`node_inactive`), тот же родитель (`MovePlan.NoOp=true`).
- `Deactivate(now)`, `Activate(now)` — повтор -> `changed=false` (EC-19).
- `CanDelete(stats DeleteStats) error` — `DeleteStats{Children int, Balance money.Amount, HasOperations bool}`; ошибки `node_has_children`, `node_has_balance`, `node_has_history`; корень -> `root_immutable`.

### 1.3 Имя узла `node.Name`

`ParseName(raw string) (Name, error)`:

1. Невалидный UTF-8 -> `invalid_name`.
2. Нормализация Unicode NFC. Пакет `golang.org/x/text/unicode/norm` (версию взять `go list -m golang.org/x/text@latest` в M1.6; стандартная библиотека NFC не даёт).
3. Trim пробелов по краям (`unicode.IsSpace`), внутренние последовательности пробельных символов -> один пробел U+0020.
4. Запрещены управляющие символы (`unicode.IsControl`) и символы категорий Cf (zero-width и bidi-override) -> `invalid_name`.
5. Длина в рунах 1..100, иначе `invalid_name` (пустое после trim тоже).

`Name.Key()` — ключ уникальности: `strings.ToLower` от NFC-формы после `cases.Fold` (`golang.org/x/text/cases`). Уникальность `(parent_id, name_key)` среди всех детей родителя, включая неактивных (EC-23, EC-25).

### 1.4 Ограничения размера

- `MaxChildren = 1000` прямых детей у узла (константа домена, `children_limit`, EC-26). Глубина ограничена типами (4).
- Общее число узлов не ограничено доменом.

## 2. Пользователи и акторы

`user.User{ID, Email, PasswordHash, Status(active|inactive), TokenVersion int64, CreatedAt}`.

- `user.ParseEmail(raw)`: trim, нижний регистр всей строки, длина 3..254, ровно один `@`, непустые части, без пробелов; полная RFC 5322 не нужна. Ошибка `invalid_email` (EC-110).
- Пароль: 12..128 байт UTF-8, иначе `invalid_password` (EC-111). Хэш argon2id делает адаптер `authn`, домен знает только тип `PasswordHash` (непрозрачная строка PHC-формата).
- `ActorID` = `user.ID` аутентифицированного пользователя. Пользователь без назначений может войти, но не видит ни одного узла.

## 3. Роли и права

### 3.1 Роли

| Роль | Ранг | Смысл |
|---|---|---|
| `viewer` | 1 | читает узлы, балансы, историю операций, настройки |
| `analyst` | 2 | + отчёты, аудит, список участников |
| `admin` | 3 | + управление узлами (кроме удаления), настройками, балансами, ролями до `admin` включительно |
| `owner` | 4 | + удаление узлов, назначение и снятие `owner` |

Назначение: `role.Assignment{UserID, NodeID, Role, GrantedBy, GrantedAt}`; не более одного назначения на пару (пользователь, узел). Повторное назначение той же роли — no-op (EC-40), другой — замена.

### 3.2 Наследование

Эффективная роль пользователя на узле N = максимум рангов назначений на N и на всех предках N (`path`). Назначения на потомках и соседях не дают прав на N (EC-30, EC-31). Если назначений на цепочке нет, N **не виден** (вне поддерева -> 404, Р-11).

Корни доступа актора (`AccessRoots`) = узлы с назначениями актора, у которых нет назначенного актору предка (верхние). Отображаются фронтом как корни дерева.

### 3.3 Права (permission) и матрица

`role.Permission` — enum. Матрица (✓ = есть), реализуется функцией `role.Allows(r Role, p Permission) bool` и проверяется таблично T-U-19:

| Permission | viewer | analyst | admin | owner |
|---|---|---|---|---|
| `node.read` | ✓ | ✓ | ✓ | ✓ |
| `settings.read` | ✓ | ✓ | ✓ | ✓ |
| `balance.read` | ✓ | ✓ | ✓ | ✓ |
| `operation.read` | ✓ | ✓ | ✓ | ✓ |
| `report.read` | | ✓ | ✓ | ✓ |
| `audit.read` | | ✓ | ✓ | ✓ |
| `member.read` | | ✓ | ✓ | ✓ |
| `node.create` | | | ✓ | ✓ |
| `node.rename` | | | ✓ | ✓ |
| `node.move` | | | ✓ | ✓ |
| `node.deactivate` (и activate) | | | ✓ | ✓ |
| `settings.write` | | | ✓ | ✓ |
| `balance.credit` | | | ✓ | ✓ |
| `balance.debit` | | | ✓ | ✓ |
| `balance.transfer` | | | ✓ | ✓ |
| `member.manage` | | | ✓ | ✓ |
| `node.delete` | | | | ✓ |

Дополнительные правила (функции домена `role.CanGrant(actorRole, target Role) bool`, `role.CanRevoke(actorRole, current Role) bool`):

- `member.manage` позволяет назначать и снимать роли с рангом ≤ ранга актора на этом узле; `owner` назначает и снимает только `owner` (EC-33).
- Создание узла требует `node.create` на родителе.
- Перенос узла N требует `node.move` на **текущем родителе** N и на **новом родителе** (EC-45): перенос меняет состав двух поддеревьев.
- Удаление узла N требует `node.delete` на **родителе** N (удаление меняет поддерево родителя; аудит `node.deleted` пишется на родителя, поэтому он должен быть виден актору). Корень удалить нельзя.
- Переименование, деактивация, настройки, участники, операции требуют права на самом узле.
- Перевод требует `balance.transfer` на узле-источнике и узле-получателе.
- Инвариант: у корня `platform` всегда есть хотя бы одно назначение `owner` активного пользователя (`last_root_owner`, EC-35, EC-36). Снятие последнего owner некорневого узла разрешено (EC-37).

## 4. Деньги, балансы, операции

### 4.1 `money.Amount`

- int64 минимальных единиц валюты инсталляции (`TB_CURRENCY`, только отображение; сумма в БД без валюты).
- `ParseAmount(s string) (Amount, error)` — только `^[0-9]{1,19}$` без ведущих нулей (кроме "0"), значение ≤ MaxInt64; иначе `invalid_amount` (EC-76).
- `Positive()` для сумм операций: `> 0`, иначе `amount_must_be_positive` (EC-75).
- `Add(b) (Amount, error)` -> `balance_overflow` при переполнении; `Sub(b) (Amount, error)` -> `insufficient_funds` при результате < 0 (EC-74, EC-77).
- Никакого float (T-ARCH-05).

### 4.2 Операции (ledger, Р-05)

`balance.Operation{ID, NodeID, Kind, Amount, BalanceAfter, TransferID *ID, CounterpartyNodeID *node.ID, Comment, ActorID, CreatedAt}`

| Kind | Влияние на баланс узла | Пара |
|---|---|---|
| `credit` | + amount | нет |
| `debit` | − amount | нет |
| `transfer_out` | − amount | `transfer_in` на получателе, общий `TransferID` |
| `transfer_in` | + amount | `transfer_out` |

- Записи неизменяемы (append-only); исправление ошибки — встречная операция.
- `Comment`: 0..200 рун, та же нормализация пробелов, что у имени, управляющие символы запрещены.
- `balance.Balance{NodeID, Amount, Version, UpdatedAt}` — агрегат; `Apply(kind, amount) (Balance, error)`.
- Инвариант INV-L1: для каждого узла `balances.amount = Σ(credit, transfer_in) − Σ(debit, transfer_out)` (EC-82).
- Инвариант INV-L2: `balances.amount >= 0` (домен + `CHECK`, EC-138).
- Лимит операции: эффективная настройка `max_operation_amount` (см. §5); превышение -> `amount_exceeds_limit`.
- Операции запрещены, если узел не effective active (`node_inactive`) или эффективная `operations_enabled = false` (`operations_disabled`, EC-81). Для перевода — проверка обоих узлов.

## 5. Настройки узла

Набор ключей фиксирован (`settings.Key`), неизвестный ключ -> `unknown_setting`.

| Ключ | Тип значения | Валидация | Правило наследования (эффективное значение) | Значение по умолчанию у корня |
|---|---|---|---|---|
| `display_timezone` | string IANA | `time.LoadLocation` успешен, не пустая, не `Local` | ближайшее переопределение вверх по пути | `UTC` |
| `operations_enabled` | bool | | логическое И по всей цепочке (предок `false` запрещает всем потомкам) | `true` |
| `max_operation_amount` | Amount (строка) | 1..MaxInt64 | минимум по всей цепочке | `100000000` (1 000 000.00) |

Хранится только явно заданное значение узла; удаление переопределения возвращает наследуемое. `settings.Resolve(chain []NodeSettings) Effective` — чистая функция, вход упорядочен от корня к узлу, в ответе для каждого ключа значение и `SourceNodeID`.

Почему разные правила: часовой пояс — предпочтение отображения (ближайший выигрывает), а разрешение операций и лимит — ограничения, которые потомок не может ослабить.

## 6. Аудит

`audit.Entry{ID, OccurredAt, ActorID, NodeID, Action, TargetType, TargetID, Before, After audit.Fields, RequestID}`.

Домен не знает JSON: `Before`/`After` — `audit.Fields` (упорядоченный срез `{Key, Value string}`), адаптер сериализует в jsonb. Действия (`audit.Action`):

`node.created`, `node.renamed`, `node.moved`, `node.child_moved_out`, `node.child_moved_in`, `node.deactivated`, `node.activated`, `node.deleted` (пишется на родителя), `member.granted`, `member.changed`, `member.revoked`, `user.created`, `settings.set`, `settings.unset`, `balance.credited`, `balance.debited`, `balance.transferred` (две записи: на источнике и получателе), `auth.password_changed`.

Правила:
- Запись в той же транзакции, что и изменение (EC-96); no-op изменения не пишутся (EC-101).
- Неизменяемость: нет UPDATE/DELETE для роли приложения (EC-97).
- Неуспешные попытки и отказы в доступе не пишутся (только лог, EC-99).
- Секреты (пароли, хэши, токены) не попадают в `Before/After` (EC-100).
- Перенос пишет три записи: `node.moved` на узле (`before.parent_id`, `after.parent_id`), `node.child_moved_out` на старом родителе (только `node_id` и `name`, без нового места: старая ветка не должна узнать идентификатор чужого узла), `node.child_moved_in` на новом родителе (`node_id`, `name`). EC-98.

## 7. Инварианты (сводка)

| ID | Инвариант | Где обеспечивается |
|---|---|---|
| INV-T1 | Ровно один узел `platform`, у него `parent_id IS NULL` | домен + partial unique index |
| INV-T2 | `nlevel(path) = level(type)` и `subpath(path,0,-1) = path родителя` | домен, CHECK (уровень), функция проверки `tb_tree_violations()` в тестах |
| INV-T3 | Тип родителя = `AllowedParent(type)` | домен + триггер BEFORE INSERT/UPDATE |
| INV-T4 | Имя уникально среди детей родителя по `name_key` | домен (сообщение) + unique index |
| INV-T5 | Детей ≤ 1000 | use case под структурной блокировкой |
| INV-A1 | Корень имеет ≥1 owner (активный пользователь) | use case с блокировкой назначений корня |
| INV-A2 | Одно назначение на (user, node) | unique |
| INV-L1 | Баланс = сумма журнала | транзакция + тест-инвариант |
| INV-L2 | Баланс ≥ 0 | домен + CHECK |
| INV-I1 | Один успешный результат на (user, Idempotency-Key) в пределах TTL | PK + та же транзакция |
| INV-D1 | Актор видит и меняет только узлы своих поддеревьев | use case + RLS |
| INV-AU1 | Каждое изменение состояния имеет запись аудита в той же транзакции | use case, тест на откат |
