# М06d — Composition cleanup implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Координатор выполняет Native последовательно; один fresh whole-branch reviewer в конце.

**Goal:** Удалить общий platform.Service/store и завершить архитектурную приёмку М06, сохранив все текущие сценарии и154 regression checks.

**Architecture:** app.Modules содержит concrete owner refs; owned Config groups и actual assembly. HTTP делает transport conversions, Telegram bridge использует только subscriptions/notifications, CLI/River вызывают public owners. Затем убрать все dead facades/root generation.

**Tech Stack:** Go1.27.1/pgx/sqlc/Redis/River/Echo5, React/Playwright, Docker3XUI3.7.0; no new dependencies.

**Spec:** [2026-10-06-m06d-composition-design.md](../specs/2026-10-06-m06d-composition-design.md), 2026-10-06-m06d-composition-v1, #60.

## Global Constraints

- Fresh origin/v2 d796e3a076785ce0f5b933fcd2a22c0f4baf4e26, feature/m06d-composition, PR v2, no codex prefix/force/auto-merge. М06c PR70/dev29 fully delivered.
- All15 migrations/API/dependencies/IDs/keys/actions/nullable fields/idempotency/job JSON/hash/lock ordering/limits/security preserved. One Go-process and existing disabled-TG/legacy bearer exclusivity unchanged.
- Config groups as Spec; HTTP origins, Accounts.Operators and Subscriptions.PanelID bound to dependent owner providers; old namespace platform, defaults3/15/1/UTC/legacy bearer true и file conflicts remain. Existing Accounts.Now supplies controlled clock; production cfg immutable.
- App contains no all-domain methods/wire/SQL/shared store; HTTP conversions only, no module-private imports. No new bus/framework/interfaces/config toggles. Modules retain own SQL/private models.
- All154 legacy tests one-to-one TestRegression<old name minus Test>, actual app assembly; preserve assertions/fixtures/no source-copy business rules. Test-only raw persisted rows allowed.
- #60 OPEN/In progress until whole-M06 acceptance and exact source delivery/preview. Future scenarios/C45/C46/C47 unchanged; Python removal only C47. Production/provider/realTelegram/Happ/VPN/macOS trust excluded.
- Autonomous spec/plan/Native/manual v2 merge/preview authorized; no repeat menus. One Astra/high final review, one TDD fix pass, no re-review.

## Review Focus

- Owned config aliases preserve dynamic old fixture transitions and actual operator list/origin/panel bindings; invalid TLS/secret conflicts/default timezone still fail closed: Task1 actual config/provider/HTTP/composition/native cases, Task2 old config regressions.
- Transport mapping preserves big IDs/amounts/nullable13/false/empty arrays, status/Retry-After/cookie rotation/allowlisted conflict details and no secret errors: Task1 actual HTTP tests, Task2 all154+persisted audit.
- Role revocation/restriction and target/cursor validation precede aggregate card/history/key; no SQL escapes owner: Task1 actual operator cases, Task2 restriction rollback/history same-time pages and boundaries.
- Native errors/outage/restart and disabled token path never route through a stale parallel Service/worker graph: Task1 NativeTrialFlow/Outage/Restart and lifecycle; Task3 actual process/TLS/native/restore.
- Test migration does not replace checks with copied facade logic or remove failures; SMTP guards/order/MaxConns1, Tx rollback and paid replay keep actual owners: Task2 baseline154 mapping/all regressions; Task3 full22 and whole-M06 graph.

---

### Task 1: Перевести действующую сборку и transport consumers

**Files:** Create app/config.go and app/modules.go (replace app/accounts.go); modify app/{telegram,trial_bridge}.go and composition tests; httpapi/*.go and fixtures; cmd/server/*.go/tests; backend/tests connected/native fixtures; subscriptions/service.go. Move only active transport converters from platform/{accounts,catalogue,support,operator,trial,subscription,subscriptions_mapping,access_operations,telegram,purchase,yoomoney,restrictions,provision}.go into matching HTTP files; leave dead platform solely for Task2 old regressions.

**Interfaces:** Produce app.Modules concrete9 refs, app.Config/HTTPConfig/LoadConfig/SecretFile/Validate and NewModules(pool,limiter,queue,*Config); httpapi.New(*app.Modules,*pgxpool.Pool,app.HTTPConfig); app.NewTrialBridge(*subscriptions.Service,*notifications.Service); app.NewTelegram(telegram.Config,*subscriptions.Service,*notifications.Service,*http.Client); subscriptions.TrialServer()(string,bool), exactly Spec. Private HTTP adapters keep same wire fields and error data. Task2 consumes actual API/owners and groups.

- [x] Add TestRuntimeCompositionBoundary in app/boundaries_test.go: actual go list -deps ./cmd/server rejects old platform/store runtime deps; app AST rejects wire/store/SQL and all-domain Service methods, negative fixtures. Run `go -C backend test ./internal/app -run '^TestRuntimeCompositionBoundary$' -count=1`; Expected actual RED naming current graph.
- [x] Implement owned Config groups with all old env/validation, compose9 owners/hook closures/clock and exact shared-value providers; no full domain facade. TrialServer returns only existing panelID/trial flag. Narrow Telegram bridge, direct module workers/CLI, preserve safe CLI JSON/errors.
- [x] Adapt HTTP constructor/owner refs and existing typed conversions/error mapping at transport; role/target/cursor checks and actual card fallback unchanged. Replace a.svc calls; health pool Ping, no SQL. Adapt current app/HTTP/cmd/connected/native fixtures to same assembly and new groups/public inputs.
- [x] Run private PG/Redis `RUN_BROWSER_TESTS=1 go -C backend test -race ./cmd/server ./internal/app ./internal/httpapi ./tests -run 'Test(RuntimeCompositionBoundary|ModuleBoundaries|.*SQLBoundary|.*Composition|TrialBridge.*|NativeTrial.*|RegistrationHTTP|SessionBoundary|AccountSecurity.*|Operator.*|Support.*|Purchase.*|Internal.*)' -count=1`; Expected actual runtime boundary, security/current consumers and NativeTrial flow/outage/restart PASS. Read every failure, no disabled checks.
- [x] Compare generation/API/all15 schemas/deps, actual global caller inventory and diff; Conventional Commit+Co-Authored. Task-done executes exact broad focused command once on committed stage; deferred product push until final reviewed source avoids duplicate costly CI, canonical docs already public.

### Task 2: Сохранить154 regressions и удалить переходную структуру

**Files:** Move all platform/*_test.go into httpapi/regression_*_test.go; add only needed test fixture/independent persisted-row structs; remove all platform/*.go/root store/db/queries; backend/sqlc.yaml remove first shared block; app/boundaries_test.go architectural removal gate. Baseline mapping in own private discovery.json (154 names at fresh base).

**Interfaces:** Consume Task1 actual API/Modules/owned groups. Produce no runtime platform/store or renamed all-domain facade; all154 one-to-one regression tests; no peer-private/root store test dependency.

- [ ] Add TestSharedFacadeRemoved: assert directories internal/platform/internal/store/db/queries absent and no root sqlc output/query block; negative runtime import fixture retained. Run `go -C backend test ./internal/app -run '^TestSharedFacadeRemoved$' -count=1`; Expected RED on actual still-existing facades.
- [ ] Migrate154 funcs to TestRegression<old name minus Test>, preserving assertion flow/inputs/failure cases and readable actual owner calls. Actual app.NewModules-backed API fixture owns controlled clock/group config; native-only helpers call owners, no copied rule/SQL mutation implementation. Two persisted structs keep previous column/types; account snapshot uses public accounts values.
- [ ] Remove dead facades/helpers/root generated store/idempotency queries/sqlc block; adapters only where active transport uses them. No removal of module tests/schema/maintenance/Python/legacy HTTP. `make -C backend generate`; Expected only private owners outputs, stable subsequent generation.
- [ ] Verify baseline154→154 explicit function-name correspondence, source/module ownership/import/global consumer inventory, API/all15 migrations/deps. Run private PG/Redis `go -C backend test -race ./internal/app ./internal/httpapi -run 'Test(SharedFacadeRemoved|RuntimeCompositionBoundary|ModuleBoundaries|.*SQLBoundary|Regression.*)' -count=1`; Expected all154 migrated behavior tests and whole architectural boundaries PASS.
- [ ] Commit scoped source/tests with Conventional+Co-Authored; task-done executes exact regression command once. Product push remains deferred until final review/required full acceptance.

### Task 3: Whole-M06 acceptance и доказательства

**Files:** docs/evidence/m06d-acceptance.md, roadmap/architecture/M06 delivery status and task states; own adapted full22/compatibility/regression-inventory/verify-final/publication helpers.

**Interfaces:** Consume final owned assembly/public contracts; produce D01–D08 evidence at one committed product. #60 stays OPEN until remote delivery; preserved154 inventory and architecture result included.

- [ ] Adapt existing full22 M06c driver to own path/base/actual code; do not rerun green baseline. Run generation/drift/API+15schemas+deps/vet/types/build/runtime/full connected race/Python/Playwright/Compose/smoke/native3.7TLS/purchase/repeat/paid restore/down; Expected22/22 PASS one committed product and own stack down. Existing unknown external SMTP/panel duplicate retry limits remain explicit.
- [ ] Record actual stage/package/test counts/durations,154 mapping/whole-M06 import+SQL+active composition evidence, all Native rulings and limits; source/CI/merge/preview separate. Mark closed functional scenarios unchanged; full #60 pending remote.
- [ ] Commit docs + Co-Authored; own verify-final checks22 records/product equivalence/generation/API/schemas/deps/154 map/no retired folders/whitespace. Task-done runs exact final verifier, Expected PASS.

## Finish

- [ ] One fresh Astra/high whole-branch review with full range/Spec/Review Focus/ledger/proofs. Exhaustive declines→Final rulings; Important/Critical one RED→GREEN fix pass+green full suite, no re-review; minors deferred.
- [ ] Publish exhaustive rulings, export logs/ledger/report then delete only own Native workspace.
- [ ] Final source SSH guarded push/create+attach PR v2/exact-source CI/manual SHA guarded merge/parents/source-equal tree/preview tag+3publicmultiarch indexes+6configs. Close #60/Project Done only after own whole-M06 acceptance; re-read statuses/dependents and choose next available roadmap order without treating external S13 resources as already ready.
