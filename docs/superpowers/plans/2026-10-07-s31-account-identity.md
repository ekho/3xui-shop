# С31 Account Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans
> for the already selected Native execution. Steps use checkbox syntax.

**Goal:** добавить независимый вход, Telegram linking/unlinking и операторское
восстановление существующего аккаунта, локально принять и доставить #29 в v2.

**Architecture:** accounts владеет identity/proofs; Telegram проверяет подпись,
notifications отправляет guarded mail, HTTP и общий frontend используют публичные
операции. Существующие credentials/session/idempotency helpers переиспользуются.

**Tech Stack:** существующие Go/Echo/Postgres/Redis/River/sqlc/oapi,
React/Vite/React-admin/Playwright, без новых зависимостей.

**Spec:** `docs/superpowers/specs/2026-10-07-s31-account-identity-design.md`.

## Global Constraints

- Branch `feature/s31-account-identity`, base `f9ddf123b3b6be92d2e83b0b15b3b8909a29c5cd`, PR→v2.
- Один Go-процесс; foreign SQL/private imports запрещены; название/домен из config.
- SAME account/VPN/subscription/payment/legacy IDs; nullable original_kind сохраняет source.
- Initial email: 8-digit code 10 min, proof 30 min, five attempts; пароль 15..128/common-password/Argon2.
- Link: opaque43, hash-only, 10 min, memory-only; conflict без merge; retirement reservations сохраняются.
- Recovery: операторский пароль/роль, reason 1..1000 trim, confirmation, Idempotency-Key; постоянная quarantine до подтверждения.
- Unknown legacy/Stars billing guards сохраняются до С35/С36; credentials не снимают restriction.
- Только собственные локальные fixtures/3X-UI 3.7.0; никаких внешних денег/TG/SMTP/production/Happ/VPN/Mac trust.
- Старые шесть web DTO, Mini DTO и cookies неизменны; API/SQL генерировать штатно.
- Native inline; один whole-branch Astra/high reviewer; один author Critical/Important RED→GREEN pass; Minor отложить; повторного ревью нет.
- Conventional Commits + `Co-Authored-By: Codex <noreply@openai.com>`.
- Все rulings/costs/evidence сохранить перед удалением только собственного SDD; exact-source CI/manual merge/actual preview verification.

## Review Focus

1. Concurrent email claim/registration versus same-account enrollment: один owner, без частичных credentials — Task 1 race test.
2. Detached TG login racing link/unlink/recovery: reservation/quarantine запрещают новый аккаунт — Task 2/3 connected tests.
3. Used/expired link proof после logout/credential change/другого TG: никаких повторных grants — Task 2 replay/revocation tests.
4. Revoked/restricted recovery operator после отправки письма: подтверждение не выдаёт credentials и quarantine остаётся — Task 3 role test.
5. Late HTTP response при смене клиента/unmount и failed logout: другой клиент/CSRF/секрет не меняются — Task 3 browser tests.

---

### Task 1: Same-account Mini email enrollment

**Files:** create accounts `identity.go`, `identity_contracts.go`, `identity_email.go`,
`identity_test.go`, `internal/queries/identity.sql`, migration
`backend/db/migrations/00025_account_identity.sql`; modify existing accounts
contracts/data/mini_app/mail/credentials queries, notifications mail renderer,
`backend/internal/httpapi/account_identity.go`/api/mini_app and tests,
`docs/api/openapi.yaml`; generator-owned outputs through Makefile.

**Interfaces:** consume existing Mini auth, email/password limiters, encrypted mail,
credential proof/code/hash/version helpers, accounts locks/audit. Produce:

```go
GetIdentity(ctx context.Context, id uuid.UUID) (IdentityContext, error)
RequestInitialEmail(ctx context.Context, rawMini string, in InitialEmailInput, ip string) (RegistrationAccepted, error)
CompleteInitialEmail(ctx context.Context, rawMini string, in InitialEmailCompleteInput, ip string) (VerifyResult, error)
```

`IdentityContext`: nullable Email, IndependentLogin, TelegramLinked, CanUnlink,
nullable UnlinkBlockedReason/PendingInitialEmail, SourceKind.
`InitialEmailInput`: Email. `InitialEmailCompleteInput`: ChallengeId UUID,
Code/NewPassword/AcceptedTermsVersion/AcceptedPrivacyVersion strings.
`Snapshot.SourceKind`, `TelegramLoginDisabled` are additive internal facts.

- [ ] Write `TestIdentityEmailMissingRoute`: enabled existing Mini fixture GET
  `/me/identity` and POST initial-email expect 200/202; old code returns 404.
- [ ] Run connected `go test ./internal/httpapi -run IdentityEmail -count=1 -race`;
  record real feature-missing RED separately from later compile gaps.
- [ ] Add generated schema/query fields/purposes, mapper and same-account request/
  completion operations; reuse proof issuance/guard/Argon2; do not grant from
  anonymous email proof. Extend notifications purpose dispatch with code-only
  initial-email copy, exact Origin/Mini allowlist/CSRF HTTP mapping.
- [ ] Add `TestIdentityEmailSameAccount` asserting same UUID/VPN/IDs/source,
  verified email/password, version+1, old Mini/web session and proofs revoked.
  Add `TestIdentityEmailClaimRace` with two accounts and simultaneous registration;
  exactly one target-email owner and no partly converted loser.
- [ ] Add occupied-email uniform202/no-mail, wrong Mini source/TG/code, five guesses,
  10-minute expiry, resend replacement, restriction/old legal-version checks.
- [ ] Run `make -C backend generate` and `npm --prefix web run api:generate`.
  Run connected `go test ./internal/modules/accounts ./internal/modules/notifications
  ./internal/httpapi -run 'Identity|MiniApp|CredentialMail' -count=1 -race`; expect all PASS.
- [ ] Commit `feat(accounts): add independent login to Telegram accounts`.
- [ ] task-done uses the same focused connected command; preserve actual outputs.

### Task 2: Signed linking, retirement and client identity UI

**Files:** create accounts `identity_link.go`/tests, Telegram identity-link verifier
method/tests, `web/src/AccountIdentity.tsx`, `web/tests/account-identity.spec.ts`;
modify accounts mini_app/operators/session/credentials/email-change identity guards,
identity SQL, Telegram mini_app, HTTP account_identity/allowlist, OpenAPI,
web API/generated client/MiniApp/Cabinet/i18n/styles.

**Interfaces:** consume Task 1 IdentityContext and credential ownership. Produce:

```go
StartTelegramLink(ctx context.Context, rawWeb string, in CurrentPasswordInput, ip string) (TelegramLinkChallenge, error)
ConfirmTelegramLink(ctx context.Context, in ConfirmTelegramLinkInput) (TelegramLinkResult, error)
UnlinkTelegram(ctx context.Context, rawWeb string, in CurrentPasswordInput, ip string) (TelegramUnlinkResult, error)
```

`TelegramLinkChallenge`: LinkToken string, ExpiresAt time.Time.
`ConfirmTelegramLinkInput`: embedded trusted TelegramSessionInput, LinkToken string.
`TelegramLinkResult`: Linked bool. `TelegramUnlinkResult`: Changed bool.
`MiniApp.ConfirmLink(ctx, MiniAppLinkInput, ip string) (TelegramLinkResult,error)`
verifies signed initData; `MiniAppLinkInput` contains InitData/LinkToken and both
accepted legal versions. Frontend new functions consume only generated DTOs.

- [ ] Write connected `TestIdentityLinkMissingRoute`, expect anonymous signed
  `/telegram/link` creates no account without proof; valid proof links same target.
  Run `go test ./internal/httpapi -run IdentityLink -count=1 -race`, record RED.
- [ ] Implement account/TG/email locking and reservation ownership; 10-minute
  hash-only link proof, owner conflict, first payload/consent, guarded used-proof
  replay, one audit/version increment. Extend creation/login guards to retired ID.
- [ ] Revoke pending link proof on web logout/revoke-others/credential mutations;
  implement independent-login/current-password unlink with legacy guard and
  session revocation. Avoid introducing a separate binding framework.
- [ ] Add `TestIdentityRetirementRace`, `TestIdentityLinkReplayAfterLogout`,
  `TestIdentityLinkOwnerConflict`, `TestIdentityLinkConcurrent`, `TestIdentityLegacyUnlinkDenied`:
  no second account, no foreign owner, expired/old-version/other-TG proof denied,
  same-owner relink and exact same UUID/history preserved.
- [ ] Write real browser `account-identity.spec.ts` tests: Mini enrollment;
  pre-consent existing-account choice without creating an account; web code/link/
  unlink; conflict/expired/error/retry; no URL/storage leak, RU/EN/keyboard.
  Run `npx playwright test tests/account-identity.spec.ts`, record RED.
- [ ] Implement shared AccountIdentity and explicit first Mini consent link mode,
  abort/reset secret lifetime, generated transports and routes. Preserve SDK
  launch memory until signed link+login finishes; no browser cookie proof fallback.
- [ ] Regenerate contracts, run focused connected Identity/MiniApp tests,
  `npm run typecheck` and focused real-browser checks; expect PASS.
- [ ] Commit `feat(accounts): link and retire Telegram identities`.
- [ ] task-done runs the focused connected and browser commands above.

### Task 3: Operator quarantine/recovery and safe exit

**Files:** create accounts `identity_recovery.go`/tests and frontend
`web/src/IdentityRecovery.tsx`; modify identity mail/queries/notifications,
operator/client HTTP/API, OpenAPI, web Admin/main/API/i18n,
`web/tests/account-identity.spec.ts` and account-security tests.

**Interfaces:** consume Tasks 1–2 proof/email/retirement ownership and existing
operator password/role/pair-lock/idempotency. Produce:

```go
RequestOperatorRecovery(ctx context.Context, rawWeb string, actor, target uuid.UUID, key string, in OperatorRecoveryInput, ip string) (IdentityRecoveryAccepted, error)
CompleteIdentityRecovery(ctx context.Context, in IdentityRecoveryCompleteInput, ip string) (VerifyResult, error)
```

`OperatorRecoveryInput`: Email/CurrentPassword/Reason string, Confirmed bool.
`IdentityRecoveryAccepted`: ChallengeId UUID, ExpiresAt time.Time, ResendAfter int64.
`IdentityRecoveryCompleteInput`: optional ChallengeId/Code/Token; NewPassword,
AcceptedTermsVersion/AcceptedPrivacyVersion strings. Recovery mail fragment link
points to `/recover-account`; initial-email completion remains authenticated Mini.

- [ ] Write `TestIdentityRecoveryMissingRoute`, expect existing operator/password/
  key request 202 then anonymous code completion200. Run connected focused HTTP
  test; record actual RED, not compile failures as behavior evidence.
- [ ] Implement quarantine-before-mail, no-role/protected-target/occupied-email
  denial before state changes; reason/password/confirmation/key validation.
  Recheck actor authorization at mail delivery and confirmation; persist disabled
  login after expiry/error/resend, and retire old ID only on successful recovery.
- [ ] Add `TestIdentityRecoveryOperatorRevoked`, `TestIdentityRecoveryQuarantineRace`,
  `TestIdentityRecoveryIdempotency`, target/actor restrictions and failed mail;
  assert role loss cannot grant, old TG cannot login/create, exactly one mutation,
  old access/money/legacy/UUID unchanged and no credentials in audit.
- [ ] Write browser recovery form and selected-client action, password/reason/
  confirmation checks, expired/error/retry/fragment cleanup. Add
  `late recovery response cannot affect another selected client` and
  `failed web logout retains CSRF and retries successfully`; record RED.
- [ ] Implement React-admin selected-client action and browser recovery route;
  fix web logout to clear CSRF only after confirmed logout/already-ended401.
  Preserve Mini always-local privacy cleanup. Abort/reset on client/unmount changes.
- [ ] Regenerate, run focused connected Identity/CredentialMail/MiniApp tests,
  `npm run typecheck` and identity/security real-browser checks; expect PASS.
- [ ] Commit `feat(accounts): recover lost Telegram access through support`.
- [ ] task-done runs these same focused commands.

### Task 4: Migration, restart, regression and local evidence

**Files:** focused migration/HTTP/browser integration tests; existing owned native
acceptance helper when required; `docs/superpowers/evidence/2026-10-07-s31-account-identity.md`.

**Interfaces:** consume final Tasks 1–3 artifacts and public API; produce actual
same-revision local acceptance, full regression and complete Native review ledger.

- [ ] Add migration/Down tests for populated original_kind/reservation/quarantine/
  proofs, legacy/source mapping and old DTO equality. Add DB restart roundtrip
  and native 3X-UI 3.7.0/TLS SMTP same-account access preservation; record RED
  before repairing any uncovered behavior, never fabricate external readiness.
- [ ] Generate and check no diff; `go vet ./...`; connected
  `RUN_BROWSER_TESTS=1 go test ./... -count=1 -race -timeout=20m`; expect all PASS.
- [ ] Run `npm run typecheck`, `npm run test:e2e`, existing connected Python tests,
  production web/backend builds and owned HTTPS/Caddy routes/headers; expect PASS.
- [ ] Record actual counts/timing/limits and all rulings/costs/deferred minors.
- [ ] Commit `test(accounts): record identity recovery local acceptance`.
- [ ] task-done uses focused connected Identity/ModuleBoundaries tests; full-suite
  outputs from unchanged final product code remain evidence.

## Delivery

One fresh whole-branch review with spec/plan/artifacts; one author Critical/Important
RED→GREEN fix pass plus changed-code regression, no re-review. Preserve complete
ledger before removing only this plan's SDD. SSH push, attach PR→v2, exact-source
required CI, live dependency/shared-state/target/parent/tree checks, manual guarded
merge and actual dev release/tag/amd64+arm64 image label checks. Close #29/Done
with local scope and next dependent scenario only after those proofs exist.
