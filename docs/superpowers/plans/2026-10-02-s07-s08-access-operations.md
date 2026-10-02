# С07/С08 и необходимый С41 — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax for tracking. Пользователь разрешил документы и реализацию автономно. С07 реализуется первым; С08 использует тот же executor. Native backend/frontend, один свежий финальный обзор всей ветки после всех сценариев.

**Goal:** Проверяемые операторские изменения доступа с постоянными identities, независимым VPN-ban и работающим месячным reset unlimited.

**Architecture:** Новый persistent тип операции использует existing River, PanelClient и PG transactions. Один account owner сериализует initial trial и новые panel writes; snapshot абсолютного target сохраняется до первого эффекта. С08 расширяет kinds, а monthly scheduler ставит в ту же очередь уникальные операции account/period.

**Tech Stack:** Go/Echo/pgx/sqlc/River, stdlib time/timezone, PostgreSQL, React-admin/Playwright; 3X-UI3.7.0. Новые зависимости и второй panel client не нужны.

**Spec:** [С07](../specs/2026-10-02-s07-subscription-operations-design.md), [С08/С41](../specs/2026-10-02-s08-access-profiles-design.md)

## Global Constraints

- UUID/sub_id/panel_key/server сохраняются. Только managed memberships известного profile; unknown/empty groups отказали. Unmanaged inbounds/чужие клиенты не изменяются.
- Compensate1..365: max(confirmed expiry,now)+N суток; devices/limit/counters/profile не меняются. Unlimited/perpetual/banned/unreachable отклоняются; missing client bonus на разрешённой configured панели без дополнительного traffic cap.
- Assign конкретной revision/period и starter trial начинают новый срок и reset; starter требует panel client, не повторяет first-free trial gate. Reset сохраняет срок/лимиты/profile и ban.
- Pending/provisioning/applied/needs_review; applied только после panel readback. Абсолютный target и шаги записываются до эффекта; reset с потерянным ответом не повторяется вслепую.
- Restriction/support-ban не меняются; role/session/actor/reason/Origin/CSRF/bounded strict JSON/idempotency как С48.
- Unlimited только hidden revision, expiry0 и inherited regular; С41 готов до включения. Снятие unlimited — configured starter trial/reset/выбранный профиль.
- С41 первый день00:00 configured timezone/defaultUTC; invalid timezone отказала; startup grace≤3600s, unique account/calendar period в БД, banned/non-unlimited recheck/skip. Никакого продления срока.
- RU/EN375px/keyboard, confirmations для destructive действий, busy inputs disabled, reason/key при retry сохраняются.
- Только local own Docker; production/Happ/MacVPN/trust/clipboard/publication вне scope.

## Review Focus

- Два workers и initial trial не получают разные panel effects: общий ownership account, подтверждённый concurrent test.
- Lost response добавления дней не добавляет N повторно: desired expiry persisted beforewrite, restart читает прежний target.
- Reset side effect enable не снимает ban и не уничтожает накопленный после неоднозначного ответа трафик: boundary doubles и native3.7.0 readback.
- Partial membership оставляет needs_review и block новых writes, explicit reconcile завершает только безопасно совпавший target.
- CurrentSubscription после новой операции не показывает старый initial trial; profile/expiry/limits/operation-status обновляются из confirmed target.

## Task 1: С07 — persistent executor и операторские API

**Files:** migration `backend/db/migrations/00012_access_operations.sql`, queries/generated store; `backend/internal/s01/access_operations.go`, `access_worker.go`, focused tests; `panel.go`, `provision.go`, `subscription.go`, `operator.go`; `backend/internal/httpapi/access_operations.go`, tests/routes; OpenAPI/generated wire; `backend/cmd/server/main.go` queue registration.

**Interfaces:**
- `CreateAccessOperation(ctx context.Context, actor,target,key uuid.UUID,in wire.AccessOperationInput) (wire.AccessOperation,error)` stores pending operation and River job atomically.
- Typed input kinds `compensate {days}`, `assign_plan {plan_id,revision,period_days}`, `starter_trial`, `reset_traffic`; only matching fields allowed, all include reason. Service validates absence as well as type.
- POST `/api/v1/operator/clients/{id}/access-operations`202; GET `.../access-operations/{operation_id}`; POST same `/reconcile` with reason, idempotency and explicit reset-cost acknowledgement only when needed. Foreign client/operation mismatch refused.
- Operation public DTO: operation_id,account_id,kind,status,created_at,updated_at,reason,operator_account_id (nullable for system), desired nonsecret conditions/plan revision, completed steps/review reason. No rawpanel/key/credentials.
- AccessWorker consumes persisted target. Shared per-account PostgreSQL ownership applies to ProvisionWorker and every access kind; unresolved needs_review owns the account until explicit resolution.
- Existing `audit_events.operation_id` FK points to trial_operations: preserve it. Add nullable `access_operation_id` FK and compatible audit DTO field for access events, without fake trial rows or dropping the old FK.
- `CurrentSubscription` and operator card read last confirmed access conditions + pending operation while preserving trial grant history.

- [ ] Write `TestAccessCompensationPreservesConditions` with now/expiry literals for active and expired clients; invalid0/366, banned/unlimited/perpetual/unreachable no effects; missing client one bonus grant. Write `TestAccessAssignmentAndResetPreserveIdentityAndBan`, `TestAccessReplayLostResponseAndTrialRace`, `TestAccessPartialWriteAndExplicitReconcile` and `TestCurrentSubscriptionAfterAccessOperation`.
- [ ] Observe focused RED against real PG and boundary-specific native-shaped panel double; doubles assert exact IDs, limits, counters, enabled and ordering. Then author strict compatible OpenAPI and operation schema.
- [ ] Implement only these kinds using existing bounded transport, worker lease and account locks. Persist desired absolute target before panel writes; update metadata from readback. Ambiguous reset/membership blocks, metadata update failures do not invite second reset.
- [ ] Generate types/sqlc; run full Go race/vet with testkit. Report locks/timing/role-revoke and rollback evidence; root commits after UI/native С07 acceptance.

## Task 2: С07 — операторский интерфейс и native acceptance

**Files:** `web/src/Admin.tsx`, small `AccessOperations.tsx`, typed API/generated types, i18n/styles, `web/tests/s07-s08.spec.ts`; acceptance drivers/evidence owned separately by native e2e/root.

**Interfaces:** Uses Task1 operation API and С09 revisions; component tracks operation_id/status and GET refresh, clear distinction compensate versus destructive newperiod/reset.

- [ ] RED browser tests for days validation/plan revision selection, explanation/confirmation/reset-cost acknowledgement, pending/error/same-key retry, reconcile mismatch,401/403/stale-card isolation and new subscription view. Implement existing patterns, then full `npm --prefix web run test:e2e`.
- [ ] Own Docker3X-UI3.7.0 real compensation, expired basis, missing bonus, assign/starter/manual reset; verify identities/counters/ban/server before/after. Lost-response/restart and partial-reset use controlled fault gate only on this test stack. Record С07AC1–8, commit coherent С07 stage before С08 extension.

## Task 3: С08/С41 — profiles, independent ban and monthly schedule

**Files:** migration `backend/db/migrations/00013_access_profiles.sql`, queries/generated; `access_operations.go`, `access_worker.go`, `panel.go`, `provision.go`, `subscription.go`, `config.go`, `monthly_reset.go`, focused tests; `cmd/server/main.go`; OpenAPI/wire; frontend component/kinds/i18n/tests; owned Compose configuration only if timezone requires it.

**Interfaces:**
- Extend typed kinds `set_profile {profile:regular|euru|unlimited}` and `set_vpn_ban {vpn_banned:boolean}` on same operation API/executor; save no-client profile for future trial/bonus.
- `PanelClient.ProfileInboundIDs(ctx,profile)` resolves exact managed tag segments; unlimited includes regular, reject empty/unknown. Preserve all unrelated memberships/raw fields; native3.7.0 uses full client payload at `clients/update/{email}`, attach/detach and bulkEnable/bulkDisable/resetTraffic. Existing positive devices uses native limitIp=devices+1; preserve this convention everywhere.
- `Service.EnqueueMonthlyResets(ctx context.Context, now time.Time) error` computes current eligible period from configured timezone and queues same-executor jobs with system actor. Startup plus bounded timer from server, period uniqueness in DB; recheck banned/profile immediately before effect.
- Config `ACCESS_RESET_TIMEZONE` defaultUTC, Go LoadLocation. Existing delivered image is scratch: embed stdlib `time/tzdata` so named zones work in the container. No separate scheduler framework.

- [ ] RED `TestAccessProfilePreservesConditionsAndUnmanagedMembership`, `TestAccessUnlimitedAndRevoke`, `TestAccessBanPersistsThroughEveryReset`, `TestAccessNoClientProfileAndProvisioning`, `TestMonthlyResetTimezoneGraceAndUniquePeriod`, `TestMonthlyResetRechecksAndLostResponse`. Include UTC and Europe/Moscow month boundary, exact3600s/3601s, double process, banned skip, leave-unlimited skip, no overdue-month reset/expiry extension.
- [ ] Implement extension and schedule atop Task1 with explicit system audit. Do not enable unlimited if its hidden plan or scheduler prerequisite is invalid. Regular/euru changes preserve limits/expiry/counters; unlimited revoke uses configured trial/reset; no-client nonunlimited only saves intent. Matching desired-state new-key no duplicate effect. Persist the execution actor for reconciliation separately from the original actor; test another live operator recovering after the author's revocation and the reconciler's revocation before a destructive retry, including system monthly operations.
- [ ] Extend RED/GREEN browser tests for separate profile/ban/restriction/support states, unlimited consequences/reset confirmation/status/errors/RU/EN375px/keyboard. Generate Go/web; run full Go race/vet and web suite.
- [ ] Native Docker verify regular↔euru, unlimited+inheritedregular/revoke, ban→reset→ban retained→unban; identities unchanged, no unmanaged writes. Deterministic scheduler proof uses injectedclock in isolated tests, not Mac clock mutation. Backup/restore accounts/operations/monthly period uniqueness; run existing auth cleanup afterrestore.

## Task 4: Whole-branch review, evidence and local completion

**Files:** `docs/evidence/s07-acceptance.md`, `s08-s41-acceptance.md`, aggregateprogress/roadmap, private ledger.

- [ ] Root audits all31 scenario AC plus С41; exact current revision, command results, actual surface evidence and exclusions. Fix important findings with regression RED→GREEN, affected green suite.
- [ ] One fresh read-only Astra/high whole-branch review of baseline32b0205→current revision, then one bounded important-fix pass. No per-task reviewer cycle.
- [ ] Root commits Conventional Commits with Co-Authored; full source checks once after final changes. Retain worktree/branch. Mark objective complete only when accepted local scope fully achieved; production/push/MR/CI remain distinct and unclaimed.
