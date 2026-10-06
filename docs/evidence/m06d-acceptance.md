# М06d — приёмка сборки модульного монолита

Дата: 2026-10-06. Владелец [#60](https://github.com/ekho/3xui-shop/issues/60), контракт `2026-10-06-m06d-composition-v1`.
[Спецификация](../superpowers/specs/2026-10-06-m06d-composition-design.md) · [Native-план](../superpowers/plans/2026-10-06-m06d-composition.md) · [Общий контракт](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6011584601).

## Ревизия и результат

Product revision `f051f1225c4292911b43146a7ff9be1fda4ff418`. Полная матрица **22/22 PASS**, 518.386s на одной committed revision: Go **13 пакетов PASS** с race/connected consumers, Python **105/105**, Playwright **127/127**. Все **154/154** перенесённых регрессий сохранены и прошли собственный Task2 и полный прогон. Собственный native stack остановлен; API, все15 миграций и зависимости не изменены, генерация стабильна.
Единственный fresh whole-branch review Astra/high на d796e3a..81592a8: Critical0/Important0/Minor0, Ready from code review; fix pass/re-review не требуются. Exact-source CI, ручное слияние в v2 и preview ожидают проверки. #60 OPEN/In progress до завершения D08; локальная приёмка не означает доставку или запуск production.

| Этап | Seconds | Result |
| --- | ---: | --- |
| names | 0.191 | PASS |
| go-generate | 0.854 | PASS |
| web-generate | 0.623 | PASS |
| generated-drift | 0.022 | PASS |
| compatibility | 0.304 | PASS |
| go-vet | 0.458 | PASS |
| web-types | 1.970 | PASS |
| web-build | 2.059 | PASS |
| runtime-config | 0.083 | PASS |
| go-race-connected | 297.256 | PASS |
| python | 20.980 | PASS |
| playwright | 78.167 | PASS |
| compose-config | 0.242 | PASS |
| compose-build | 17.666 | PASS |
| smoke | 23.321 | PASS |
| native-up | 8.441 | PASS |
| native-check | 45.650 | PASS |
| purchase-prepare | 0.157 | PASS |
| purchase-overlay | 0.783 | PASS |
| purchase-check | 5.344 | PASS |
| purchase-restore | 12.650 | PASS |
| native-down | 1.165 | PASS |

## Собственная архитектурная приёмка всего М06

| Критерий | Фактическая проверка |
| --- | --- |
| D01 | Actual cmd/server dependency graph исключает platform/store; AST app запрещает wire, SQL и общие доменные методы. Modules содержит только9 public owner refs; negative import/facade/SQL fixtures проходят. |
| D02 | Сохранены старые env/file-conflict/default/TLS/proxy/trial/timezone/payment predicates и управляемые часы. Группы конфигурации связывают operator IDs/panel ID/origin с единым владельцем; production cfg immutable. Telegram disabled работает без token. |
| D03 | Actual HTTP/session/security/operator/support/trial/payment checks и перенесённые regressions сохраняют статусы, cookies, cursors, nullable/false/empty arrays, большие ID и безопасные ошибки. Transport projections остаются в httpapi. |
| D04 | TrialBridge получает только subscriptions/notifications; CLI и River используют public owners. Native outage/stop/restart/replay, purchase repeat и paid restore проверяют одну действующую сборку. |
| D05 | Компилируются154 новых TestRegression имён, каждое соответствует сохранённой baseline-функции. Сравнение полного преобразованного исходника сохраняет assertions и failure cases; audit13 nullable/TX/52-row, mail MaxConns1/revocation/account-order проверки входят в green suite. |
| D06 | internal/platform, internal/store, db/queries и root sqlc block отсутствуют. Все module import/SQL boundaries green; shared source не переименован в новый all-domain facade. API/all15 migrations/dependency files равны fresh v2 d796e3a. |
| D07 | Все22 проверки одной product revision: actual локальная 3X-UI3.7.0/TLS, подписанный callback/repeat и восстановление оплаченного состояния; teardown выполнен. М06а/b1/b2/c уже доставлены, их текущие владельцы и новая сборка проходят собственную общую приёмку. |
| D08 | Fresh review без замечаний завершён; exact-source CI/manual merge/preview остаются OPEN. |

Runtime: app.NewModules собирает accounts/catalogue/subscriptions/vpn/payments/support/notifications/mail delivery/audit reports. HTTP вызывает нужного владельца и делает только wire/error conversion; Telegram bridge не имеет доступа к общей бизнес-фасаде. Config.LoadConfig/Validate переехали в app, namespace `platform`, defaults3/15/1/UTC и legacy bearer true сохранены. Общие старые значения связываются в app, независимые настройки не добавлены.

## RED/GREEN и сохранение проверок

Task1 actual runtime RED2.934s (5PScZO) обнаружил root platform/store dependencies и app imports. Guard получил отрицательные fixtures переименованной фасады/SQL/wire; GREEN3.032s (XvBsEx). Task1 committed real PG/Redis race: cmd2.849s, app4.344s, HTTP20.614s, connected47.170s. Перенесены9 composition test files из app в httpapi, включая lifecycle: actual transport now imports app, старое размещение создаёт import cycle. Все прежние assertion paths сохранены, общий fixture вызывает app.NewModules.

Task2 actual removal RED1.995s (MSFS4v) зафиксировал три каталога и root sqlc block до удаления. Все исходные28 test files сохранены до миграции;27 files с154 Test функциями перенесены, persisted-row structs независимы от private module stores. Initial compile выявил2 неиспользуемых imports, отдельно исправлено распознавание package identifiers. Структурный GREEN2.269s (vZHIZf).

Первый настоящий154 run не прошёл две исходные проверки: после перехода cfg к pointer test snapshot больше не копировался; изменение fixture.queue не отключало queue, захваченную constructor. Исправлена только подготовка этих случаев: копия/восстановление config value и реальная сборка с nil queue/тем же clock. Все original assertions сохранены; отдельный focused GREEN13.218s (kDTJmR). Final Task2 actual154 race: app4.007s, HTTP246.530s; full22 повторно проверяет их в полном приложении. Product behavior ради test migration не менялось. Выявленные compile/fixture ошибки не выдаются за функциональные product RED.

## Границы

Python-бот остаётся до С47; legacy HTTP и штатные job kinds/JSON/IDs/keys сохранены. Промокоды/рефералы остаются последним функциональным этапом Р7; campaigns/reports/retention/mirror принадлежат открытым сценариям. Production, реальные Telegram/provider/переводы, установленный Happ/VPN/macOS trust исключены. Внешняя SMTP-доставляемость и benchmark целевого сервера остаются отдельными prerequisites; локальный TLS SMTP не подтверждает доставляемость, inherited uncertain-send retry не переписывается.
Существующие browser checks и реальные connected/native проверки покрывают свои границы отдельно; новый browser-to-real-backend full trial/support flow не заявлен. Новые пользовательские функции в М06d не добавлялись.

## Native rulings

Все ledger решения в порядке принятия, включая стоимость ошибки:

- Ruling: Preserve accepted autonomous documents/Native/manual v2 merge/preview — #55 mandate and #60 contract cover necessary stages; coordinator implements, one fresh reviewer at end — cost if wrong: bounded accepted design needs revision, no production or external provider authority inferred.
- Ruling: Reuse delivered M06c PR70/dev29 source-equal current v2 baseline without an identical full rerun — live parents/tree/tag/3public indexes+6configs and exact-source CI proved the base; all future source changes receive own proof — cost if wrong: stale environment could invalidate a dependent test; owned prerequisites are checked before costly execution.
- Ruling: Keep three coupled Native tasks in one bounded cleanup plan — runtime switch, old regression migration/deletion and final acceptance have concrete shared interfaces; no implementation delegation or separate framework — cost if wrong: a larger cohesive diff increases integration risk, actual stage tests and fresh whole-branch review cover it.
- Ruling: Bind operator IDs/panel ID/origin from one agreed owner group at app composition — old shared values must not drift into separate independent settings; mutable test callbacks preserve previous semantics and production config remains immutable — cost if wrong: future config work could misread precedence, exact bindings are canonical and retained configuration/HTTP/native tests must pass.
- Task1: Ruling: Move the eight composed transport/owner test files from app to httpapi without losing assertions — the HTTP consumer now imports app; keeping wire aggregation tests inside package app would create an import cycle, and the transport owns wire projections — cost if wrong: ownership of future composition checks may be less obvious; filenames retain composition and the single app.NewModules fixture constructs actual owners, no copied domain rules.
- Task2: Ruling: Restore the two original fixture conditions through the real composition contract — cfg is now a shared pointer, so its snapshot copies/restores the value; the missing queue case constructs app.NewModules with nil instead of mutating a detached test field — cost if wrong: accidental fixture changes could hide behavior regressions; all original assertions remain byte-equivalent after explicit transformation and focused then all154 checks use actual owners. Failed first154 run is retained privately as task-2-fixture-fail.log; no production behavior change is needed.

Примечание к Task1 ruling: lifecycle стал девятым перенесённым файлом, под тем же решением о транспортном размещении; degraded/drain assertions сохранены. Промежуточные product push отложены до общего проверенного результата единого этапа завершения сборки; canonical contract docs опубликованы до реализации, финальный source обязан пройти exact-source CI.

## Final rulings

Все шесть Declined to judge оценены координатором по фактическим границам. Findings и deferred minors отсутствуют; все254 прежних Go Test entrypoints сохранены, две новые архитектурные проверки дают256.

- Final: Ruling: Exact-source CI/manual v2 merge/parents/tree/preview/images remain coordinator delivery gates — code review approval does not deliver the product; live workflow and accepted advance authority were read, every final source and automatic prerelease effect must be verified — cost if wrong: an unverified artifact could be called delivered; #60 stays OPEN until actual proofs.
- Final: Ruling: Current GitHub/Project state and whole-M06 closure are coordinator-owned — live #60 is OPEN, M06c PR70/dev29 source-equal d796 base and canonical decisions were checked; own D01–D07 pass and D08 will gate closure — cost if wrong: stale history or child completion could hide missing parent acceptance; re-read exact remote state before Done.
- Final: Ruling: Production/real provider/Telegram/Happ/VPN/macOS trust remain excluded — clients receive only the stated local controlled evidence; no new external test or cutover is inferred from autonomous development — cost if wrong: external readiness still requires resources and exact-target authority; no production readiness claim.
- Final: Ruling: External SMTP delivery/target performance and inherited uncertain-send/panel-retry limits remain explicit prerequisites — preserved config and local TLS/3X-UI checks do not prove delivery or stronger retry guarantees; no behavior weakened or new retry activated — cost if wrong: outside acceptance may expose existing limits; external launch remains gated.
- Final: Ruling: No new browser-to-real-backend full trial/support result is claimed — unchanged API/frontend and existing browser plus actual HTTP/connected/native proofs cover their separate boundaries for this ownership cleanup — cost if wrong: a gap between boundaries may need a dedicated future surface proof; mandatory full22 and preserved assertions still pass.
- Final: Ruling: Future campaigns/reports/retention/bonuses/config/data/Python cutover remain later scenarios/C45/C46/C47 — M06 removes the transition facade while keeping current behavior, legacy HTTP/Python and open task responsibility; new features are not marked Done — cost if wrong: later read/config/migration contracts need separate design and acceptance; roadmap coverage remains open.

Ревью проверило весь diff108 файлов, сравнило28 saved originals с Git baseline и27 migrated files с явным преобразованием; все154 checks и assertion flow сохранены. Оно самостоятельно повторило read-only API/all15 migrations/deps/product-equivalence/whitespace проверки и прочитало все22 оригинальных лога. Полную матрицу и generation повторно не запускало; CI/merge/release/image delivery проверяет координатор.
