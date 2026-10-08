# С35 — автопродление Telegram Stars

Владелец [#33](https://github.com/ekho/3xui-shop/issues/33); контракт
`2026-10-08-s35-stars-recurring-v1`, комментарии6051821522/6052341410/6052964308/6053125079.
База `27ae2dec9aadfe03bada575a416d1f1afdf3c383`.
Задачи1–4 завершены; один fresh Astra/high reviewer проверил всю ветку
`27ae2de..2247474`. Две Important исправлены единственным author RED→GREEN
проходом в `537e6c25809ee0a7c123daa5e5d594840513f182`.
Полный локальный Go/race после исправлений: 1213 PASS / 1 FAIL в неизменённой
проверке поддержки; отдельный диагностический запуск этого теста PASS.
Author pass ожидает полный зелёный exact-source CI; ручное слияние в v2 и
предварительный релиз тоже ожидаются. Итог доставки фиксируется в #33.
Первый exact-source CI на `40e723e` завершился ошибкой: cumulative20m timeout
HTTP-пакета и missing update в fake TLS panel при genuine cycle. Исправлены
только тестовые helpers; продуктовые исходники после `537e6c2` не менялись.
Новый полный локальный набор с тем же fake TLS transport запущен; PASS пока
не заявляется, следующий exact-source CI требуется на исправленных inputs.

## Проверки

| Проверка | Наблюдаемый результат |
| --- | --- |
| Playwright Stars | 31/31 PASS; первоначальный RED16 FAIL/15 PASS |
| Полный Playwright | 403/403 PASS в2.9m |
| Current native graph и recurring real browser | PASS13.865s; TLS3X-UI3.7.0, actual native period2592000 и cancel/resume |
| Полный Go/race до final fixes, source2247474 | 1210 тестов / 13 пакетов PASS, 0 FAIL / 0 individual SKIP; 12 пакетов без тестов |
| Полный Go/race после final fixes, source537e6c2 | 1213 PASS / 1 FAIL: TestRegressionSupportQuotaAndRate; 12 пакетов PASS / 1 FAIL; 0 individual SKIP, 12 пакетов без тестов |
| Current native/browser после final fixes | пакет ./tests PASS163.534s с RUN_BROWSER_TESTS=1, TLS3X-UI3.7.0 |
| Отдельная диагностика SupportQuotaAndRate/race | PASS4.090s; причина локального полного сбоя не установлена |
| Python, включая настоящий Go-потребитель adapter | 110/110 PASS14.641s |
| go vet / TypeScript typecheck / build | PASS |
| Генерация Go/SQL/TypeScript | 39 outputs: повторная генерация идентична |
| Owning author-fix regressions | RED2.690s → focused GREEN18.459s |
| Первый exact-source CI, 40e723e | Platform37744667538 FAIL: cumulative20m timeout и native fake-panel child; preview37744667497 три images PASS |
| Native fake TLS cycle + panel identity/readback | RED18.483s + boundary RED1.094s → GREEN6.234s, без изменений access/money owners |
| Six resume guards / read-only fixture schema | race PASS11.713s; профиль attributed GetSwagger в regression helper0.65s →0.07s, полный набор ещё pending |
| git diff --check рабочей копии | PASS; committed-range имеет Minor M1 ниже |

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
- Final I1 воспроизведён настоящим applied set_profile regular→euru: GET
  предлагал resume, HTTP200/native false позволяли старый cycle и возврат regular.
  Теперь текущий профиль сверяется с frozen quote в resume и cycle admission;
  обычная клиентская отмена/возобновление проходят прежние проверки.
- Final I2 воспроизведён conflicting replay первого и последующего applied charge:
  исходный proof/access сохранялись, но cancel intent отсутствовал. Теперь
  required cancellation записывается в той же review transaction для затронутого
  и исходного receipt account; повтор конфликта не создаёт лишний control,
  exact replay не отменяет billing. Reconciliation использует captured first charge.
- C07 final retry сначала получил INVALID_EVENT из-за неподдерживаемого outcome
  passed; исправленный event с фактическим I1/I2 RED и новым source diff ALLOWED.
  Один полный Go/race выполнен для изменённого кода. Web/Python/generator источники
  после успешных полных проверок задачи4 не изменялись.
- Полный post-fix запуск exit1 из-за TestRegressionSupportQuotaAndRate
  (quota setup SERVICE_UNAVAILABLE); остальные1213 тестов прошли. Support owner
  и этот тест не изменены от базы. Одна bounded диагностика с race и теми же
  ресурсами PASS4.090s; это не объявляется полным зелёным локальным набором.
  Required whole green suite будет доказан exact-source CI до слияния;
  speculative support fix и неизменённый повтор18-minute run не выполняются.

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

23. Final: Ruling: I1 is Important; compare the actual finite profile to the immutable recurring quote at resume and subsequent-cycle admission — a real operator profile change must not be undone by renewed billing, while ordinary client cancel/resume keeps its original profile — cost if wrong: an operator profile change back to the exact original profile can make the old contract eligible again; unresolved policy/source/refund checks still apply.

24. Final: Ruling: I2 is Important; record required cancellation in the existing conflicting-receipt transaction for the affected account and retained original receipt account — future billing cannot remain enabled after money review blocks automatic fulfillment; exact replay remains unchanged — cost if wrong: a conflicting cross-account payload conservatively cancels billing for both affected accounts, requiring support to resolve the retained financial conflict.

25. Final: Ruling: declined C36/#34 external browser/login handoff remains the next separate scenario — this issue delivers billing proof guards, while clients get the currently available cabinet/manual login path until C36 adds the explicit handoff — cost if wrong: discovering alternative payment still takes extra navigation before C36.

26. Final: Ruling: declined C27/#37 lapse notification delivery remains with its notification owner — current state and timer preserve actual paid/grace/lapse behavior but do not promise proactive notices — cost if wrong: a client can miss expiry until notification implementation; paid access is not extended by a missing notice.

27. Final: Ruling: declined C45/#47 deploy/runtime configuration and real resources remain separate — current single-process composition and owned TLS fixtures prove local behavior only, with no production authority — cost if wrong: real deployment configuration can still prevent provider/SMTP delivery and needs its own acceptance.

28. Final: Ruling: declined C46/#53 legacy import/decoder remains separate — actual legacy markers and unknown native identities stay fail-closed; C35 does not reinterpret the existing unsupported-payment runtime contract — cost if wrong: imported accounts need accurate complete history before any external billing or resume opens.

29. Final: Ruling: declined C47/#54 cutover/restore and Python removal remain separate — the current Go feature runs locally without claiming the final migration or live Happ acceptance; switching live Happ is excluded by the user — cost if wrong: final production cutover still requires migration/rollback evidence and removal of the legacy entry points.

30. Final: Ruling: declined real provider money/Telegram/production checks remain excluded from reviewer and local acceptance; release artifacts will be independently verified during authorized delivery — owned fake transport and TLS3X-UI do not prove actual provider behavior — cost if wrong: native external-service behavior can still differ and requires future explicitly authorized external acceptance.

31. Final: Ruling: retain the actual post-fix full local failure TestRegressionSupportQuotaAndRate (SERVICE_UNAVAILABLE), unchanged support source and single isolated race PASS4.090s; require one exact-source full green CI before the author pass/issue delivery is complete — no speculative support edit or unchanged eighteen-minute local repeat; the failure cause is unknown and local PASS is not claimed — cost if wrong: the local environment failure can recur, and CI must remain a merge gate rather than hiding it.

32. Final: Ruling: repair the owned native fake panel's missing existing update endpoint, with a direct identity-preserving PanelClient boundary RED-GREEN check and the unchanged native cycle test — CI/no-Docker test reaches child update while the locally tested real TLS3X-UI3.7.0 supports it; no provider/product money or access code changes — cost if wrong: the fake panel could still differ from real3X-UI, so both transports remain required acceptance inputs.

33. Final: Ruling: reuse the read-only compiled API schema through stdlib sync.OnceValues in existing regression/composition test helpers only — the exact CI timeout was cumulative at a fresh loader, and the six-case CPU profile measured repeated GetSwagger23.89%/New16.37% before counting GC/race overhead; production New/validation and every money/security assertion remain unchanged — cost if wrong: future tests that mutate a schema must use an independent instance; the whole race suite and actual negative-input checks must remain green, and reduced runtime is measured rather than assumed.

34. Final: Ruling: the single author pass remains open while its full suite is red; bounded CI test-fixture repairs are part of that same verification boundary, without a second product fix pass or re-review — the mandatory full-green rule cannot be fulfilled by hiding timeout/missing fake protocol or rerunning unchanged inputs — cost if wrong: a fixture repair could conceal a product defect; retain the unchanged real-panel/native proof, actual owner tests and exact-source CI gate.

## Исправление тестовых inputs после CI

[CI37744667538](https://github.com/ekho/3xui-shop/actions/runs/37744667538)
исчерпал global20m budget во время нового loader в HTTP fixture. Это cumulative
package timeout; текущий `starter-applied` тест исполнялся1s, stack находился
в JSON parser. Зависание этого отдельного теста не установлено.
В том же run `TestNativeStarsRecurring` не получил applied child: existing fake
panel отвечала405 на настоящий `/panel/api/clients/update/...`. Owned no-Docker
race запуск воспроизвёл этот сбой18.483s; direct TLS PanelClient test дал RED1.094s.
Update fixture теперь сохраняет email/UUID/subID и неизвестные Client поля,
отклоняет три чужие identity substitutions и подтверждает актуальные limits/expiry.
Этот check, прежний traffic check и неизменённый native recurring test GREEN6.234s.

HTTP CPUprofile шести resume guard cases: GetSwagger23.89% cumulative CPU,
API New16.37%; доля именно regression helper7.19% (0.65s). Shared stdlib once
в двух read-only test helpers снизил attributed helper loader до0.07s/0.94%.
Six-case race PASS11.713s, before14.518s; величина ускорения всего набора пока
не известна. Production New/compiled model/validation не изменены. Не добавлены
timeout increase, новые зависимости, process или второй product fix pass.

Первый C07 diagnostic event оказался INVALID_EVENT: отсутствовал обязательный
nullable field, затем обнаружены prose вместо reference IDs. Два bounded owned
diagnostics были начаты до корректировки event — это workflow deviation;
malformed check не объявляется разрешением задним числом. После чтения полной
schema corrected event ALLOWED; changed-input full-run event отдельно ALLOWED.
Полный локальный Go/race fake TLS + RUN_BROWSER_TESTS=1 выполняется;
новый точный CI и author-pass completion остаются обязательными до merge.
Предыдущий local SupportQuotaAndRate failure сохраняется с неизвестной причиной.

## Итоговое ревью

Единственное fresh-context whole-branch review: Astra/high, диапазон
`27ae2dec9aadfe03bada575a416d1f1afdf3c383..2247474eb9715d780f5adf8dd45013136c5c824d`.
Вердикт With fixes: 0 Critical, 2 Important, 1 Minor. Все пять literal Review
Focus проверены по actual SQL/callers/assertions; отдельный reviewer focused run
не имеет подтверждённого exit/output и не объявлен PASS. Повторного ревью нет.

I1/I2 regraded Important по пользовательскому эффекту и исправлены автором
через TestStarsRecurringProfileAuthority и TestStarsRecurringConflictCancellation
(first/cycle), настоящий RED→GREEN. Оплаченный applied доступ, original receipt,
профиль оператора и idempotency сохранены; обычные cancel/resume и cycle/policy
regressions входят в focused GREEN18.459s. После fixes полный local Go/race
содержит один описанный сбой поддержки; полный зелёный exact-source CI —
обязательный оставшийся gate author pass. No re-review.

## Deferred minors

- M1: дополнительная пустая строка в конце migration00029; `git diff --check`
  всего committed range возвращает exit2. Рабочая копия PASS не является этой
  проверкой. Функционального эффекта нет; Minor не входит в author fix pass.

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

## Фактическая доставка С35

[PR87](https://github.com/ekho/3xui-shop/pull/87), source390eb97f833f705d1d74d7670586b5ea6d4b90b1 → actual merge0d19b02669a90c335a7ac62d5062de4715687706. Два родителя и merged tree проверены; exact-source PR Platform37748760067 и PR Preview37748760072 COMPLETE SUCCESS. Go13 tested packages, web403/Python110, все generated/static/container/native-process TLS3X-UI3.7.0/TLS SMTP/restart/cleanup PASS. Individual Go CI count не выводился.

[V2 prerelease2.0.0-dev.71](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.71), run37752856394/all4jobs PASS; annotated tag→exactmerge,3OCI indexes amd64/arm64 и6config revision/version/source labels/aliases проверены. [#33closed/ProjectDone](https://github.com/ekho/3xui-shop/issues/33#issuecomment-6056418435),5DoD checked. Full34Rulings/1Minor/review/logs/hashes архивированы до удаления только SDD этого плана. Ранее failed local/CI checks выше остаются историческими фактами; нынешняя полная проверка1215PASS, прежняя support причина неизвестна.

Один свежий review/один author fix pass, no re-review/CI rerun. M1 committed-range whitespace exit2 остаётся deferred; clean working-tree check не подменяет его. Production/real money/live Happ исключены. Этот итоговый документальный checkpoint включён в следующую ветку С36; C35 product source не меняется.
