# С10 — интеграция первой покупки: Native-план

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Координатор выполняет задачи последовательно; один свежий whole-branch reviewer в конце.

**Goal:** Сохранённый PR #5 первой покупки работает с владельцами accounts/catalogue/subscriptions/vpn и сливается в v2.

**Architecture:** Существующий С10 включается обычным merge в новую ветку от v2. Purchase код обращается к public owner ports; VPN получает явные funding/outcome функции. М05 отдельно переносит владельца денег из platform.

**Tech Stack:** Go 1.27.1, Echo 5, pgx/sqlc, River, PostgreSQL/Redis, React/TypeScript, Playwright, Docker 3X-UI 3.7.0; без новых зависимостей.

**Spec:** [2026-10-06-s10-modular-integration-design.md](../specs/2026-10-06-s10-modular-integration-design.md), сохранённая функциональная спецификация `2026-10-03-s10-first-purchase-design.md` из afaeacf.

## Global Constraints

- Base origin/v2 26d4b967; branch feature/s10-modular-integration, PR #5 → v2; no codex prefix и no force-push.
- Название/домен продукта конфигурируются; сценарные номера не добавляются в runtime names.
- Один процесс, собственные owner SQL; сохранённая миграция 15/API/targets, no new dependencies.
- Секрет уведомлений только YOOMONEY_NOTIFICATION_SECRET_FILE; параметры при деплое.
- Ни настоящих денег, URL уведомлений кошелька, production, живого Happ, VPN/trust Mac.
- Полная документация и реализация автономно приняты; Native и последовательные merge уже разрешены.

## Review Focus

- Ошибка payment outcome после panel readback не публикует access/assignment/profile: Task 1 rollback test.
- Отсутствующий funding/outcome hook не выдаёт paid access: Task 1 fail-closed test.
- Потеря ownership session во время чтения панели не заменяется новой: Task 1 preparation test.
- Изменение аккаунта во время panel read повторно проверяется без held SQL Tx: Task 1 preparation test.
- Reconcile purchase сохраняет funding и NULL executor, второй receipt не удваивает срок: Task 1 existing recovery tests; Task 2 native repeat.

---

### Task 1: Сохранить С10 и связать существующих владельцев

**Files:** preserved source afaeacf; platform/purchase.go, purchase_worker.go, yoomoney.go; modules/vpn/service.go, data.go, access_worker.go, internal/queries/access_operations.sql; platform/modules.go; app/accounts.go; cmd/server/main.go; generated source; focused platform/purchase_modular_test.go.

**Interfaces:** consumes accounts facades, catalogue.LockCurrentPlan, vpn.AccessOwner/UnresolvedTx/QueueAccessTx. Produces `vpn.PurchaseHooks` Check/Outcome with exact signatures from Spec, nullable PurchaseOrderID in AccessWrite/AccessState. Existing customer/API signatures remain those of the preserved С10.

- [ ] Merge afaeacf with `--no-commit`; keep current owner implementations and resolve functional additions, regenerate from current owner queries. Inspect every conflict; no resurrection of global owner SQL. Expected: old source ancestor, current modules remain owners.
- [ ] Run preserved purchase/receipt tests before adapting removed owner calls; record actual RED. Expected: unresolved removed global queries or purchase execution rejected by current worker, never fabricated RED.
- [ ] Adapt accounts calls via existing facades, quote via LockCurrentPlan, access via VPN ports/physical owner. Add `TestPurchaseOutcomeRollsBackAccessMetadata`, `TestPurchaseMissingHooksFailClosed`, `TestPurchasePreparationRechecksAccount` against real PG/controlled TLS panel before implementing hooks. Expected: lost outcome/false applied/stale account caught.
- [ ] Run focused RED, implement Check/Outcome and immutable composition, preserve money rules and operator guards. Review also old source files/callers affected by each resolution.
- [ ] Run `go -C backend test -race ./internal/platform ./internal/modules/vpn ./internal/modules/subscriptions ./internal/httpapi -run 'Test(Purchase|YooMoney|Access|Monthly|Trial|Provision)' -count=1` with private test file settings. Expected: all required PG/Redis tests execute and PASS.
- [ ] Validate generation and diff; Conventional Commit with Co-Authored, verify SSH/branch then push integration branch. Expected: source preserved, same PR source unchanged until final delivery.

### Task 2: Полная локальная приёмка

**Files:** existing deploy/acceptance and deploy/purchase helpers; docs/evidence/s10-modular-integration-acceptance.md; roadmap/status docs. Private verification driver stays outside Git.

**Interfaces:** consumes Task1 unchanged HTTP/PurchaseHooks and composed single process; produces exact-revision local/native/review/delivery evidence for #17 and subsequent #59.

- [ ] Read current Native helper instructions; use existing local stack and disposable secret. Run full generation/no drift, API compatibility and dependency checks, Go vet/connected race/browser tests, web types/build/full Playwright, runtime config and Python regression. Expected: all PASS, no skipped required integration checks or external payment submit.
- [ ] Run ordinary native HTTPS/3X-UI 3.7.0 checks then existing purchase prepare/overlay/check/restore on owned fixtures. Verify one access, finite trial identity/remaining period, signed callback repeat, pending paid restart and backup/restore; always stop own native stack. Expected: PASS with declared local limits.
- [ ] Record exact revision/commands/durations and M04b delivery proof; update roadmap. Expected: local verification and external acceptance clearly separate.

## Finish после Native-задач

Final task command: private `verify-final.py` проверяет всю завершённую матрицу,
идентичность final product после docs commit, историю, генерацию и совместимость.
Задача фиксирует локальные проверки; независимый review и delivery выполняются
после task-done, как требует Native workflow. #17 остаётся открытой до всех gates.

- [ ] One fresh Astra/high whole-branch reviewer; ledger every declined judgment/ruling/minor. Critical/Important receive one RED→GREEN fix pass and required suite; no re-review.
- [ ] Preserve all rulings in public evidence; export/delete only this plan's Native scratch. Expected: record retained, sibling work preserved.
- [ ] Inspect final source, verify old afaeacf ancestor and source remote unchanged; fast-forward remote feature/s10-first-purchase, update/attach PR #5. Wait all exact-source CI, check merge effects, merge into v2 with SHA guard and verify merge tree/preview/tag/3 images. Expected: authorized delivery, no auto-merge/production. Close #17/Project Done only after its own gates; proceed to М05.
