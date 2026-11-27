# 06. Фронтенд

React 19 + TypeScript 6.0 strict, Vite 8, TanStack Router (file routes) и Query, React Hook Form + Zod, Tailwind CSS 4, Vitest + Testing Library + MSW, Playwright. Версии — `00_overview.md` §3.3.

Принцип: **клиент не источник истины.** Права с сервера (`GET /me/permissions`) используются только для того, чтобы скрыть недоступные кнопки. Любое действие может вернуть 401/403/404/409/422, и UI обязан это корректно показать.

## 1. Конфигурация качества

- `tsconfig.json`: `strict: true`, `noUncheckedIndexedAccess: true`, `exactOptionalPropertyTypes: true`, `noImplicitOverride: true`, `noFallthroughCasesInSwitch: true`, `verbatimModuleSyntax: true`, `moduleResolution: "bundler"`.
- ESLint (flat config): `typescript-eslint` `strictTypeChecked` + `stylisticTypeChecked`; правила-ошибки: `@typescript-eslint/no-explicit-any`, `no-unsafe-*`, `no-floating-promises`, `no-non-null-assertion`, `react-hooks/rules-of-hooks`, `react-hooks/exhaustive-deps`. `// eslint-disable` запрещён правилом `eslint-comments/no-use` или проверкой `grep` в `make lint-web` (T-ARCH-04, EC-137).
- Генерация: `npm run gen` = `openapi-typescript ../api/openapi.yaml -o src/api/schema.d.ts`; файл коммитится, `make gen-check` следит за актуальностью.

## 2. Структура и маршруты

```
src/
  main.tsx                     # QueryClient, RouterProvider, AuthProvider
  api/schema.d.ts              # сгенерировано
  api/client.ts                # createClient<paths>() + middleware: Bearer, refresh, X-Request-Id
  api/problem.ts               # разбор application/problem+json -> ApiProblem
  auth/session.ts              # access token в памяти (модульная переменная + подписка), не localStorage
  lib/money.ts                 # parseMoneyInput, formatMinor (BigInt)
  lib/idempotency.ts           # crypto.randomUUID() на попытку формы
  routes/
    __root.tsx                 # layout: шапка, сайдбар-дерево, <Outlet/>
    login.tsx                  # /login?redirect=
    index.tsx                  # / -> редирект на первый корень доступа
    nodes.$nodeId.tsx          # layout карточки, табы
    nodes.$nodeId.index.tsx    # обзор: данные узла, баланс, кнопки действий
    nodes.$nodeId.settings.tsx
    nodes.$nodeId.members.tsx
    nodes.$nodeId.operations.tsx   # история с фильтрами в search params
    nodes.$nodeId.reports.tsx
    nodes.$nodeId.audit.tsx
  features/<feature>/          # хуки useQuery/useMutation, формы, компоненты
  components/ui/               # Button, Input, Select, Field, Dialog (нативный <dialog>), Table, Alert, EmptyState
```

Маршруты под `/nodes/*` защищены `beforeLoad`: нет сессии -> попытка refresh -> иначе `redirect({ to: '/login', search: { redirect: location.href } })`.

Search params валидируются Zod-схемой в `validateSearch` маршрута; невалидные значения отбрасываются к значениям по умолчанию (EC-123).

## 3. Экраны

| Экран | Маршрут | Данные (query keys) | Действия | Права для показа кнопок |
|---|---|---|---|---|
| Логин | `/login` | — | вход | — |
| Дерево (сайдбар) | все `/nodes/*` | `['me']`, `['children', nodeId, filters]` | раскрытие узла (ленивая подгрузка детей, «Load more» по `nextCursor`), фильтр active/inactive | — |
| Обзор узла | `/nodes/$nodeId` | `['node', id]`, `['balance', id]`, `['perms', id]` | создать ребёнка, переименовать, перенести, деактивировать/активировать, удалить, начислить, списать, перевести | `node.create`, `node.rename`, `node.move`, `node.deactivate`, `node.delete` (на родителе: показ по `perms` родителя), `balance.*` |
| Настройки | `/nodes/$nodeId/settings` | `['settings', id]` | задать / сбросить переопределение | `settings.write` |
| Участники | `/nodes/$nodeId/members` | `['members', id, inherited]` | добавить по email, сменить роль, снять | `member.manage`; в селекте ролей только роли ≤ своей |
| История операций | `/nodes/$nodeId/operations` | `['operations', scope, filters]` (infinite query) | фильтры: узел поддерева, вид, период | — |
| Отчёт | `/nodes/$nodeId/reports` | `['report-summary', ...]`, `['report-children', ...]` | период, tz, гранулярность | `report.read` (иначе таб скрыт) |
| Аудит | `/nodes/$nodeId/audit` | `['audit', scope, filters]` (infinite) | фильтры: действие, период | `audit.read` |

Перенос: диалог с поиском по `GET /nodes/{id}/move-targets` (только допустимые родители, EC-122), подтверждение. Без drag-and-drop.

Формы (React Hook Form + `zodResolver`): создание узла (`type` из допустимых детей, `name` 1..100 после trim), переименование, операция (сумма, комментарий ≤ 200), перевод (получатель — поиск по поддереву через `GET /nodes/{id}/children` рекурсивно не делаем: поле ввода id получателя с проверкой `GET /nodes/{id}` и показом имени), участник (email, роль, пароль для нового пользователя опционально), смена пароля (в меню пользователя).

## 4. Работа с деньгами (EC-121)

- `parseMoneyInput(input: string, scale = 2): Result<string, MoneyError>`: регэксп `^\d{1,17}(\.\d{1,2})?$`, конкатенация целой и дополненной дробной части как строк, результат — строка минимальных единиц без ведущих нулей; без `Number`/`parseFloat`. Больше двух знаков после точки -> ошибка `too_many_decimals`; результат > `9223372036854775807` (сравнение через `BigInt`) -> `too_large`.
- `formatMinor(minor: string, scale = 2, locale = 'en')`: `BigInt` -> целая и дробная часть -> `Intl.NumberFormat` только для целой части группировки (строкой через `BigInt.prototype.toLocaleString`).
- Валюта отображается кодом из `GET /nodes/{id}/balance` (`currency`).
- Масштаб 2 знака — допущение UI для EUR/USD-подобных валют; записано в README.

## 5. Сессия и запросы

- Access token хранится только в памяти; refresh — httpOnly cookie, JS её не видит (EC-124). Перезагрузка страницы -> `POST /auth/refresh` при старте.
- Middleware клиента: на 401 `token_expired`/`token_invalid` выполняет **один** refresh, параллельные запросы ждут тот же промис (single-flight), затем повторяют запрос один раз; refresh не удался -> очистить сессию и кэш Query, перейти на `/login?redirect=...` (EC-119).
- Мутации баланса: `Idempotency-Key` создаётся при открытии формы/диалога (`lib/idempotency.ts`) и переиспользуется при повторной отправке той же попытки (сетевой сбой, двойной клик); кнопка блокируется на время запроса; после успеха ключ сбрасывается (EC-120).
- После успешной мутации: инвалидация `['node', id]`, `['balance', id]`, `['children', parentId]`, `['operations', ...]`, `['audit', ...]`; оптимистичных обновлений нет (проще и честнее для данных с серверными правилами).

## 6. Обработка ошибок на клиенте

| Ответ | Поведение |
|---|---|
| 400/422 с `errors[]` | ошибки полей в форму через `setError(field)`; общий `detail` над формой |
| 403 `forbidden` / `role_not_grantable` | Alert «You do not have permission for this action»; кэш не меняется, повторно запрашиваются `['perms', id]` (EC-117) |
| 404 на маршруте узла | экран «Not found or no access» со ссылкой на корень доступа; `['me']` и дерево перезапрашиваются (EC-118) |
| 409 (`name_taken`, `insufficient_funds`, ...) | сообщение по коду из словаря `problemMessages`, неизвестный код -> `detail` |
| 429 | сообщение с `Retry-After` |
| 503/сеть | Alert с кнопкой «Retry»; для мутации баланса повтор с тем же ключом |

Имена узлов выводятся как текст (React экранирует), длинные имена усекаются CSS `truncate` с `title` (EC-125). `dangerouslySetInnerHTML` запрещён (ESLint `react/no-danger` или grep в `make lint-web`).

## 7. Тест-сценарии фронта

Vitest + Testing Library + MSW (`src/**/*.test.ts(x)`, название теста начинается с `[T-W-NN]`):

| ID | Что проверяет | EC |
|---|---|---|
| T-W-01 | кнопка скрыта без права; при 403 от сервера показан Alert, данные не изменены | EC-117 |
| T-W-02 | 404 на `/nodes/$id` -> экран «Not found or no access», refetch `me` | EC-118 |
| T-W-03 | два параллельных запроса с 401 -> ровно один `POST /auth/refresh`, оба повторены; refresh 401 -> редирект на `/login?redirect=` | EC-119 |
| T-W-04 | двойной клик «Debit» -> один запрос; повтор после сетевой ошибки с тем же `Idempotency-Key` | EC-120 |
| T-W-05 | `parseMoneyInput`/`formatMinor`: "10.50"->"1050", "10.505" ошибка, "0.1"->"10", max int64, без float (property-тест на 1000 случайных строк через `BigInt`) | EC-121 |
| T-W-06 | диалог переноса показывает только move-targets; 422/409 -> сообщение, дерево перезапрошено | EC-122 |
| T-W-07 | фильтры истории в URL: переход назад восстанавливает; мусор в search params -> значения по умолчанию | EC-123 |
| T-W-08 | после логина токен не появляется в `localStorage`/`sessionStorage`/`document.cookie` | EC-124 |
| T-W-09 | имя из 100 символов с RTL/комбинирующими символами отображается текстом, есть `title` | EC-125 |

Playwright e2e (`web/e2e/*.spec.ts`, название `[T-E-NN] ...`) — `08_testing.md` §3.6.

Примечание M5.1: `lint-web` и `typecheck` отдельные цели; правила слоёв фронтенда: `api` не импортирует React, `lib` чистые, `components` не ходят за данными, `features` не импортируют друг друга. Родитель корня доступа в дереве берётся из `breadcrumbs`, а не из `parentId`, который у корня доступа равен `null`.
