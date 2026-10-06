# М06b1 — Telegram delivery: Native implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Координатор выполняет задачи последовательно; один fresh whole-branch reviewer в конце.

**Goal:** Существующая очередь Telegram имеет одного private notifications owner, без изменения trial/delivery API и persisted replay.

**Architecture:** Перенести существующие lease/complete и SQL за module/internal. Subscriptions вызывает public caller-Tx ports; app собирает accounts allowlist и current CardTx callback. Active bridge использует owner, root временно содержит только HTTP DTO/error facades.

**Tech Stack:** Go1.27.1, pgx/sqlc/Redis/River, Echo5, React/Playwright, Docker3XUI3.7.0; no new dependencies.

**Spec:** [2026-10-06-m06b-telegram-delivery-design.md](../specs/2026-10-06-m06b-telegram-delivery-design.md), `2026-10-06-m06b1-telegram-delivery-v1`, owner #60.

## Global Constraints

- Base fresh origin/v2 6cc8d031ea9185bdbfc25affd00c9fc2f873e42e; branch feature/m06b-telegram-delivery, PR base v2, no codex prefix/force-push.
- M06a PR67/dev.23 доставлен. #60 OPEN до support → notifications (Telegram → email) → audit → removal shared platform/store и собственной acceptance.
- Notifications не импортирует platform/wire/HTTP/Echo/app/private peers; telegram_deliveries SQL только в owner. Authority accounts, CardTx subscriptions, Bot API telegram.
- API, all15 migrations, dependencies, IDs/sequence/raw hashes/result/lease/retry неизменны. Caller Tx enqueue сохраняет trial/grant/audit/idem atomicity.
- Claim limit1, DB-clock60s/attempts+1/SKIP LOCKED/operator allowlist; opaque32bytes/43chars/SHA256. Complete id/token409 → JSON400; existing5 failed codes, positive sent IDs, constant-time token, live lease/chat/role, same raw-result replay only.
- Автономные Native/documents/manual merges/v2 preview разрешены; CI waiver PR62 неприменим. No production/real provider/Happ/VPN/macOS trust; Python retirement С47 отдельно. Email extraction — M06b2.

## Review Focus

- Старый completed result_hash с другим key order/extra field всё ещё replay без typed normalization: Task1 TestTelegramDeliveryPersistedCompatibility.
- Malformed raw JSON при invalid id/token сохраняет409 приоритет, enum/chat mismatch rejected: Task1 TestTelegramDeliveryPersistedCompatibility и preserved TestTelegramLease.
- Caller rollback/outbox INSERT/card failure не оставляет trial/audit/idem или lease/attempt: Task1 TestTelegramDeliveryComposition.
- Текущая карточка после решения и latest sent message выбираются через одну caller Tx: Task1 TestTelegramDeliveryComposition и preserved TrialBridgeDecisionReplay/TelegramLease.
- Два конкурентных workers не получают одну live lease; expired completed replay rejected и re-lease token меняется: Task1 preserved TelegramLeaseConcurrent/TelegramLease + compatibility expiry case.

---

### Task 1: Перенести Telegram outbox и подключить всех потребителей

**Files:** Create modules/notifications/{telegram.go,internal/queries/telegram.sql,generated internal/store}; app/telegram_delivery_test.go. Modify app/{accounts.go,trial_bridge.go,boundaries_test.go}, platform/{service.go,modules.go,telegram.go}, subscriptions/{service.go,contracts_test.go,trial.go,data.go,operator.go,internal/queries/shared.sql}, sqlc.yaml/generated root/subscription stores. Remove obsolete db/queries/telegram.sql and root generated telegram.sql.go; remove only3 outbox queries from db/queries/trials.sql.

**Interfaces:** Produce notifications.New and five public methods exactly Spec (context/pgx.Tx/UUID/*UUID/int64/string/RawMessage/time). TelegramJob JSON tags match wire; neutral TelegramSent/TelegramFailed keep original field order/tags. Error Status/Code/Message; callback error retained for facade/bridge mapping. Subscriptions.New gains concrete *notifications.Service before config/now. Root constructor gains explicit owner/Notifications accessor; app bridge reads raw payload into subscriptions.TelegramPayload and reuses card conversion.

- [x] Add TestNotificationsTelegramSQLBoundary with existing checker and all Spec negative fixtures; run `go -C backend test ./internal/app -run '^TestNotificationsTelegramSQLBoundary$' -count=1`. Expected RED identifies old root/subscription SQL.
- [x] Add TestTelegramDeliveryPersistedCompatibility and TestTelegramDeliveryComposition using bridgeFixture. Pre-seed legacy raw sent/failed union hash and live lease; assert exact replay leaves rows unchanged, changed raw/state/enum/chat conflicts or invalid input; invalid id/token wins malformed JSON. Compare neutral Sent/Failed bytes with generated wire. Exercise current card/last-message, public Tx rollback, outbox trigger-failure trial/audit/idem rollback and card callback-failure lease rollback. Run those tests; Expected missing public owner/contracts RED.
- [x] Move existing lease/complete into notifications/telegram.go, reuse stdlib random/SHA256/RawMessage, preserve complete validation/order/hash semantics and exact SQL. Add public Enqueue/Latest Tx ports (nil message/pending state for no rows). Add sqlc owner block, remove root/subscription outbox SQL, generate; Expected one SQL owner.
- [x] Pass real tx through payload/notify/decisionResult/reconsiderTrialLocked and all callers, including subscriptions/operator.go/data.go. Compose callback + accounts allowlist in app and test root; replace root with explicit error/wire conversion facades. Bridge direct owner + neutral outcome/raw payload and remove duplicated outboxCard. Expected no new transactions/interfaces/cycles or hidden business logic.
- [x] Run focused real-PG/Redis race using private *_URL_FILE: `go -C backend test -race ./internal/app ./internal/platform ./internal/modules/notifications ./internal/modules/telegram ./internal/httpapi -run 'Test(Telegram|Trial|Approval|Lifecycle|Operator|Module|.*SQLBoundary)' -count=1`. Expected all new and preserved consumer tests PASS.
- [x] Generate backend/web; compare API/all15 migrations/dependencies with base, git diff check, inspect all caller/SQL/import boundaries. Conventional Commit + Co-Authored, verified SSH/branch/upstream push. Expected coherent owner slice.

### Task 2: Полная приёмка и актуальные доказательства

**Files:** docs/evidence/m06b1-acceptance.md; M06a delivery docs/roadmap/architecture status; private reused22-stage driver/compatibility/publication helpers outside Git.

**Interfaces:** Consume Task1 product/current app composition; produce exact-revision full/native evidence for fresh final review and remote delivery, #60 OPEN for M06b2/M06c/M06d.

- [x] Reuse completed M06a driver/helpers, adapt own workspace/base/proof names. Run22-stage matrix names/generation/drift/API+migrations+deps/vet/types/build/runtime/connected Go race/Python/Playwright/Compose build/smoke/native3.7/TLS/purchase prepare+overlay+repeat check+paid restore/down. Expected all PASS on one committed product revision, owned stack stopped.
- [x] Record commands/counts/durations/limits/all Native Rulings; update M06a source/CI/merge/dev.23 delivered truth and next M06 sequence. Expected local acceptance distinct from pending exact-source CI/manual merge/preview, #60 OPEN.
- [x] Commit docs with Co-Authored; own verify-final.py checks22 records/unchanged product/generation/compatibility. Expected PASS.

## Finish после Native-задач

- [x] One fresh Astra/high whole-branch review (explicit model/effort), declines → exhaustive Final Rulings; Important/Critical one RED→GREEN fix pass + green suite, no re-review.
- [x] Publish all rulings; export/delete only this Native scratch after verified clean pass.
- [x] Create/attach PR → v2, all exact-source CI, fresh gates/effects + manual SHA-guarded merge, actual parents/source-equal tree and preview/tag/three multiarch images. Record M06b1 delivery in #60, keep OPEN/In progress, then bounded email M06b2.
