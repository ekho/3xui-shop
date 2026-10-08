# С35: Stars с автопродлением — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task (accepted Native method). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Подтверждённые повторные Stars-платежи продлевают доступ один раз; клиент может доказанно отменить автосписание и безопасно сменить способ оплаты.

**Architecture:** Владелец payments расширяет существующие immutable checkout/receipt/refund и renewal/access операции. Telegram остаётся проверенным транспортом, root связывает typed callbacks; один процесс выполняет HTTP/River/native poll и 15-минутный stdlib timer.

**Tech Stack:** Go, PostgreSQL/goose/pgx, Redis, River, Echo, React/React-admin, существующие Playwright/oapi-codegen/sqlc; 3X-UI3.7.0.

**Spec:** `docs/superpowers/specs/2026-10-08-s35-stars-recurring-design.md`.
Owner [#33/6051821522](https://github.com/ekho/3xui-shop/issues/33#issuecomment-6051821522), version `2026-10-08-s35-stars-recurring-v1`.
Base `27ae2dec9aadfe03bada575a416d1f1afdf3c383`; branch `feature/s35-stars-recurring`; PR → `v2`.
Native, документы, PR/manual merge и предварительный релиз разрешены автономным мандатом6004574101.

## Global Constraints

- Один Go-процесс HTTP/River/Telegram; существующие PostgreSQL, Redis, React, React-admin, 3X-UI **3.7.0**.
- Новый процесс, worker, зависимость, реестр событий или универсальный платёжный слой не добавляются.
- Чужой SQL/private-пакеты из модулей не используются.
- `stars_recurring?: boolean` добавляется **в конец** Input/Quote с `omitempty`; отсутствие/false сохраняет прежние байты и разовую покупку.
- UUID аккаунта, panel key, VPN UUID/sub ID, история, оплаченные quote/target, старые поля/порядок JSON/body hashes и cookie/bearer правила сохраняются.
- Только signed Mini: `purchase`, 30 дней, XTR, `0 < amount <= 10000` и явный `stars_recurring=true`; native period2592000.
- Unknown legacy billing с `LegacyUserID` остаётся закрытым до С46.
- Только actual native True подтверждает bot-cancel; resume True = `resume_allowed`, не active.
- Grace: `paid_until < now <= paid_until+24h`; lapse после24h, timer15min.
- Авторизация распространяется на локальные проверки, PR/v2 и согласованный предварительный релиз; production и переключение живого Happ исключены.
- Один fresh Astra/high whole-branch review; один author Critical/Important RED→GREEN pass при необходимости; Minor записаны, no re-review.
- Conventional Commits + `Co-Authored-By: Codex <noreply@openai.com>`.

## Review Focus

- Два настоящих first charge одного invoice: оба сохраняют собственную first-charge identity для отмены, второй не создаёт выдачу. TestStarsRecurringFirstPayment, task1.
- Потеря ответа resume одновременно с restriction/quarantine: новое required cancel имеет приоритет; старое подтверждение не открывает внешнюю оплату. TestStarsSubscriptionAuthorityRace, task2.
- Provider event с меньшим/random update_id после простоя и exact replay: serial first-seen facts принимаются, повтор не переопределяет cancel. TestStarsSubscriptionUpdates, task2.
- Refund-before-success последующего цикла: исходный immutable invoice-root refund сохраняется, реальный child receipt не выдаёт доступ. TestStarsRecurringRefund, task3.
- Same-plan operator reassignment и внешний pending checkout: старая цепочка не возобновляется/не оплачивает новую выдачу. TestStarsRecurringSourceAndHandoff, task3.

---

Одна финансовая граница; четыре последовательных task commits, без исполнителей/ревьюеров по задачам.
Во всех Run ниже `C35_ENV` означает фактические env assignments из
`.superpowers/acceptance/c35-stars-recurring/database-url` и `redis-url`
(`TEST_DATABASE_URL_FILE`, `TEST_REDIS_URL_FILE`), сами URL не публикуются.
Каждый Run запускается из указанного каталога; output хранится в своём SDD workspace.
Expected проверяется по реальному выводу, а не по предполагаемому числу тестов.

### Task 1: Первый recurring invoice и неизменяемое денежное доказательство

**Files:**
- Create: `backend/db/migrations/00029_stars_recurring.sql`, `backend/internal/modules/payments/stars_subscription.go`, `backend/db/stars_recurring_migration_test.go`, `backend/internal/httpapi/stars_recurring_test.go`.
- Modify: `backend/internal/modules/payments/contracts.go`, `purchase.go`, `stars.go`, `backend/internal/modules/telegram/stars.go`, `telegram/internal/botapi/client.go` (module-relative suffixes).
- Modify: `docs/api/openapi.yaml` and generated Go/TS contracts.
- Test: existing `backend/db/stars_migration_test.go`, `backend/internal/httpapi/stars_payment_test.go`, `stars_settlement_test.go`.

**Interfaces:**
- Consumes: `CreatePurchaseOrder(ctx context.Context, account, key uuid.UUID, in PurchaseOrderInput) (PurchaseOrder,error)`; `CheckStarsPreCheckout(ctx context.Context,in StarsPreCheckoutInput)(bool,error)`; `RecordStarsPayment(ctx context.Context,in StarsPaymentInput)error`.
- Produces: final optional `StarsRecurring bool json:"stars_recurring,omitempty"` in Input/Quote; `StarsInvoice.SubscriptionPeriod int64`; `StarsPreCheckoutInput.QueryID string`.
- Produces: `StarsGateway.EditSubscription func(context.Context,int64,string,bool) error` (native True-only); immutable first-receipt-keyed subscription, cycle, control tables.
- Produces: `RequireStarsCancellationTx(ctx context.Context,tx pgx.Tx,account uuid.UUID,reason string)error`; `StarsSubscription(ctx context.Context,account uuid.UUID)(StarsSubscription,error)`.
- `StarsSubscription`: state string; nullable OrderId *uuid.UUID, ProviderState/ControlState *string, PaidUntil *time.Time; PeriodPhase string; CanCancel/CanResume/ExternalBillingBlocked/NeedsReview bool. No raw native IDs.

- [ ] **Step 1: Write migration/hash and first-payment tests.**

`TestStarsRecurringMigration` moves actual owned DB28→29 without changing old quote/body_hash/one-time checkout; payer/root/first receipt/cycle/intent mutation/delete rejected; used downgrade atomicblocked. `TestStarsRecurringInputCompatibility` marshals old/false Input/Quote and compares exact old JSON/hash; true is appended.
`TestStarsRecurringFirstPayment` uses signed HTTP fixture and real SQL/queue. Assertions:

```go
// Production removal of period/reservation/first receipt would fail this test.
if invoice.SubscriptionPeriod != 2592000 { t.Fatal("missing native period") }
if ok, err := owner.CheckStarsPreCheckout(ctx, firstQuery); err != nil || !ok { t.Fatal("first query") }
if ok, err := owner.CheckStarsPreCheckout(ctx, firstQuery); err != nil || !ok { t.Fatal("query replay") }
if ok, err := owner.CheckStarsPreCheckout(ctx, otherQuery); err != nil || ok { t.Fatal("duplicate start") }
// Exact replay -> 1 paid order/receipt/queued grant/canonical cycle.
// Genuine extra first -> 2 receipts/2 subscriptions/1 grant, required cancel.
// Wrong payer/amount/flags/period, disabled/late -> retained review, no grant.
```

Invalid true for60days/renew/change/external/amount10001/cookie fails; defaultone-time C34 still works.
Test code uses existing starsHTTPFixture/starsPayment/starsReceipt/panelFixture/testkit, no new fixture framework.

- [ ] **Step 2: Run RED.**

Run (backend): `C35_ENV go test ./db ./internal/httpapi -run 'TestStarsRecurring(Migration|InputCompatibility|FirstPayment)$' -count=1 -timeout=5m`.
Expected: actual behavior FAIL because new migration/recurring acceptance/reservation/funding absent. JSON injection for missing fields avoids compile-only RED.

- [ ] **Step 3: Implement first recurring vertical slice.**

Migration adds immutable native period, set-once query ID and the three spec tables (firstreceipt PK, unique canonical/root and root+period cycle).
Keep old input/quote bytes; add boundary rules before quote; native period onlyrecurring.
Split base payment validation from paid recurring metadata (refunds lack metadata); retain actual extra first identity and mark cancel atomically.
Extend shared funding proof for valid first recurring cycle without relaxing old method/amount/bot/payer/root/hash/negative-money guards. No child checkouts.
Only confirmed trustworthy first native receipt funds root; additional/malformed money remains review.

- [ ] **Step 4: Regenerate and run GREEN plus C34 regressions.**

Run (root): `make -C backend generate`; `npm --prefix web run api:generate`.
Expected: generated contracts updated from the owning JSON OpenAPI, no hand edits.
Run (backend): `C35_ENV go test ./db ./internal/httpapi -run 'TestStars' -count=1 -timeout=10m`.
Expected: all selected Stars tests PASS including retained one-time provenance/refund/funding/liveness guards; no individual skipped test.
Run (root): `git diff --check`.
Expected: exit0.

- [ ] **Step 5: Commit and task-done.**

Commit `feat(payments): retain first recurring Stars subscriptions` with Co-Authored trailer; stage onlytask-owned files.
Whole-task task-done command: backend `C35_ENV go test ./db ./internal/httpapi -run 'TestStars' -count=1 -timeout=10m`.
Expected: PASS and actual completion ledger line.

### Task 2: Доказанная отмена, resume, события и owner guards

**Files:**
- Modify: `backend/internal/modules/payments/stars_subscription.go`, `stars.go`, `renewal.go`, `purchase.go`.
- Create: `backend/internal/httpapi/stars_subscription.go`, `stars_subscription_test.go`.
- Modify: `backend/internal/modules/telegram/internal/botapi/client.go`, `telegram/runtime.go`, `telegram/stars.go`, `backend/internal/httpapi/stars_runtime_test.go`.
- Modify: `backend/internal/modules/accounts/service.go`, `identity.go`, `identity_link.go`, `identity_recovery.go`, `restrictions.go`, `data.go`.
- Modify: `backend/internal/modules/subscriptions/service.go`, `access_operations.go`; `backend/internal/app/modules.go`, `backend/cmd/server/main.go`, `backend/internal/httpapi/mini_app.go`, OpenAPI/generated contracts.

**Interfaces:**
- Consumes: task1 exact payment contracts/tables and public RequireStarsCancellationTx/StarsSubscription.
- Produces: `StarsSubscriptionControlInput{Action string,Confirmed bool}`;
  `ControlStarsSubscription(ctx context.Context,account,key uuid.UUID,in StarsSubscriptionControlInput)(StarsSubscription,error)`.
- Produces: `CanUnlinkTelegramTx(ctx context.Context,tx pgx.Tx,account uuid.UUID)(bool,error)`;
  `ExternalBillingEligibleTx(ctx context.Context,tx pgx.Tx,a accounts.Snapshot)(bool,error)`.
- Produces: `ReconcileStarsSubscriptions(ctx context.Context)error`; `RunStarsSubscriptionScheduler(ctx context.Context)error` (immediate+15min, errors contained).
- Produces: `StarsSubscriptionUpdateInput{BotID,PayerID,UpdateID int64; Payload,State string; At time.Time}`;
  `RecordStarsSubscriptionUpdate(ctx context.Context,in StarsSubscriptionUpdateInput)error`.
- Root injects public callback fields `RequireStarsCancellation func(context.Context,pgx.Tx,uuid.UUID,string)error` into accounts/subscriptions.Config and `CanUnlinkTelegram func(context.Context,pgx.Tx,uuid.UUID)(bool,error)` into accounts.Config.
- HTTP GET `/api/v1/stars-subscription`, POST `/api/v1/stars-subscription/control`: cookie+CSRF or Mini bearer; own UUID only.

- [ ] **Step 1: Write control/native/authority/scheduler RED tests.**

`TestStarsSubscriptionControl`: actual native True cancel retains existing finite paid access; cancel replay returnssamefacts; false/400/lostreply neverconfirmed; resume True showsresume_allowed, needsTelegramaction, no grant. Foreign/missingconsent/unconfirmed/missingkey/cookieCSRF fail, pendingexternal/refund/extraidentity/noCurrentAppliedSource blockresume.
`TestStarsSubscriptionAuthorityRace`: pause native resume, transactionally restrict/quarantine/replaceplan, then finish oldTrue; billing remainsclosed and latestrequiredcancelprocessed with capturedoriginalpayer afterTGclear.
`TestStarsSubscriptionUpdates`: serial active/canceled/failed validnative updates, exactreplay, smallerIDaccepted, foreignbot/payer/root rejected, confirmedbotcancel notoverriddenbyactive.
`TestStarsSubscriptionScheduler` pins:

```go
// Fixture has actual first receipt and provider paid_until.
clock = paidUntil.Add(24 * time.Hour)
if state.PeriodPhase != "grace" { t.Fatal("24h grace boundary") }
clock = clock.Add(time.Second)
// Run real owner reconciliation: lapsed, cancel intent, native uncertain retained.
// A canceled context stops the actual loop; ordinary API400 doesn't kill HTTP.
```

Native fake transport asserts editUserStarSubscription user_id/firstcharge/is_canceled and booleanresponse/allowed_updates; no realTelegram. Test all transaction hooks with real SQL state/actual calls, not only configfields.

- [ ] **Step 2: Run RED.**

Run (backend): `C35_ENV go test ./internal/httpapi -run 'TestStarsSubscription' -count=1 -timeout=10m`.
Expected: behavior FAIL for missing endpoint/nativecontrol/timer/requiredcancel. Boundary/type fields supplied through JSON until new APIexists.

- [ ] **Step 3: Implement public control/state and shared guards.**

Persist controlintent/authority/idempotency before HTTP, use current per-account AccessOwner and rechecklatestcommand; immutable resultprovenance retained.
Gateway successonlytrue; no DBTX duringHTTP. Retry onlycurrentsetter, uncertaintyclosesbilling. Resume revalidatescurrentcanonical/appliedsource/binding/noexternalpending; nofakeactive.
Resolve allcapturedfirstcharges, notonlycanonical. Update.Subscription parsed/trusted/dedup existingidempotency, noeventregistry.
Markcancel in same transaction of restriction/ban/unlimited/operatorintent/quarantine.
Replace static billing predicate at sharedcreate/policy and unlinkpoints with public ownerproof; owner read-only offers need same proof. Nil typedhooks used only standalonemodulefixtures keep oldfailclosed behavior.
Add one timer with existing main schedulerchannel pattern; no worker/process/dependency. Safe logs contain no payer/charge/token.
SDK/unlink/cookie cannot infercancel from providerstate/elapsedtime.

- [ ] **Step 4: Run GREEN and relevant identity/operator regressions.**

Run (backend): `C35_ENV go test ./internal/httpapi ./internal/modules/accounts ./internal/modules/telegram -run 'Test(Stars|.*Identity|.*Recovery|.*Restriction|.*AccessOperation|.*Starter)' -count=1 -timeout=15m`.
Expected: PASS, no skipped selected tests; currentcanonical APIs and HTTP retain consent/restriction protections.
Run (root): generators task1, `git diff --check`.
Expected: generation/exit0.

- [ ] **Step 5: Commit and task-done.**

Commit `feat(payments): control Stars renewal with native cancellation proof`, Co-Authored.
Whole-task command: backend preceding GREEN command.
Expected: PASS and actual ledgercompletion.

### Task 3: Последующие циклы, точный refund и безопасная смена оплаты

**Files:**
- Modify: `backend/internal/modules/payments/stars_subscription.go`, `stars.go`, `stars_refund.go`, `purchase.go`, `renewal.go`, `purchase_worker.go` only where existing sharedguards/funding need it.
- Modify: `backend/internal/modules/subscriptions/renewal.go`, `backend/internal/httpapi/purchase.go`.
- Test: `backend/internal/httpapi/stars_recurring_test.go`, `stars_settlement_test.go`, `stars_subscription_test.go`, existing five-provider financial tests.

**Interfaces:**
- Consumes: tasks1/2 immutable subscriptions/cycles/nativeproof/sharedguard/typedhooks.
- Produces: existing RecordStarsPayment accepts validsubsequentrecurring receipts with originalrootpayload, creates one existing renew order per immutablecycle and uses existing PurchaseArgs/AccessOwner.
- Produces: existing RefundStarsPurchase and RecordStarsRefund map original invoice-root receipt to actualchildorder while retaining immutable stars_refunds.order_id=root.
- Produces: existing CreatePurchaseOrder/RenewalOffer/PlanChangeContext support one-time Mini Stars renew/change and safe verified-web external methods under current guards; no new recurringchange order.

- [ ] **Step 1: Write cycle/refund/handoff RED tests.**

`TestStarsRecurringCycle`: actual appliedfirstorder, next genuine30d charge atprevpaiduntil, frozenrootterms despite cataloguechange, same VPN UUID/subID/account, 1 childreceipt/renewgrant; exactreplay1; duplicateperiodmoneyreview; invalidfirstflags/missingcanonical/outoforderexpiry/earlycharge/disabled/ban/sourcechange/pendingpartial retainmoneywithoutgrant.
`TestStarsRecurringRefund`: childnative refund exactproof/capturedfirstbinding; earlyproviderrefund before childsuccess keeps root provenance but maps actualchildledger; pending/partial retirementpreservessteps/target; alreadyappliedaccess isn't erased; payoutuncertain no blindretry; refundmarkrequiredcancel but isn't nativecancelproof.
`TestStarsRecurringSourceAndHandoff`: same-planoperatorreassign/starterclearing blocksoldchain/resume; pendingexternalblocksresume; cancellation ofall identities opens allfive verifiedweb methods/currentTG sameUUID; alreadyqueuedexternalcan'tapplyafterlatestresume; pending/uncertain/legacy remainblocked; Mini one-shotrenew/change aftercancel works and R3firstrepurchase remainsblocked.

```go
// Root immutable payload stays unchanged across child funds/refunds.
if child.Action != "renew" || child.Quote.PeriodDays != 30 { t.Fatal("cycle quote") }
if !nextExpiry.Equal(oldExpiry.Add(30*24*time.Hour)) { t.Fatal("cycle double/lost access") }
// SQL asserts refund.order_id == root, purchase_refunds.order_id == child;
// actual retainedAccess target/steps/applied checked after negative event.
```

- [ ] **Step 2: Run RED.**

Run (backend): `C35_ENV go test ./internal/httpapi -run 'TestStarsRecurring(Cycle|Refund|SourceAndHandoff)$' -count=1 -timeout=10m`.
Expected: FAIL for absentchildcycle/negative-moneyrootmapping/one-shotStarsrenew.

- [ ] **Step 3: Implement cycles/refund at financial owners.**

Keep charge/account/order lock order; receipt conflict dedup knows root and actualcycle mapping.
For validcanonicalnextcharge, validate exactpriorappliedsource/frozenquote/amount/date/expiry first; create immutablechildrenew order/receipt/cycle atomically, with original invoicepayload and no childcheckout. General funding SQL joins declared cycle and root checkout, keeps refund/reviewguards.
Separate proofbase from paid recurringmetadata; negativeproof resolves receiptactualorder under existingAccessOwner. Earlyrefundroot retained and binds knownincomingchild; nevermutate paymentproof/history.
Starsrenew/change use existing plan eligibility/livepolicy, own currentcheckoutidentity and proof; CurrentPlanSourceTx accepts eligible Telegram source but payments methodguard remainsauthoritative. Allfiveexternalhandlers convergeon sharedeligibility throughqueue/prepare/reconcile/finalwrite.
No operatorfunding escape hatch or manualrecurrencegrants.

- [ ] **Step 4: Run GREEN and all payment/renewal/access regressions.**

Run (backend): `C35_ENV go test ./internal/httpapi -run 'Test(Stars|.*Purchase|.*Renewal|.*PlanChange|.*Refund|.*Payment|.*Access)' -count=1 -timeout=15m`.
Expected: PASS for existing andnew tests, no unrelated money weakening.
Run (root): `git diff --check`.
Expected: exit0.

- [ ] **Step 5: Commit and task-done.**

Commit `feat(payments): settle recurring Stars cycles and safe billing handoff`, Co-Authored.
Whole-task command: preceding GREEN command.
Expected: PASS and completionledger.

### Task 4: Общий UI, реальный локальный whole graph и доставка

**Files:**
- Modify: `web/src/api/client.ts`, `Catalogue.tsx`, `Cabinet.tsx`, `PurchaseOrder.tsx`, `i18n.ts`; Create onlyneededcommon `web/src/StarsSubscription.tsx`.
- Test: `web/tests/stars.spec.ts`, `backend/tests/native_stars_test.go`, `web/tests/real.spec.ts` if whole-graph browserfixture needsnewassertions.
- Modify: `docs/api/openapi.yaml`, generatedcontracts and `docs/superpowers/evidence/2026-10-08-s35-stars-recurring.md`; update roadmap #33 only with actualstates.

**Interfaces:**
- Consumes: tasks1–3 publicsafe HTTP DTOs and period/controlservertruth.
- Produces: APIclient `starsSubscription(signal?:AbortSignal)` and `controlStarsSubscription(action:"cancel"|"resume",key:string)`; commonru/en UI, uncheckedeligible30d toggle, confirmcancel/resume, paidperiod/status/retry.
- Native prooftest uses actual current App modules/nativepoll/fakeTGtransport/River and TLS3X-UI3.7.0; no oldDockerproduct is acceptedasnewproductproof.

- [ ] **Step 1: Write UI and currentcomposition RED tests.**

Playwright tests: defaultone-shot bodyunchanged, eligiblefirst30d checkbox/monthlyprice/nativeperiod; unsupportedperiodnochoice; openInvoiceSDKcancelled/failed/pending distinguish aria-livehints but neverfakepaid; cancelconfirm/uncertain/resume_allowed/foreign/noauth/abort/retry/ru-en keyboard; Mini one-shotrenew/change serverenabled onlyafterrealcancel.
`TestNativeStarsRecurring`: currentwholeGo graph obtains actualnative subscription_period; first/successive/replayedcharge givesexactaccess; nativeTrue cancel/restart survives DB; later extrafirst nativecancel individuallyprocessed; HTTP/browserstate andcontextshutdown exercised.
Use existing owned TLS panel/mail/native helpers, safe checkpoints only.

- [ ] **Step 2: Run RED.**

Run (web): `npm run test:e2e -- tests/stars.spec.ts`.
Run (backend): `C35_ENV go test ./tests -run '^TestNativeStarsRecurring$' -count=1 -timeout=10m`.
Expected: actual behavior FAIL on missing recurringUI/currentcomposition behavior, not absentenvironment.

- [ ] **Step 3: Implement shared UI and owning OpenAPI.**

CommonCatalogue includes recurringbool onlychecked/eligible; serverstillvalidates. Mini managing renew/change uses existingXTRprice/forms afterownercontextpermits.
CommonCabinet/Order status component loads ownsafeDTO withabort/sessionguard and actualcontrolresult. Avoidunneededglobalfetches in otherflows; keep loading/error/retry/none.
SDKcallback stores onlyhint; order refresh determinesfinancialtruth. Stars-invoice OpenAPI security miniAppBeareronly matchescurrentruntime (C34Minor).
Generate bothclients andretainoldomitempty contracts; no nativecharge/bot/payer/tokenURL in UIlogs/fixtures.
Realgraphtest retains currentprovenance/owner/panelreset/addreplyambiguity protections.

- [ ] **Step 4: Run focused GREEN, then one final broad local check.**

Run (web): `npm run test:e2e -- tests/stars.spec.ts`; `npm run typecheck`; `npm run build`.
Run (backend): `C35_ENV go test ./tests -run '^TestNativeStarsRecurring$' -count=1 -timeout=10m`.
Expected: PASS with actual native/HTTP/panel/browser facts, no fake passedprovidercheck.
Run (backend): `C35_ENV go test ./... -count=1 -race -timeout=20m`; `go vet ./...`.
Run (web): `npm run test:e2e`; Python current regressioncommand fromPlatformworkflow; generators twice/hashcomparison.
Expected: complete suitesPASS, actualcounts recorded, generatedsourcesidentical, no skippedrequiredtest; failures named and fixedunderTDD. No broadrepeatwithoutchangedinputs/newevidence.
Run (root): `git diff --check`.
Expected: exit0.

- [ ] **Step 5: Commit, task-done, final review and guarded delivery.**

Commit `feat(web): manage Stars recurring subscriptions`, Co-Authored.
Whole-task command: web `npm run test:e2e -- tests/stars.spec.ts` and backend `C35_ENV go test ./tests -run '^TestNativeStarsRecurring$' -count=1 -timeout=10m` sequential, no parallelwebbuild/browserdistremoval.
Expected: actualtaskcompletionledger after PASS.
Buildreviewpackage from realbase27ae→currentHEAD; ONE fresh Astra/high reviewer reads spec/plan/rulings/literalReviewFocus. Regradebyusereffect; ONEauthorCritical/ImportantRED→GREENpass andwholegreensuite; Minoronlyrecorded/no re-review.
Archive complete ledger/rulings/minors and sanitizedevidence, then deletesonlythisplanSDD; commitreview/evidencecompletion.
Verify SSH/upstream/exactbranch beforepush; createPR→v2, attachPR; currentrequiredPlatform+previewCI exactsourcechecks beforeguardedmanualmerge. Then verifyactualmergeparents/tree/tag/prerelease/3OCIindicesand6configs withsource/versionlabels, #33DoD/ProjectDone onlyafteractualdelivery.
Expected: #33closed onlywithlocalimplementation/exactv2deliveryproof; productionunclaimed. Continuenextreadyroadmaptaskunderexistingmandate.

## Self-review and acceptance mapping

Spec firstinvoice/provenance/hash → task1; actualnativecontrol/billing/identity/timer → task2; allcycle/refund/renew/source/five-method money → task3; UI/OpenAPI/wholegraph/review/CI/delivery → task4. ReviewFocus allfive eachhasanowningtest above.
Task1 Produces names/types matchtask2 Consumes; task2 publictypedcallbacks matchrootintegration andtask3 guards; task3 existingactions/DTOs consumedunchangedtask4. No unsupportedreceipt-to-order reconstructionorchildcheckout.
C27notices/C36externalbrowser/C45runtimeconfig/C46reallegacyimport/C47Pythonremoval remaincanonicaldependentissues, notclaimedimplementedinC35.

