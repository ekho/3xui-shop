# M06b1 — локальная приёмка Telegram delivery

Дата: 2026-10-06. Владелец #60, контракт `2026-10-06-m06b1-telegram-delivery-v1`.
[Спецификация](../superpowers/specs/2026-10-06-m06b-telegram-delivery-design.md) ·
[Native-план](../superpowers/plans/2026-10-06-m06b-telegram-delivery.md) ·
[Общее решение](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6009126213).

## Ревизия и результат

Product revision `52ea333a3504ba59475b519eb4f553bf1956d8e8`. Полная матрица **22/22 PASS**, 416.361s.
Локальная приёмка завершена. Fresh Astra/high whole-branch review диапазона
6cc8d03..95301a9: **0 Critical/Important/Minor**. Exact-source PR CI, ручной merge
в v2 и preview publication пока pending. #60 OPEN/In progress:
после Telegram остаются email M06b2 → audit M06c → removal shared platform/store
M06d и собственная архитектурная приёмка всего M06.

| Проверка | Результат |
| --- | --- |
| Generation/compatibility | Go/web без drift; API, all15 migrations и dependencies равны свежей базе v2 6cc8d03. Naming/vet/types/test build/runtime config PASS. |
| Go/подключённые потребители | `RUN_BROWSER_TESTS=1 go -C backend test -race ./... -count=1`, реальные изолированные PostgreSQL/Redis: **14 пакетов с тестами PASS**, platform192.454s, connected consumers104.066s. |
| Web/Python | Playwright **127/127**, Python **105/105** PASS. Внешняя форма оплаты перехвачена до provider request. |
| Контейнеры | Compose config/build backend/gateway/bot и HTTPS/routing/secret-file/migration/restore smoke PASS. Один Go HTTP/River процесс; legacy runtime выключен в native profile. |
| Native | Реальная **3X-UI3.7.0**, TLS SMTP/HTTPS; stop/restart после commit сохраняет operation/grant/keys/readback; Bot API simulated, Telegram отключён. |
| Покупка/restore | Signed localhost receipt выдаёт доступ новому/триальному аккаунту, повтор неизменен; pending paid dump read-only restore в собственной БД, source restart выдаёт один native доступ. |
| Cleanup | Собственный native stack остановлен; secrets/dumps/full logs остаются в private ignored paths. |

## Проверенные границы

Notifications единолично владеет telegram_deliveries SQL и lease/complete.
Subscriptions вызывает Enqueue/Latest public caller-Tx ports; actual CardTx
предоставляет текущую карточку. Accounts сохраняет operator allowlist; active
TrialBridge обращается к owner без wire, root содержит только HTTP DTO/error
facades. Domain не импортирует root transport/store/private peers.

SQL boundary actual RED на root/subscriptions SQL → GREEN. Отрицательные fixtures:
SELECT/JOIN/UPDATE/INSERT/DELETE/quoted public. Absent-owner RED → новые real-PG
compatibility/composition GREEN. Старый pre-seeded wire raw union SHA256 с другим
key order/extra field replay через прямой owner и facade; rows/attempts/time/message
не меняются. Changed result/state/chat/token конфликтуют; malformed JSON/invalid
code/positive IDs проверяются с прежним id/token409 приоритетом. Expired completed
replay rejected; neutral sent/failed bytes, payload null/optional и jobs JSON равны
старым wire. Operator allowlist и empty nonnil claim также проверены.

Current rejected card/target last sent message/newest state идут через public Tx
ports. Enqueue видим внутри caller Tx и исчезает при rollback. Контролируемый
outbox INSERT failure откатывает trial/audit/idem целиком; card failure не оставляет
lease/attempt. Existing lease60/expiry/reclaim/concurrent/decision/reconsider/
operator/provision/HTTP/channel/lifecycle cases сохранены. SQL/DB clocks, IDs,
sequence/raw payload/lease/result hashes и atomicity не мигрируются.

Первая полная попытка остановилась на go-vet: два replay-only constructor calls
в subscriptions/contracts_test.go ещё имели старую сигнатуру. Они адаптированы
отдельным test commit, TestLegacy race GREEN; вся успешная22-stage матрица затем
повторена на одной новой ревизии. Неудачные records сохранены отдельно; PR/merge
при красной матрице не выполнялись.

## Границы приёмки

Email transport/proof/revocation extraction — M06b2, audit — M06c, shared root
removal — M06d; Python retirement — С47. Новые кампании/рассылки/topics/relay,
production/real provider/installed Happ/VPN/macOS trust не входят в перенос.
Local TLS SMTP/simulated Bot API не подтверждают внешнюю доставку. Browser tests
с API interception и real backend/HTTP evidence отдельны; новый full browser-to-
real-backend trial/support run не заявлен. Прежние third-party module-directive
предупреждения web build остаются; frontend/dependencies не менялись.

## Native rulings

Все решения ledger в порядке принятия, со стоимостью ошибки:

- Ruling: Autonomous documents/Native/manual merge and v2 preview already authorized by user/#55 decision; no repeated approval menu — avoids unrelated pause — cost if wrong: bounded work would need revision; no production authority inferred.
- Ruling: Reuse verified source-equal M06a/dev.23 baseline and healthy local test services; do not repeat baseline full matrix before changed code — exact-source proof exists — cost if wrong: stale prerequisite risk, live refs/health checked.
- Ruling: Split notifications into Telegram M06b1 then email M06b2 under OPEN #60 — distinct security/transport acceptance, not new feature scope — cost if wrong: extra bounded PR/review, no missing final ownership.
- Ruling: Carry raw result JSON across owner boundary and hash compacted raw, not typed normalized fields — persisted wire hashes retain replay semantics — cost if wrong: key-order legacy replay would conflict; independent preseed test covers it.
- Task 1: Ruling: include Operator prefix in final focused completion run — changed web-operator reconsider caller needs explicit exercise — cost if wrong: missed Tx propagation regression; full connected suite also follows.
- Task 2: Ruling: pass nil notifications only to the two replay-only legacy fixtures — cached trial/callback success must not touch outbox, matching their existing nil catalogue/VPN dependencies — cost if wrong: fixture would fail on accidental new outbox dependency; production composition always supplies owner.

## Final rulings после whole-branch review

Все семь declines reviewer разрешены координатором; продуктового fix pass нет.

- Final Ruling: Email/proof/revocation, audit and shared platform/store removal remain M06b2/M06c/M06d; #60 stays OPEN — separate accepted ownership boundaries require their own proof — cost if wrong: incomplete architecture could be reported complete; no such claim made.
- Final Ruling: Python retirement and new campaigns/broadcast/topics/relay remain their roadmap scenarios — current extraction preserves existing functionality — cost if wrong: those scenarios would remain missing; roadmap and #60 retain them.
- Final Ruling: Production, real provider/Telegram delivery, installed Happ/VPN and macOS trust remain excluded — user authorized local and v2 preview only — cost if wrong: local simulations could be mistaken for external acceptance; limits explicitly retained.
- Final Ruling: Browser interception and real backend/native evidence are separate; no new full browser-to-real-backend trial/support run claimed — matrix records prove those actual scopes — cost if wrong: a cross-surface gap would remain undetected; broader claim withheld.
- Final Ruling: Exact-source remote CI/manual merge/preview remain coordinator delivery gates; reviewer did not verify live GitHub/registry — local review does not prove publication — cost if wrong: unverified source could be merged; required gates execute before merge and acceptance.
- Final Ruling: Preserve inherited uncertain-send, lease expiry and repeated-delivery semantics — transfer does not redesign transport guarantees — cost if wrong: a repeated Telegram delivery may occur; no exactly-once promise added.
- Final Ruling: Normalize authored spec/plan EOF during final documentation update and add committed-range whitespace verification; inherited frontend directive warnings remain — cosmetic review observation is not a product defect — cost if wrong: cosmetic verification gap; product unchanged, no implementation fix pass or re-review.
