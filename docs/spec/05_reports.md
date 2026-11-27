# 05. Отчёты и агрегаты

## 1. Состав

| Отчёт | Эндпоинт | Use case | Содержание |
|---|---|---|---|
| Сводка по поддереву | `GET /nodes/{id}/reports/summary` | UC-R1 `SubtreeSummary` | итоги и ряд по периодам за `[from, to)` по всем операциям узлов поддерева (включая сам узел) |
| Разбивка по детям | `GET /nodes/{id}/reports/children` | UC-R2 `ChildrenBreakdown` | для каждого прямого ребёнка: итоги по его поддереву за период и текущий баланс поддерева |

Показатели: `count` (число операций), `credits` (Σ `credit` + `transfer_in`), `debits` (Σ `debit` + `transfer_out`), `net = credits − debits`. Переводы внутри поддерева дают и `transfer_in`, и `transfer_out`, поэтому увеличивают обе суммы, а `net` не меняют; это явное правило, в UI подпись «transfers inside the subtree are counted on both sides».

Фильтр `kind` (повторяемый) ограничивает виды операций.

## 2. Входы и валидация (`Validate()`)

| Поле | Правило | Ошибка |
|---|---|---|
| `From`, `To` | даты `YYYY-MM-DD`, `From < To`, `To − From ≤ 366 дней` | `invalid_period` (EC-90) |
| `TZ` | IANA (`time.LoadLocation`), не `Local`; пусто -> эффективная `display_timezone` узла | `invalid_period` |
| `Granularity` | `day`, `week` (ISO, с понедельника), `month` | `invalid_period` |
| `Kinds` | подмножество четырёх видов | `validation_failed` |
| `Limit`, `Cursor` (UC-R2) | как общая пагинация | `validation_failed`, `invalid_cursor` |
| `IncludeInactive` (UC-R2) | bool, по умолчанию true | |

Границы: локальная полночь `From` в `TZ` до локальной полуночи `To` в `TZ`, полуинтервал. Перевод в UTC делает Postgres: `($from::timestamp AT TIME ZONE $tz)`. Периоды строятся в локальном времени, поэтому сутки перехода на летнее время имеют 23 часа, обратного — 25 (EC-89).

## 3. Запросы

Транзакция `REPEATABLE READ READ ONLY` (Р-16): все запросы отчёта видят один снимок, параллельный перенос или новая операция либо целиком видны, либо целиком нет (EC-85). Актор установлен в сессии, RLS действует.

### 3.1 Сводка (UC-R1)

```sql
WITH scope AS (SELECT path FROM nodes WHERE id = $1),
bounds AS (
  SELECT ($2::date::timestamp AT TIME ZONE $4) AS t_from,
         ($3::date::timestamp AT TIME ZONE $4) AS t_to
),
ops AS (
  SELECT date_trunc($5, o.created_at AT TIME ZONE $4) AS period_local,
         o.kind, o.amount
  FROM operations o
  JOIN nodes n ON n.id = o.node_id
  CROSS JOIN scope CROSS JOIN bounds
  WHERE n.path <@ scope.path
    AND o.created_at >= bounds.t_from AND o.created_at < bounds.t_to
    AND ($6::text[] IS NULL OR o.kind = ANY($6))
),
periods AS (
  SELECT generate_series(date_trunc($5, $2::date::timestamp),
                         $3::date::timestamp - interval '1 microsecond',
                         ('1 ' || $5)::interval) AS period_local
)
SELECT p.period_local,
       count(o.kind)                                                          AS cnt,
       coalesce(sum(o.amount) FILTER (WHERE o.kind IN ('credit','transfer_in')), 0)  AS credits,
       coalesce(sum(o.amount) FILTER (WHERE o.kind IN ('debit','transfer_out')), 0)  AS debits
FROM periods p LEFT JOIN ops o USING (period_local)
GROUP BY p.period_local ORDER BY p.period_local;
```

- `$5` берётся из белого списка `day|week|month` (не из пользовательской строки напрямую, EC-64). `date_trunc('week', ...)` даёт понедельник (ISO).
- `generate_series` заполняет пустые периоды нулями (EC-87).
- `sum(bigint)` в Postgres возвращает `numeric`: адаптер читает как `pgtype.Numeric` и передаёт в Output как `report.Sum` (строка десятичных цифр, может быть > int64, EC-91). В домене отчётов нет int64-сложения сумм.
- `periodStart` в ответе — локальная дата начала периода (`YYYY-MM-DD`), плюс `tz` в корне ответа.
- Итоги `totals` — сумма по ряду (вычисляется SQL отдельным агрегатом в том же снимке, не сложением в Go).

### 3.2 Разбивка по детям (UC-R2)

```sql
SELECT c.id, c.name, c.type, (c.status = 'active') AS own_active,
       count(o.id) AS cnt,
       coalesce(sum(o.amount) FILTER (WHERE o.kind IN ('credit','transfer_in')), 0) AS credits,
       coalesce(sum(o.amount) FILTER (WHERE o.kind IN ('debit','transfer_out')), 0) AS debits,
       (SELECT coalesce(sum(b.amount), 0) FROM balances b JOIN nodes d ON d.id = b.node_id
        WHERE d.path <@ c.path) AS balance
FROM nodes c
LEFT JOIN nodes d ON d.path <@ c.path
LEFT JOIN operations o ON o.node_id = d.id
     AND o.created_at >= $t_from AND o.created_at < $t_to
WHERE c.parent_id = $1
  AND ($include_inactive OR c.status = 'active')
  AND (c.name_key, c.id) > ($cursor_name_key, $cursor_id)
GROUP BY c.id
ORDER BY c.name_key, c.id
LIMIT $limit;
```

- `active` в ответе = эффективная активность ребёнка (собственный статус и статус scope-узла и его предков, последний вычисляется один раз через `tb_node_effective_active`). Деактивированные дети включены и помечены `active=false`; `includeInactive=false` исключает неактивных по собственному статусу (EC-86).
- Пагинация keyset по `(name_key, id)`, курсор привязан к scope, периоду, tz и фильтрам (EC-62, EC-93).
- Узел без детей -> пустая страница (EC-87).

## 4. Атрибуция и согласованность

- Операции относятся к узлам по **текущему** положению в дереве в момент снимка (Р-16): после переноса узла его прошлые операции считаются в новом предке и исчезают из отчётов старого (EC-95). Альтернатива (снимок пути в операции) отброшена: усложняет перенос и права на исторические данные; записано в README как известное ограничение.
- Разные страницы UC-R2 строятся в разных транзакциях: между страницами дерево может измениться. Это допустимо и задокументировано в OpenAPI (`description`): согласованность гарантирована в пределах одной страницы и одного запроса UC-R1.
- Удалённые узлы в отчёте отсутствуют (удалять можно только узлы без операций, так что суммы не теряются).

## 5. Права и изоляция

- UC-R1/UC-R2: Require(`report.read`, scope). Узел вне поддерева -> 404 (EC-51). Общий предок двух веток, на котором у актора нет роли, тоже 404.
- Пользователь с ролями в двух несвязанных ветках получает отчёт только по запрошенному scope, данные второй ветки не смешиваются (EC-32).
- RLS как вторая линия: даже если фильтр `path <@ scope.path` был бы опущен, `operations` и `nodes` вернули бы только строки поддеревьев актора (T-ISO-11).

## 6. Output

```go
type SubtreeSummaryOutput struct {
    ScopeNodeID node.ID
    TZ          string
    From, To    report.Date
    Totals      report.Totals            // Count int64; Credits, Debits, Net report.Sum
    Series      []report.PeriodTotals    // PeriodStart report.Date + Totals
}
type ChildrenBreakdownOutput struct {
    Items      []report.ChildTotals      // NodeID, Name, Type, Active, Totals, Balance report.Sum
    NextCursor string
}
```

`report.Sum` — значение-строка десятичных цифр со знаком (`net` может быть отрицательным), конструктор проверяет формат `^-?(0|[1-9][0-9]*)$`. В API все суммы отчётов — строки.

Примечание M4.5: суммы читаются из SQL как `numeric::text` и проверяются доменным `report.NewSum` (точность та же, кода меньше, чем с `pgtype.Numeric`).
