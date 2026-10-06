# M06a — локальная приёмка support

Дата: 2026-10-06. Владелец #60, контракт `2026-10-06-m06a-support-v1`.
[Спецификация](../superpowers/specs/2026-10-06-m06a-support-design.md) ·
[Native-план](../superpowers/plans/2026-10-06-m06a-support.md) ·
[Общее решение и зависимости](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6008661999).

## Ревизия и результат

Product revision: `65b215802b78a371b3bb518a0cf7994ec017b38e`. Полная матрица **22/22 PASS**,
суммарно 412.600 секунд выполнения проверок. Локальная приёмка support
завершена; fresh whole-branch review, exact-source PR CI, manual merge и preview
publication пока pending. #60 остаётся OPEN/In progress до notifications,
audit и удаления общего platform.Service/store с собственной приёмкой M06.

| Проверка | Результат |
| --- | --- |
| Generation/compatibility | Go/web generation без drift; HTTP API, migrations1–15 и dependencies равны базе v2 b5d8eb3. Semantic naming, Go vet, TypeScript, test build/runtime config PASS. |
| Go и подключённые потребители | `RUN_BROWSER_TESTS=1 go -C backend test -race ./... -count=1`: изолированные реальные PostgreSQL/Redis, **14 пакетов с тестами PASS**; platform188.522s, connected consumers102.212s. |
| Web/Python | Playwright **127/127** и Python **105/105** PASS. Внешняя форма оплаты перехвачена до provider request. |
| Контейнеры | Compose config/build backend/gateway/bot и HTTPS/routing/secret-file/migration/restore smoke PASS. Native profile запускает Go HTTP/River; legacy bot/reconcile не работают. |
| Native | Настоящая **3X-UI3.7.0**, TLS SMTP и кабинет HTTPS; restart после commit сохраняет operation/grant/keys. Bot API simulated, Telegram выключен. |
| Покупка и restore | Existing purchase prepare/check/restore: signed localhost receipt выдаёт доступ новому/триальному аккаунту, повтор не меняет срок/выдачу; pending paid dump восстановлен read-only в отдельной собственной БД, source restart выдаёт один native доступ. |
| Cleanup | Собственный native stack остановлен; secrets/dumps/full logs только в private ignored paths. |

## Проверенные границы

С05 теперь принадлежит support: conversation/message SQL, сообщения, страницы,
private attachments, ack/open/closed и независимый support-ban. Accounts authority
вызывается через публичные Lookup/Lock/RequireOperator/LockOperatorPair; private
SQL/store находятся внутри owner. App создаёт support, карточка оператора читает
Conversation через owner; platform содержит только временные DTO/error facades.
Domain не импортирует platform/wire/httpapi/Echo/app или private peers.

`TestSupportSQLBoundary`: actual RED на старом root SQL/generated store → перенос
→ GREEN. Отрицательные fixtures закрывают SELECT/UPDATE/INSERT/DELETE/quoted public.
`TestSupportPersistedCompatibility`/`TestSupportComposition`: absent-owner RED →
GREEN; старые field order/tags/nullable attachment/nil и empty arrays, pre-seeded
old anonymous input SHA-256 и wire result по-прежнему replay после support-ban.
Changed body конфликтует; повтор не добавляет message/audit и не меняет timestamp.

Через production composition проверены пустая/непустая operator conversation,
private attachment/recipient ack, ban отдельно от VPN и отзыв роли. Контролируемый
отказ audit INSERT откатывает conversation/message/audit/idem целиком.
Существующие blocked-write revoke/ban, concurrent duplicate/paging, квоты10/50MiB,
30messages/15min, Redis outage fail-closed, NUL/UTF-8, timestamp и HTTP/browser
проверки сохранены. Rate namespace/Lua, locks, IDs/sequence, API, миграции и
зависимости прежние. Audit writer пока остаётся в caller Tx до M06c.

## Границы приёмки

Новые Telegram topics/relay С37, рассылки/отчёты, bonus/campaign features не входят
в этот перенос. Общий platform/store удаляется после notifications/audit, Python
retirement — С47. Production, реальные деньги/provider settings, installed Happ,
VPN/trust macOS не проверялись и не менялись. Local TLS SMTP не подтверждает
доставку внешней почтовой инфраструктуры. Прежнее third-party предупреждение о
module directives остаётся; web build успешен, frontend/dependencies не менялись.

## Native rulings

Все решения ledger в порядке принятия, со стоимостью ошибки:

- Ruling: Continue autonomous specs/plans/Native/manual merges and v2 previews — accepted decision at #55 and covering #60; no implementer delegation for Native — cost if wrong: bounded worktree/doc rework, production excluded.
- Ruling: Reuse the verified source-equal M05 baseline instead of repeating a full suite at branch setup — fresh base b5d8eb3 equals source b2815af verified product dcfd61e, 22/22 and exact-source CI/preview passed; new commits only Spec/Plan — cost if wrong: stale baseline could hide a failure; clean tree/product comparison verified before edits.
- Ruling: Keep support audit INSERT in caller Tx until M06c — accepted sequential owner transfer preserves atomic message/audit/idem — cost if wrong: later port adaptation, no partial audit/message commit.
- Task 1: Ruling: Narrow the boundary RED expectation to SQL literals/queries — existing AST checker scans SQL, not operator method calls; removing root query and composition/card tests prove that caller's migration — cost if wrong: raw caller bypass could survive, covered by query deletion/compile and actual operator card check.
