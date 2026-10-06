# М06b2 — локальная приёмка email delivery

Дата: 2026-10-06. Владелец [#60](https://github.com/ekho/3xui-shop/issues/60),
контракт `2026-10-06-m06b2-email-delivery-v1`.
[Спецификация](../superpowers/specs/2026-10-06-m06b-email-delivery-design.md) ·
[Native-план](../superpowers/plans/2026-10-06-m06b-email-delivery.md) ·
[Общее решение](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6009603617).

## Ревизия и результат

Product revision `bdd9d40faf3127070d6da40732ebbcb27b695885`. Полная матрица **22/22 PASS**, 413.997s.
Локальные проверки завершены; fresh whole-branch review, exact-source CI,
manual v2 merge и preview/tag/three multiarch images ещё ожидаются.
#60 остаётся OPEN/In progress до audit М06c → удаления shared platform/store
М06d и собственной архитектурной приёмки всего М06.

| Проверка | Результат |
| --- | --- |
| Generation/compatibility | Go/web без drift; API, все15 миграций и зависимости равны fresh v2 729eea5. Naming/vet/types/build/runtime config PASS. |
| Go/подключённые потребители | `RUN_BROWSER_TESTS=1 go -C backend test -race ./... -count=1`, реальные изолированные PostgreSQL/Redis: **14 пакетов PASS**, platform192.217s, connected consumers102.662s. |
| Web/Python | Playwright **127/127**, Python **105/105** PASS. Форма оплаты перехвачена до provider request. |
| Контейнеры | Compose config/build backend/gateway/bot, HTTPS/routing/secret-file/migrate/restore smoke PASS. |
| Native | Реальная **3X-UI3.7.0**, TLS SMTP/HTTPS; process stop/restart после commit сохраняет operation/grant/keys/readback; Bot API simulated, Telegram отключён. |
| Покупка/restore | Signed localhost receipt выдаёт один доступ новому/триальному аккаунту; повтор неизменен. Pending paid dump read-only restore в своей БД; source restart выдаёт один native доступ. |
| Cleanup | Собственный native stack остановлен. Secrets/dumps/full logs остаются в private ignored paths. |

## Проверенные границы и совместимость

Notifications владеет mail_deliveries SQL, AES-GCM, прежними ru/en templates,
TLS SMTP и MailWorker. Accounts сохраняет регистрацию/credential proofs,
лимиты, validation/revoke и email guard. App связывает две узкие function hooks;
циклических импортов и foreign SQL в runtime нет. Root остаётся временным
DTO/error/SendSMTP фасадом до М06d; server и реальные integration consumers
регистрируют notifications.MailWorker с конкретным owner.

SQL boundary actual RED2.927s на accounts mail SQL → GREEN с отрицательными
SELECT/JOIN/UPDATE/INSERT/DELETE/quoted public fixtures. Сохраняется только
точное прежнее эксплуатационное исключение post_restore_auth.sql при закрытом
ingress/остановленных writers/mail workers; процедура не менялась и restore
проверен. Real TLS DATA hold actual RED3.761s (одна открытая SQL Tx) → GREEN:
во время SMTP нет SQL Tx или account row lock. Email session guard остаётся;
password/reset/cancel/restriction mutations ждут окончания отправки. Central
revokeCredentialProofs также защищает operator restriction и legacy import.
Cancellation освобождает session guard и сохраняет ciphertext; pool MaxConns1
завершает отправку на одной dedicated connection.

Независимо pre-seeded старые AES-GCM nonce-prefix/AAD UUID/uppercase payload
и River delivery_id/mail_delivery/MaxAttempts5 доставляют прежний link/code.
Прямой owner и facade replay не меняют row/time и не отправляют повторно.
Missing delivery — no-op; bad ciphertext возвращает503 без очистки. Существующие
unknown/expired/revoked/confirmed/version-changed proof, mail-kind isolation,
TLS failure/retry, password/email/restriction/legacy/migration cases сохранены.
Caller-Tx enqueue виден внутри Tx и исчезает при rollback; River INSERT failure
не фиксирует замену proof. Mail/proof/jobs/UUIDs/schema/API не мигрировались.

Focused real PG/Redis race passed перед commit и повторно через task-done;
затем все22 шага прошли на одной committed revision. Первая полная попытка
остановилась на vet: два MailWorker test consumers были за пределами прежнего
internal-only inventory. Whole-backend rg нашёл оба; registrations адаптированы,
TLS/restart/HTTP race GREEN9.351s, C06/C07 ALLOWED, полная матрица повторена
на новой ревизии. Неудачные records сохранены отдельно; native containers
в первой попытке не запускались.

Начальный branch guard ошибочно ожидал upstream origin/v2 у новой ветки.
Push не выполнялся до диагностики empty upstream и корректного explicit
push -u в собственную SSH ref. Первый C06 event был invalid из-за relative
.superpowers evidence; file URI исправлен, ALLOWED получен до push/contract
write. Это не отказ в разрешении. Product edits до принятого контракта не было.

## Границы приёмки

Audit/shared platform-store extraction — следующие части М06. Новые кампании,
рассылки и reports/retention — собственные сценарии. Python retirement — С47.
Production, реальные provider/Telegram requests, установленный Happ/VPN/macOS
trust исключены. Local TLS SMTP не подтверждает внешнюю доставляемость.
Inherited uncertain-send/commit failure допускает повтор SMTP; exactly-once
не обещается. Browser interception и real backend/native consumer evidence
разделены; новый full browser-to-real-backend trial/support flow не заявлен.
Прежние third-party module-directive warnings web build сохранены.

## Native rulings

Все решения ledger в порядке принятия, со стоимостью ошибки:

- Ruling: Autonomous documents/Native/manual merge/v2 preview already authorized by user/#55 decision — no repeated approval menu — cost if wrong: bounded implementation would need revision; no production authority inferred.
- Ruling: Reuse fresh source-equal M06b1/dev.25 + healthy loopback PG/Redis/dependencies, no baseline full-suite rerun — exact product proof exists — cost if wrong: stale prerequisite risk, refs/files/health checked.
- Ruling: Concrete MailService next to Telegram Service with two narrow function hooks, not generic channel framework — distinct SMTP proof/guard flow and import cycle require a mail owner — cost if wrong: extra concrete type; no speculative abstraction or dependency.
- Ruling: Central revokeCredentialProofs guard protects restriction and legacy callers too — dropping old proof row lock during SMTP otherwise changes safety — cost if wrong: revoked credential email could be sent concurrently; restriction/credential races cover it.
- Ruling: Reuse existing VPN session-lock cleanup with same-connection work and close-on-unlock-error — SMTP must stay outside SQL Tx without pooled lock leak/MaxConns1 deadlock — cost if wrong: pooled lock/resource leak, cancellation/single-connection checks required.
- Ruling: Initial new branch accepts observed no-upstream only for explicit SSH push -u to its own named remote ref — existing-branch upstream rule does not describe first push — cost if wrong: wrong destination; branch/head/exact SSH and resulting upstream guarded.
- Ruling: Adapt catalogue/vpn qualified accounts.New test callers too — actual global caller inventory found them beyond plan initial files — cost if wrong: other modules would fail compile; coherent constructor transfer requires these five test-only callsites. Scope/API unchanged.
- Ruling: Retain exact reviewed post_restore_auth.sql exception for notifications alongside accounts — existing S02 restore procedure clears credential ciphertext with all writers/workers stopped; owner extraction does not change operational SQL — cost if wrong: checker could hide runtime foreign SQL; only this exact maintenance file is exempt, module scans and restore checks remain. Focused FA3qKJ behavior GREEN exposed this previously account-only exception; minimal matcher correction, no restore behavior change.
- Task 2: Ruling: adapt both actual backend/tests mail-worker registrations to notifications.MailWorker with svc.MailDelivery() — first full vet RED8EIT79 found the test consumer outside the earlier internal-only inventory; whole-backend rg found exactly native_trial and web_trial — cost if wrong: real trial/restart test runtime would not compile or would omit mail; focused compile then full connected/native matrix required. Full first attempt stopped before native containers; six failed-attempt records retained in verification-failed-vet.json.
