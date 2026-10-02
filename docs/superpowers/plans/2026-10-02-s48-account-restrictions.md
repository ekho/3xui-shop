# С48 — ограничения аккаунта: план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax for tracking. Пользователь принял С48 и разрешил документы и реализацию автономно; дополнительных согласований нет. Native specialist placement применяется по enabled TradeOS policy; один свежий обзор всей ветки после С48 → С09 → С07/С08.

**Goal:** Явное ограничение аккаунта с отзывом авторизации и сохранением старой истории регистрации, без изменения VPN.

**Architecture:** Переиспользовать `accounts.restricted`, account/role locks, idempotency, credential queries и аудит С02/С06. История SQLite переносится отдельной атомарной CLI-процедурой поверх существующих identities; React-admin получает действия и отдельный блок истории.

**Tech Stack:** Go/Echo/pgx/sqlc/River, PostgreSQL, Python stdlib sqlite3, React-admin/TypeScript/Playwright. Новые зависимости не нужны.

**Spec:** [С48](../specs/2026-10-02-s48-account-restrictions-design.md)

## Global Constraints

- С48 не меняет подписку, VPN-ban, ограничения переписки, Stars-рекуррент, платежи или идентификаторы подключения.
- Actor только из сессии; reason после trim от1 до1000 Unicode-символов, без NUL; JSON до16 KiB, unknown fields запрещены, Origin/CSRF обязательны.
- Самоограничение и target с активной operator role запрещены. Снятие ограничения не восстанавливает старые sessions/proofs.
- Импорт version1 через stdin и `DATABASE_URL_FILE`; dry-run без записи. SQLite `mode=ro`, timezone naive timestamps явно задана.
- Только own Docker 3X-UI3.7.0; production/real data/Happ/Mac VPN/trust/clipboard исключены. Root владеет локальными commits; публикация не входит.
- RU/EN,375px, keyboard, semantic labels/focus, disabled controls во время write.

## Review Focus

- Отключённая во время операции роль не оставляет принятый запрос без авторизации: тест live role под account locks.
- Отзыв sessions/proofs не может пережить rollback ограничения: искусственная ошибка audit вставки откатывает весь переход.
- Повтор старого rejected после ручного снятия не блокирует клиента: import→unrestrict→reimport.
- Исторические actor/timestamps NULL не превращаются в нового web actor или дату импорта: fixture с NULL и несколько страниц с одинаковым временем.
- Запоздалая карточка другого клиента не подставляет restriction/history: browser test смены карточки во время загрузки/POST.

## Task 1: Backend, schema, API и контролируемый перенос

**Files:** `docs/api/openapi.yaml`; `backend/db/migrations/00010_account_restrictions.sql`; `backend/db/queries/operators.sql` (или отдельный restrictions.sql); `backend/internal/store/*` generated; `backend/internal/wire/models.gen.go`; `backend/internal/s01/restrictions.go`, `legacy_approval.go` и соответствующие tests; `backend/internal/s01/operator.go`; `backend/internal/httpapi/operator.go`, `api.go`, tests; `backend/cmd/server/main.go`, CLI tests; `deploy/s48/export_legacy_approval.py` и unittest в `tests/`.

**Interfaces:**
- Consumes: `lockOperatorPair(ctx, tx, actor, target)`, existing `replay`/`saveIdempotency`, DeleteAccountSessions/RevokeCredentialProofs/ClearRevokedCredentialMail.
- Produces: `SetOperatorRestriction(ctx context.Context, actor, target, key uuid.UUID, in wire.OperatorRestrictionInput) (wire.OperatorRestrictionResult, error)`; POST `/api/v1/operator/clients/{id}/restriction`, `{restricted,reason}`, result `{restricted,changed_at,operator_account_id}`.
- Card adds optional legacy snapshot, `legacy_events`/`legacy_has_more`; history adds `kind:legacy` and separate decimal-string source-ID cursor, preserving existing UUID audit/trial cursor contracts.
- CLI `server import-legacy-approvals --dry-run|--apply`; typed version1 users/events package; output only counts/stable codes. No HTTP import route.

- [ ] Write `TestRestrictionRevokesAuthorizationWithoutChangingAccess`, `TestRestrictionReplayAndProtectedOperator`, `TestRestrictionRollbackAndConcurrentWrites` before their implementation. Assert restricted login/API, zero live sessions/proofs/ciphertext, unchanged VPN/subscription and one audit per actual transition. Replay original response, changed body409, no-op no event, protected/revoked role and invalid reasons rejected.
- [ ] Run focused Go checks; observe missing behavior fail. Add `TestLegacyApprovalImportAtomicReplay` with approved/pending/rejected, nullable metadata, original events and 51+ same-time records; unknown/mismatched identity and changed duplicate roll back. Add read-only exporter unittest using temporary SQLite and explicit timezone; assert byte-identical input and exclusion of arbitrary payload/action.
- [ ] Describe compatible OpenAPI additions first; regenerate Go. Implement migration/query/service/handlers/CLI/exporter using existing guards and one transaction. Revoke proofs/sessions only on actual first restricted transition, including rejected import; no panel/jobs.
- [ ] Run `go tool sqlc generate`, `go tool oapi-codegen -config oapi-codegen.yaml ../docs/api/openapi.yaml`; run `go test -race ./... -count=1` and `go vet ./...` with existing file-backed test env. Run `poetry run python -m unittest discover -s tests -v`.
- [ ] Root reviews diff and stages a Conventional Commit with Co-Authored footer after UI/integration passes; do not commit independently.

## Task 2: React-admin restriction and historical approval

**Files:** `web/src/Admin.tsx` (small extracted component if necessary), `web/src/api/client.ts`, generated schema, `web/src/i18n.ts`, `web/src/style.css` as needed, `web/tests/s48.spec.ts`.

**Interfaces:** Consumes Task1 schemas and current client card; produces restriction action/form and paged read-only legacy history. Existing key/support/trial states remain separate.

- [ ] Write browser cases for confirmed restrict/unrestrict, error→same-key retry with reason retained, disabled inputs, key cleared after success, protected target, legacy NULL/actor rendering and pagination, RU/EN/mobile/keyboard and stale-card isolation. Run focused file to see missing controls fail.
- [ ] Regenerate `npm --prefix web run api:generate`. Implement one typed API wrapper, reuse existing reason/confirmation/abort patterns; no new frontend framework or persistent secret storage.
- [ ] Run `npm --prefix web run test:e2e`; report all failures and test counts. Root stages commit only with integrated Task1.

## Task 3: Local acceptance and cutover boundary

**Files:** `deploy/s48/local.py`, `deploy/s48/browser.mjs` if needed for reproducible actual evidence; `docs/evidence/s48-acceptance.md`; existing roadmap; private plan ledger.

**Interfaces:** Reuses own Compose project/private bridge from `deploy/s01/local.py` and `deploy/s06/local.py`; no live Telegram or installed VPN.

- [ ] Record owned images, rollout/rollback and unchanged Docker VPN state. Rebuild only backend/gateway after guards pass. Use controlled disposable identities and readonly SQLite fixtures.
- [ ] Run real web/API restrict→old-session denied→unrestrict→new login; verify panel identity/limits/counters unchanged. Run pipe dry-run/apply/replay/conflict; then backup/restore fixture verifying snapshots/events/restriction and auth cleanup procedure.
- [ ] Document AC1–8 evidence and exact source revision. Runbook says registration reminders/approval middleware are stopped together only at future cutover; trial approval continues, current legacy code stays intact.
- [ ] Root commits verified S48, updates ledger, then proceeds to S09 without another approval gate.
