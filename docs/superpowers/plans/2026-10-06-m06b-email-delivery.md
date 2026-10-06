# М06b2 — Email delivery: Native implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Координатор выполняет задачи последовательно; один fresh whole-branch reviewer в конце.

**Goal:** Существующая email-доставка принадлежит notifications, SMTP не удерживает SQL Tx и сохраняет revoke ordering.

**Architecture:** Перенести mail SQL/encryption/templates/SMTP/River за notifications/internal. Accounts владеет email session guard и public proof validation; app связывает function hooks. Caller-Tx mail/job остаются атомарны.

**Tech Stack:** Go1.27.1, pgx/sqlc/Redis/River, Echo5, React/Playwright, Docker3XUI3.7.0; no new dependencies.

**Spec:** [2026-10-06-m06b-email-delivery-design.md](../specs/2026-10-06-m06b-email-delivery-design.md), `2026-10-06-m06b2-email-delivery-v1`, owner #60.

## Global Constraints

- Base fresh origin/v2 729eea58b9a510ccc8e5ca191673861407d50555, branch feature/m06b-email-delivery, PR v2, no codex prefix/force-push. M06b1 PR68/dev.25 delivered.
- #60 OPEN до notifications email → audit → shared platform/store removal и M06 acceptance; Python С47 отдельно. No production/real provider/Telegram/Happ/VPN/macOS trust.
- API/all15 migrations/dependencies/UUIDs/ciphertext/job identity сохраняются; AES-GCM nonce-prefix/AAD UUID, uppercase Type/Locale/Token/Code, delivery_id/mail_delivery/MaxAttempts5, ru/en templates unchanged.
- Notifications не импортирует accounts/platform/app/wire/HTTP/private peers; mail SQL only owner. No DB Tx during SMTP; existing email advisory namespace hashtextextended(email,0), MaxConns1 same pooled connection, cleanup Background1s/close broken connection.
- Accounts owns proof rules and account→sorted recipients→proof lock order; centrally guard revokeCredentialProofs for restriction and legacy callers. Sender does not lock account row.
- Native/manual merge/preview already authorized; PR62 CI waiver inapplicable; no repeated approvals, no new interface/dependency/schema/campaign feature.

## Review Focus

- SMTP Tx removal must not race operator restriction/legacy revoke: Task1 TestMailDeliveryGuard and preserved EmailChangeConcurrency (+restriction).
- Cancelled SMTP must not leak pooled session advisory lock: Task1 TestMailDeliveryGuard cancellation and MaxConns1 cases.
- Already encrypted legacy row/job must still decrypt AAD+uppercase fields, render same link and replay unchanged: Task1 TestMailDeliveryPersistedCompatibility + AccountSecurityMigration.
- Moving cross-owner clear queries must preserve revoked/used/reset/registration predicates and caller rollback: Task1 TestMailDeliveryPersistedCompatibility and preserved queue/proof tests.
- Worker plain account read must remain deadlock-free with account-first credential mutations: Task1 preserved EmailChangeConcurrency and TestMailDeliveryNoTransaction.

---

### Task 1: Перенести email owner и связать существующих потребителей

**Files:** Create notifications/{mail.go,smtp.go,internal/queries/mail.sql,generated internal/store}, app/mail_delivery_test.go, platform/mail_delivery_test.go. Modify accounts/{mail.go,service.go,credentials.go,email_change.go,registration.go,internal/queries/{registration,credentials}.sql,generated internal/store,accounts_test.go,migration_test.go}; app/{accounts.go,boundaries_test.go}; platform/{mail.go,service.go,modules.go,email_change_test.go}; subscriptions/contracts_test.go, vpn/data_test.go, catalogue/catalogue_test.go (actual accounts ctor); cmd/server/main.go. Existing sqlc notifications block reused.

**Interfaces:** Produce all concrete MailService/MailConfig/MailPayload/MailArgs/worker/SendSMTP and accounts New/WithMailGuard/MailProofValidTx signatures exactly Spec. Config getter retains current SMTP/clock; platform MailDelivery accessor passes actual owner to worker; platform.SendMail uses existing notificationError. ClearCredential owner takes []uuid.UUID selected from accounts proofs. No root SMTP logic or account mail SQL.

- [x] Add TestNotificationsMailSQLBoundary with existing checker/all negative fixtures and remove mail table from accounts matcher. Run `go -C backend test ./internal/app -run '^TestNotificationsMailSQLBoundary$' -count=1`; Expected RED names actual accounts mail queries.
- [x] Add TestMailDeliveryNoTransaction using platform fixture + real TLS HoldNextData. During DATA assert no other xact_start and account row available; release and worker completes. Run with private *_URL_FILE, Expected RED for existing open SMTP Tx.
- [x] Add TestMailDeliveryPersistedCompatibility + TestMailDeliveryGuard: independent legacy ciphertext/job seed, consumer link/code/cleared row/replay, invalid proof/ciphertext/Tx rollback; guard cancellation/restriction/single-connection cases. Extend preserved EmailChangeConcurrency restriction and update only old lock comment. Run, Expected missing-owner/contracts RED before product edits.
- [x] Move existing mail insert/lookup/lock/clear/complete SQL into notifications owner; replace credential foreign joins with account-only proof ID selects and public Tx clears. Generate `make -C backend generate`; Expected one mail owner, unchanged schema/API/deps.
- [x] Implement concrete MailService: copied AES-GCM/templates/SMTP and job args5, short same-connection Tx→network→Tx under accounts guard. Implement WithMailGuard + unchanged proof validation and central revoke guards (all callers). Adapt app/root/worker + all qualified and bare ctor/test callsites. Expected no cycles/foreign SQL/network Tx; old error503 and dynamic cfg preserved.
- [x] Run real PG/Redis race: `go -C backend test -race ./internal/app ./internal/platform ./internal/modules/accounts ./internal/modules/subscriptions ./internal/modules/notifications ./internal/modules/catalogue ./internal/modules/vpn ./internal/httpapi -run 'Test(Mail|Account|Registration|Password|Credential|EmailChange|Operator|Legacy|Module|.*SQLBoundary)' -count=1`; Expected all new/preserved consumer checks PASS. Generation/API/all15migrations/deps and committed diff inspect; Conventional Commit+Co-Authored, verified SSH/branch/upstream push.

### Task 2: Полная приёмка и актуальные доказательства

**Files:** docs/evidence/m06b2-acceptance.md, delivery/roadmap/architecture status; private reused22-stage/compatibility/verify-final/publication helpers.

**Interfaces:** Consume Task1 unchanged public/persisted contracts; produce exact-revision local evidence for fresh reviewer and remote delivery, #60 OPEN.

- [ ] Adapt own M06b1 driver/helpers/workspace/base; run22-stage generation/drift/API+schema+deps/vet/types/build/runtime/full connected race/Python/Playwright/Compose/smoke/native3.7/TLS/purchase/repeat/paid restore/down. Expected all PASS at one committed product, own stack stopped.
- [ ] Record actual counts/durations/commands/limits/all Native rulings; M06b1 already delivered facts and next M06c/M06d. Expected local acceptance distinct from pending exact-source CI/manual merge/preview.
- [ ] Commit docs + Co-Authored; own verify-final checks22 records/product equivalence/generation/history/API/deps/committed whitespace. Expected PASS.

## Finish после Native-задач

- [ ] One fresh Astra/high whole-branch review; exhaustive declines→Final rulings. Important/Critical one RED→GREEN fix pass + green suite, no re-review; minors deferred.
- [ ] Publish all rulings; export/delete only own Native scratch after verified clean pass.
- [ ] Create/attach PR v2 → exact-source CI → fresh gates/effects/manual SHA-guarded merge → parents/source-equal tree/preview tag+3multiarch images. Record M06b2 delivery in #60, keep OPEN/In progress, then M06c audit.
