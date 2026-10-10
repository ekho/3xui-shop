# С40 Group Reconciliation Implementation Plan

> **For agentic workers:** use superpowers:executing-plans inline; one fresh
> independent whole-branch review after implementation. Task mandate already
> supplies Native execution and manual merge authority.

**Goal:** сверить assigned VPN client memberships после изменения панели.
**Architecture:** vpn prepares durable access operations; existing AccessWorker
alone writes the panel. Existing notifications delivery keeps current rights.
**Tech Stack:** Go/pgx/River/PostgreSQL, existing Telegram runtime and React UI.
**Spec:** `docs/superpowers/specs/2026-10-10-s40-group-reconciliation-design.md`.

Tasks 1/2 use one atomic implementation commit: migration39 adds the operation
kind and alert columns together, and the existing worker consumes both paths.

## Global Constraints

- `2026-10-05-modular-monolith-v1`, `2026-10-09-s39-server-pool-v1`.
- No new dependency/process/executor, no foreign SQL from adapters.
- Preserve identifiers, frozen fulfillment targets, ban and reset guards.
- Only synthetic owned fixtures. Migration39 after delivered38; PR into v2.

## Review Focus

- Inbound disappears between prepare/write: no detach of absent/unknown IDs.
- limitIP=1 and manual disabled: no limit mutation or reactivation.
- Role/binding revoked after alert claim: guarded send must skip.
- Lost attach/detach reply: same target/client and no repeated reset/create.
- Stale system target: retire only group operation, preserve unrelated work.

### Task 1: Safe preparation and one executor

Files: vpn/panel.go, new vpn/group_reconciliation.go and tests, access_worker.go,
accounts/data.go, migration00039, cmd/server/main.go, OpenAPI output enum and
generated Go/TS contract, existing ru/en status labels.
Produces: `PrepareGroupReconciliation(ctx, account) (uuid.UUID, error)`;
`ReconcileGroups(ctx) error`; `RunGroupReconciliationScheduler(ctx) error`.
Consumes: existing AccessOwner, accounts public snapshots, AccessArgs/AccessWorker.

- [x] Add failing panel and isolated DB tests for the spec's membership/ban,
  identity, unresolved-operation, offline, limitIP=1 and replay cases.
- [x] Observe RED; implement minimally and verify GREEN with race detection.
- [x] Wire one hourly scheduler and generated output kind; preserve API input.
- [x] Commit `feat(vpn): reconcile assigned group memberships` with Co-Authored-By.

### Task 2: Durable guarded infrastructure alerts

Files: accounts/infrastructure.go, notifications/client.go and group_alerts.go,
vpn/group_reconciliation.go, app/modules.go, Telegram client delivery and tests.
Consumes: `WithTelegramDelivery`, existing client outbox and notification worker.
Produces: public alert enqueue + `WithInfrastructureDelivery` guard.

- [x] Add failing tests for alert dedup, ru/en, revoked grant/binding and retry.
- [x] Add only safe alert code/target account columns in reserved39; keep
  delivery proof in existing guard and one outbox path. Verify RED→GREEN.
- [x] Deliver with Task1's atomic commit.

### Task 3: Native acceptance, review and delivery

Files: new native group reconciliation test, evidence and runbook; CI runs test
through existing full behavior suite, no second expensive fixture stage.

- [x] Exercise compiled process HTTP/River/owned TLS Bot API, startup/restart,
  alert delivery/revocation, profile mutation and stable keys/targets.
- [ ] Generate contracts; full Go race + vet, existing Python/web tests.
- [ ] Independent read-only review; fix material findings and retain evidence.
- [ ] Create/attach PR into v2, wait exact HEAD gates and migration38 ancestor,
  manual merge, cleanup own fixtures, close43 and Project Done, root handoff.
