# С35 — автопродление Telegram Stars

Владелец [#33](https://github.com/ekho/3xui-shop/issues/33); контракт
`2026-10-08-s35-stars-recurring-v1`, комментарии6051821522/6052341410/6052964308/6053125079.
База `27ae2dec9aadfe03bada575a416d1f1afdf3c383`.
Задачи1–3 завершены; полный локальный набор задачи4 прошёл.
Итоговое ревью, exact-source CI, ручное слияние в v2 и предварительный релиз ожидаются.

## Проверки

| Проверка | Наблюдаемый результат |
| --- | --- |
| Playwright Stars | 31/31 PASS; первоначальный RED16 FAIL/15 PASS |
| Полный Playwright | 403/403 PASS в2.9m |
| Current native graph и recurring real browser | PASS13.865s; TLS3X-UI3.7.0, actual native period2592000 и cancel/resume |
| Полный Go/race с native/browser | 1210 тестов / 13 пакетов PASS, 0 FAIL / 0 individual SKIP; 12 пакетов без тестов |
| Python, включая настоящий Go-потребитель adapter | 110/110 PASS14.641s |
| go vet / TypeScript typecheck / build | PASS |
| Генерация Go/SQL/TypeScript | 39 outputs: повторная генерация идентична |
| git diff --check | PASS |

## Что проверяет реализация

Явный opt-in в первой30-дневной signed Mini покупке сохраняет старую разовую
покупку и её JSON/hash. Новый immutable receipt-root удерживает первоначальные
invoice/bot/payer/first charge; неожиданная дополнительная first charge хранится
для собственной отмены и не выдаёт второй доступ. Subsequent genuine payments
создают существующий renewal order с frozen quote и однократной выдачей; ребёнок
не создаёт invoice. Native event time сохраняет допустимость поздней доставки.

Native True — единственное подтверждение setter; resume True лишь разрешает
пользователю включить продление в Telegram. Lost reply/400/uncertain не открывают
другие методы. Required cancel от restriction/quarantine/VPNban/unlimited/plan/
starter превосходит устаревший resume. Оплаченный принятый cycle после клиентской
отмены сохраняется; поздняя операторская policy-смена закрывает его старую
цепочку через существующий AccessOwner.

Ранний refund сохраняет исходный финансовый root и разрешает фактический child
ledger. Pending/partial retirement оставляет immutable target/steps и не стирает
applied. Unknown legacy/reserved pre-checkout остаётся закрытым; весь известный
billing должен иметь native cancellation proof перед external/unlink. Проверены
пять external методов, Mini one-shot renewal/plan change, grace/lapse и15min timer.

Текущий весь Go graph/native poll/River работает с собственным TLS3X-UI3.7.0.
Первый/повторный/replayed charge, paid_until отдельно от VPN expiry, failed native
Update.subscription, отмена/restart/отдельная extra-first отмена и shutdown
подтверждены настоящими API/DB/panel facts. Real browser проходит default/opt-in
checkout и true/false управление; callback SDK остаётся лишь подсказкой до native
money. UUID/sub_id/panel target и оплаченный период сохраняются.

Общий ru/en UI показывает состояние, paid period, confirmation/retry и
resume_allowed. Контролы используют прежние auth/CSRF/idempotency и abort/session
защиты. Неизвестный/malformed ответ даёт ошибку; нет stale controls при отказе в
своём scope. Keyboard/focus/aria-live проверены. SDK canceled/failed/pending
различаются, invoice OpenAPI документирует Mini-only authority: обе Minor С34
исправлены в этом расширении.

## RED, диагностика и границы доказательства

- Задачи1–3 наблюдали настоящие отсутствующие recurring first/control/cycle
  операции. Paid-client-cancel и поздняя policy-смена отдельно RED→GREEN.
- UI первоначально16 FAIL/15 PASS; исправленный owning набор31/31 PASS.
  Первый промежуточный26/31 выявил неправильную confirmation-label и две
  fixture/locator ошибки; исправления сохранили прежние права и денежные guards.
- Native whole graph уже реализован задачами1–3; его первоначальный PASS не
  объявлен RED. Временное обнуление native period дало настоящий RED1.394s;
  исходный gateway восстановлен, PASS6.440s, product gateway diff0.
- Первоначальный native fixture использовал +1s после expiry, но ожидал непрерывное
  продление. Проверено прежнее max(now,previousExpiry); fixture поставлен точно на
  expiry, product правило сохранено.
- Первый полный Go выявил старое allowed_updates=3. Owning consumer теперь
  проверяет точные4 имена; offset/limit/timeout/redirect guards сохранены.
- Первый полный web402/403 выявил неизвестный404 в старом Mini logout fixture.
  Добавлен настоящий none DTO; private-key/session assertions сохранены.
- Первый Python запуск не передал file-backed TestKit environment. Исправленный
  запуск реально выполнил110 тестов; prerequisite failure не объявлен product RED.
- C01 с evidence path, начинающимся с точки, был INVALID_EVENT; исправленный
  artifact ref ALLOWED. C07 повторного Go запуска основан на изменённом consumer.
  Ни один malformed check не считается разрешением.

## Rulings I made

1. Task1: Ruling: boundary tests accept existing HTTP403 for signed external/unsupported action; catalogue amount uses a new immutable revision, migration sub_id uses16 valid lowercase alphanumeric bytes — match existing authority/provenance constraints, never weaken them — cost if wrong: a boundary test could miss status-only regressions.

2. Task1: Ruling: add owning native Telegram StarsRecurringInvoice test and HTTP payments_mapping to task1 files/checks — provider period and optional wire field are actual first-payment boundaries — cost if wrong: task1 verification spans two extra existing packages.

3. Task1: Ruling: task1 produces DTO/schema and gateway callback type; state reader/native control bodies execute in task2 with their real endpoint/control tests — avoid unused interim stubs — cost if wrong: intermediate task1 commit is not a standalone recurring-control release; the feature is delivered only after task4.

4. Task2: Ruling: Update.subscription At is observed time only and excluded from replay hash; multiple same payload/payer identities are ambiguous/unknown — native fields lack timestamp/charge, source6052341410 — cost if wrong: less precise provider status until manual resolution, never a false native cancellation proof.

5. Task2: Ruling: unresolved pre-checkout reservation blocks external billing/unlink until actual result, even after local expiry/cancel — SDK/time cannot prove absence of money, source6052341410 — cost if wrong: abandoned invoice may require support to clear proven non-payment.

6. Task2: Ruling: nil LegacyUserID new UUIDs use current complete owner history; legacy billing fixtures require actual legacy marker/native uncertainty — accepted C35 proof replaces C16 binding-only closure, source6052341410 — cost if wrong: C46 must preserve every real legacy marker before production cutover.

7. Task2: Ruling: separate owner read/lifecycle and native-control code into two existing-module files; include native GetUpdates owning check and api.go explicit route registration in task2 — keep provider I/O and read guards understandable, no new module/dependency/registry — cost if wrong: two extra source files to navigate.

8. Task2: Ruling: advance the scheduler clock through public owner reads and /healthz, not an expired Mini session; OpenAPI uses the repository supported nullable=true syntax — test real time/HTTP liveness and preserve generator compatibility — cost if wrong: session-expiry behavior remains covered by its existing identity tests.

9. Task2: Ruling: include current C16/C17 renewal/plan-change regressions in the whole-task check and preserve unknown legacy fixtures with their actual legacy marker — shared C35 eligibility now accepts new UUID Telegram bindings with complete own history — cost if wrong: negative legacy cases do not represent a new verified account, which is checked by the positive handoff test.

10. Task3: Ruling: pending one-shot Stars managing checkout also closes resume, shared6051821522 cycle-boundary clarification — avoids double collection while retaining method-specific owner checks — cost if wrong: customer must cancel or finish a pending Stars invoice before reenabling the old recurrence.

11. Task3: Ruling: child created_at uses trusted native money-event time and expires_at=event+30min; original quote/root remains immutable — existing funding windows then validate genuine delayed native cycles — cost if wrong: event time must stay available from the trusted Telegram adapter/import in C46.

12. Task3: Ruling: client cancel after an accepted valid cycle preserves that paid grant; a new owner policy cancellation upgrades a prior client intent, while historical native proofs stay immutable — paid-period preservation and later operator profile/source authority both matter, shared6053125079 — cost if wrong: one additional idempotent native cancellation may be needed after a policy change.

13. Task3: Ruling: late operator change is exercised after money receipt and before target preparation; existing AccessOwner refuses a new operator operation across unresolved prepared access — test actual allowed authority, never bypass it with fixture SQL — cost if wrong: final-write same-source protection also relies on existing ownership checks.

14. Task3: Ruling: update C34 signed HTTP negative renewal expectation from transport403 to owner409 when no applied source exists; external method remains403 and order count stays1 — C35 explicitly enables eligible Mini managing actions, positive path tested separately — cost if wrong: refusal status is now part of the current owner eligibility contract. Add stars_payment_test.go to task3 owning tests.

15. Task4: Ruling: native whole-graph test characterizes already implemented tasks1–3, so an initial PASS is not evidence of missing product behavior; use actual UI RED and a narrow native period mutation followed by restore to demonstrate the new native check — no manufactured missing-environment/fixture failure — cost if wrong: composed native behavior can only be as strong as its observed provider and panel assertions.

16. Task4: Ruling: reuse native_trial_integration_test.go owning transport/launch helpers to capture period, edits and the same second stdlib scheduler already used by main — no duplicate fake or production process — cost if wrong: existing native scenarios gain a lifecycle loop and need the final broad regression check.

17. Task4: Ruling: show own billing state in Mini, web cabinet with current Telegram binding or an owned Stars order, and Stars order pages; a payment/fulfillment/refund change refreshes it — discover captured billing after recovery while avoiding unrelated non-Telegram web reads — cost if wrong: a web status read failure for a recovered/unlinked identity needs explicit retry via the retained Stars order.

18. Task4: Ruling: control-confirmation back button uses an owning Stars label rather than catalogue cancelConfirmation (actually Back to plans); read-only period selection uses its semantic combobox name; none-subscription fixture mirrors fresh no-panel state — first UI GREEN26/31 exposed misleading label and two fixture locators/stale alerts, not money-policy defects — cost if wrong: labels are a separate translation key but no extra UI path.

19. Task4: Ruling: native browser recurring proof reuses the existing one-shot fixture with explicit recurring option and separate TestNativeStarsRecurringBrowser; focus regex must include both tests with RUN_BROWSER_TESTS=1 — keep C34 default one-shot proof intact — cost if wrong: browser-focused verification takes a second owned account, no new transport or process.

20. Task4: Ruling: include existing botapi TestPollingTransportContract in task4 checks/files; its old exact3-update expectation must include accepted native subscription update (now exact4 names), original offset/limit/timeout/redirect guards retained — whole Go run exposed this consumer after focused Stars tests passed — cost if wrong: native subscription events would be omitted; new whole graph also observes a real native failed update without changing paid period/access.

21. Task4: Ruling: include mini-app.spec.ts shared fixture with actual none Stars DTO; its unknown404 route inserted a second alert, making the existing strict logout assertion fail before the async logout completed — preserve the private-key/session assertions and model the new real reader — cost if wrong: mock Mini fixtures with no Stars do not cover legacy_unknown (owner and Stars tests do).

22. Task4: Ruling: task-done records T4 implementation/local verification before the same step5 final-review/delivery gates; step5 remains pending until exact CI/manual merge/release — the Native task loop requires a completed task before its final review, and source/local success is not delivery — cost if wrong: a task completion line could be misread as issue Done, which remains separately guarded.

## Итоговое ревью

Ещё не выполнено. Предусмотрен один fresh Astra/high reviewer всей ветки,
один author Critical/Important RED→GREEN pass при необходимости и no re-review.

## Границы приёмки

Только собственные fixtures, PostgreSQL/Redis/Docker и TLS3X-UI3.7.0.
Реальные деньги, публичный callback, живой Telegram, внешний SMTP и production
не проверялись. Happ/VPN и Mac trust не менялись. Старые Docker product images
не считаются проверкой текущего Go graph. Новые зависимости, процессы,
универсальный event registry или новый worker не добавлены.

С36/#34 владеет переходом во внешний браузер, С27/#37 — уведомлениями,
С45/#47 — runtime/deploy config, С46/#53 — реальным legacy import,
С47/#54 — cutover/restore и полным удалением Python main/support runtimes.
Локальный PASS не доказывает production; Issue/Project Done требует также
точной v2 доставки и согласованного предварительного релиза.
