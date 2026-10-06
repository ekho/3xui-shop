# M05 — локальная приёмка payments

Дата: 2026-10-06. Владелец #59, контракт `2026-10-06-m05-payments-v1`.
[Спецификация](../superpowers/specs/2026-10-06-m05-payments-design.md) ·
[Native-план](../superpowers/plans/2026-10-06-m05-payments.md) ·
[Общее решение и зависимости](https://github.com/ekho/3xui-shop/issues/59#issuecomment-6008100859).

## Ревизия и результат

Final product revision: `dcfd61edb2de571c53a65ddb1779e6a2a8c7a538`. Полная матрица **22/22 PASS**,
суммарно 405.912 секунд выполнения проверок. Native Tasks1–2 выполнены;
локальная приёмка завершена. Свежий whole-branch review Astra/high диапазона
`c57c5e7..d6bd512`: **0 Critical / 0 Important / 0 Minor**, Ready to merge по коду.
Reviewer отдельно подтвердил boundary/signature checks, полный matrix не повторял.
Exact-source CI source `b2815af182ce81763a2bc585ecfbc6ef834e960b`:
[Platform checks](https://github.com/ekho/3xui-shop/actions/runs/37406740703) и
[V2 PR images](https://github.com/ekho/3xui-shop/actions/runs/37406740784) SUCCESS.
[PR #66](https://github.com/ekho/3xui-shop/pull/66) вручную слит в `v2`:
merge `b5d8eb315b02c23ee7a132d9845edbd759b3117b`, parents `c57c5e7`/`b2815af`,
source-equal tree `64d148e9c8da795e4682e2b2965809763189558d` проверены.
[Preview run](https://github.com/ekho/3xui-shop/actions/runs/37407798946) SUCCESS;
[2.0.0-dev.21](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.21) —
prerelease, tag указывает на этот merge. Три GHCR images, linux/amd64+arm64
и revision/version labels проверены. [#59 CLOSED/Project Done](https://github.com/ekho/3xui-shop/issues/59#issuecomment-6008608774).
Это доставка в v2 с предварительным релизом; production не менялся.

| Проверка | Результат |
| --- | --- |
| Generation/compatibility | Go/web generation без drift; HTTP API, migrations1–15 и dependencies равны базе v2 c57c5e7. Semantic naming, Go vet, TypeScript, test build/runtime config PASS. |
| Go и подключённые потребители | `RUN_BROWSER_TESTS=1 go -C backend test -race ./... -count=1`: реальные изолированные PostgreSQL/Redis, **14 пакетов с тестами PASS**; platform186.567s, connected consumers99.080s. |
| Web/Python | Playwright **127/127** и Python **105/105** PASS. Внешняя форма оплаты перехвачена до provider request. |
| Контейнеры | Compose config/build backend/gateway/bot и HTTPS/routing/secret-file/migration/restore smoke PASS. Native profile запускает Go HTTP/River; legacy bot/reconcile не работают. |
| Native | Настоящая **3X-UI3.7.0**, TLS SMTP и кабинет HTTPS; restart после commit сохраняет operation/grant/keys. Bot API simulated, Telegram выключен. |
| Покупка | Existing `deploy/purchase/local.py prepare/check`: signed localhost receipt выдаёт доступ новому/триальному аккаунту, quote и identities сохраняются, повтор не меняет срок/выдачу. |
| Paid restore | Existing `deploy/purchase/local.py restore`: pending paid dump восстановлен в отдельную собственную БД без запуска restored workers; source restart выдаёт один native доступ. |
| Cleanup | Собственный native stack остановлен; secrets/dumps/full logs находятся только в private ignored paths. |

## Проверенные границы

Orders/receipts, signature/amount/label, funding и preparation worker теперь
принадлежат payments. Accounts/catalogue/vpn читаются через public owners;
Money SQL вне payments запрещён существующим AST/SQL boundary checker.
Domain не импортирует platform/wire/httpapi/Echo или private peers.
Production App связывает PurchaseHooks напрямую с payments, server регистрирует
payments.PurchaseWorker. Platform содержит временные DTO/error facades до М06.

`TestPaymentsSQLBoundary`: actual RED на SQL в platform → owner extraction →
GREEN. `TestPaymentsPersistedCompatibility` и `TestPaymentsComposition`:
сначала absent-owner RED; после переноса проверены прежние wire/neutral JSON,
pre-seeded old order SHA-256/quote и operator reconcile result, включая disabled
method replay. Receipt+funding+один job сохраняются, повтор не дублирует job;
nil queue откатывает все money changes. Check/outcome видят caller Tx и
не выходят из его rollback. Существующие С10 preparation/account change/lost
physical owner, trial identity, missing hooks, outcome rollback, receipt dispute,
actor revoke/requeue и disabled-unexpired checkout tests проходят через facades.

Persisted kind `purchase_fulfillment`, args `order_id`, queue `provision`,
worker timeout/retry прежние. Requested MaxAttempts=1000000 сохранён;
существующий River0.48.0 driver хранит 32767 (int16), это прежнее поведение.
Official signature vector отдельно проверяет owner implementation и testkit
fixture signer; runtime не импортирует testkit. Schema и новые payment rules
не добавлены. Shared audit writer временно остаётся в caller Tx до М06.

## Границы приёмки

Production, настоящий перевод, provider callback URL/кошелёк, installed Happ,
VPN/trust macOS не проверялись и не менялись. Внешняя С13, дополнительные
провайдеры/renewal/Stars/refunds и Python retirement С47 — отдельные сценарии.
Local TLS SMTP не подтверждает доставку внешней почтовой инфраструктуры.
В web build сохраняется прежнее third-party предупреждение о module directives;
сборка успешна, frontend/dependencies не менялись.

## Native rulings

Все решения из ledger в порядке принятия, со стоимостью ошибки:

- Ruling: Continue specs/plans/Native/manual merge without renewed approval — user accepted autonomous remaining documents and sequential MR execution/merges, plus v2 preview publication; production excluded — cost if wrong: rework of bounded M05 docs/code, no production mutation.
- Ruling: Keep shared audit writer in caller Tx until M06 — owner issue explicitly separates later audit extraction; preserves existing atomic reconcile — cost if wrong: one later audit port adaptation, not money-rule duplication.
- Ruling: Nullable accounts Snapshot compares values and presence — owner DTO returns new pointers on each lookup; addresses do not describe account changes — cost if wrong: false revalidation rejection or unsafe acceptance, covered by existing real-PG preparation/trial tests.
- Ruling: Retain only existing root money facades during M05 — HTTP/tests need them until M06 adapter move; production hooks call payments directly — cost if wrong: temporary adapter rework in M06, no duplicated payment rules.
- Task 1: Ruling: Preserve requested MaxAttempts=1000000 and assert actual persisted 32767 — River v0.48.0 driver clamps to math.MaxInt16 (river_pgx_v5_driver.go:443/517), observed real-PG value; prior C10 behavior unchanged — cost if wrong: retry horizon differs; new limits are a separate decision, not this owner extraction.

- Final: Ruling: Preserve the existing River requested/persisted retry limits — users keep the prior recovery horizon; driver clamp 32767 is documented and changing retry policy belongs to a separate decision — cost if wrong: recovery horizon may be misunderstood, not a new M05 data loss.
- Final: Ruling: Keep temporary HTTP facades and caller-Tx audit writer until M06 — current callers retain their API and atomic audit; money rules already have one owner — cost if wrong: later adapter removal work, no duplicated money rules.
- Final: Ruling: Accept the bounded local proof without real provider/production/Happ/external mail checks — users receive unchanged C10 behavior; those environments are explicitly excluded from M05 and are not claimed as verified — cost if wrong: external integration defects remain for their own acceptance.
- Final: Ruling: Require fresh GitHub CI/release/image verification before merge and closure — independent reviewer did not inspect remote state; coordinator will verify the exact source and published merge — cost if wrong: stale remote evidence could incorrectly close delivery; closure stays blocked until live proof.
