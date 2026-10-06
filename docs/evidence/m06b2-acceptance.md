# М06b2 — локальная приёмка email delivery

Дата: 2026-10-06. Владелец [#60](https://github.com/ekho/3xui-shop/issues/60),
контракт `2026-10-06-m06b2-email-delivery-v1`.
[Спецификация](../superpowers/specs/2026-10-06-m06b-email-delivery-design.md) ·
[Native-план](../superpowers/plans/2026-10-06-m06b-email-delivery.md) ·
[Общее решение](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6009603617).

## Ревизия и результат

Product revision `1989231289a231dabf9eaa2ec41362b5cfb17c11`. После единственного review fix pass полная
матрица **22/22 PASS**, 417.546s. Fresh Astra/high review на d57dc8f: 1 Important I1,
0 Critical/Minor; I1 закрыт real-PG RED→GREEN и новой полной матрицей.
Delivery завершён [PR #69](https://github.com/ekho/3xui-shop/pull/69): exact-source
Platform37420656059/PR images37420656171 SUCCESS на b1a0b21f2fa7a092604ff0af9705c2b3f857a911.
Manual merge d23c848354ac663b953d9faa9b49b09d93e04303 имеет target729eea5/sourceb1a0b21
родителей и source-equal tree3a3d7c1e048f139d5f24217f9d281f66c73324f5.
Preview37421862377/all4jobs SUCCESS; [dev.27](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.27)
prerelease/non-draft, peeled tag ровно merge; три GHCR index/оба linux architectures
и revision/version/source labels проверены. [Shared checkpoint](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6010525855).
#60 остаётся OPEN/In progress до audit М06c → удаления shared platform/store
М06d и собственной архитектурной приёмки всего М06.

| Проверка | Результат |
| --- | --- |
| Generation/compatibility | Go/web без drift; API, все15 миграций и зависимости равны fresh v2 729eea5. Naming/vet/types/build/runtime config PASS. |
| Go/подключённые потребители | `RUN_BROWSER_TESTS=1 go -C backend test -race ./... -count=1`, реальные изолированные PostgreSQL/Redis: **14 пакетов PASS**, platform196.426s, connected consumers105.332s. |
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

## Единственный fix pass после final review

I1: новый central email guard выявил цикл batch account A → recipient C → account B
против CancelEmailChange(B): B → C. У mapped web accounts допускается общий pending
email; import не требует остановки всех writers. TestLegacyApprovalMailLockOrder
управляет реальными PostgreSQL row/advisory locks: reviewed product RED503 →
all mapped accounts first в прежнем UUID-порядке → focused race GREEN18.761s →
полная новая матрица22/22. Два snapshots и отзыв proofs/ciphertexts фиксируются
атомарно. Dry-run, replay, identity/protected guards сохранены; central revoke
защита SMTP остаётся. Нет нового SQL, миграции, зависимости или lock framework.

Первоначальная матрица на bdd9d40 (413.997s) сохранена как предыдущее доказательство;
после I1 она не используется для принятия изменённого product. Новая матрица
выполнена целиком на 1989231 после C07 ALLOWED. Второго reviewer нет по Native.

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

## Final rulings

Все решения после ревью, включая каждую Declined to judge, в порядке принятия:
Формулировки сохранены на момент решения; ожидавшиеся тогда remote gates завершены выше.

- Ruling: Accept I1 as Important and lock every existing mapped batch account in current UUID order before email guards — mapped web accounts and concurrent cancellation are supported, writers shutdown is not the import contract — cost if wrong: broader row hold during the bounded import; no new lock manager/schema/dependency, one real-PG RED→GREEN fix pass required.
- Ruling: Remote CI/manual merge/tag and multiarch preview remain pending coordinator gates — local review does not prove remote delivery — cost if wrong: unverified source could merge; exact-source gates still required.
- Ruling: M06b1 PR68/dev25 statements are dated delivery evidence already verified by coordinator, not new remote verification by reviewer — its source-equal v2 merge is this accepted baseline — cost if wrong: historical links could be stale; next-phase actual source/parents/preview get fresh checks.
- Ruling: Production and real payment/Telegram requests stay excluded — user authorized local and v2 preview — cost if wrong: simulations could be mistaken for external acceptance; limits remain explicit.
- Ruling: Installed Happ/VPN/macOS trust stay excluded — user prohibited switching live Happ — cost if wrong: local native panel proof could be overstated; no installed-surface claim.
- Ruling: External SMTP delivery/domain reputation/spam filtering remain external acceptance — local TLS proves transport only — cost if wrong: mail could fail externally; no external deliverability claim.
- Ruling: Preserve inherited uncertain SMTP send/commit and possible retry — owner transfer does not redesign delivery protocol — cost if wrong: duplicate email may occur; no exactly-once promise.
- Ruling: Audit/shared platform-store removal/Python retirement remain M06c/M06d/C47 — email extraction is a bounded part of OPEN M06 — cost if wrong: incomplete architecture could be marked complete; #60 remains OPEN.
- Ruling: post_restore_auth only runs with ingress closed and writers stopped — exact reviewed operational exception and unchanged procedure — cost if wrong: runtime restore could race; inherited mandatory stop contract and real restore checks retained.
- Ruling: Browser interception and real backend/native evidence stay distinct; no new complete browser-to-real-backend trial/support proof — actual matrix has those separate scopes — cost if wrong: cross-surface gap could remain; broader acceptance not claimed.
