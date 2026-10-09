# С44 Process Operations Implementation Plan

> **For agentic workers:** use the repository implementation-placement gate for
> the cohesive backend boundary; coordinator owns integration and delivery.

**Goal:** защищённый restart одного backend и наблюдаемая готовность/ошибки
HTTP/River/Telegram с независимым SMTP-каналом.

**Architecture:** reuse app lifecycle, Telegram State, River Stopped and existing
TLS SMTP. Operations owns only readiness and safe runtime events. No shell
endpoint, new supervisor, schema or dependency.

**Tech Stack:** Go standard library, pgx, go-redis, River, existing SMTP; Compose/Caddy.

**Spec:** `docs/superpowers/specs/2026-10-09-s44-process-operations-design.md`.

## Global constraints

- One Go process and go.mod; frontend/Caddy separate.
- Preserve `/healthz` response and PostgreSQL-only semantics.
- SMTP uses existing validated TLS sender; recipient only `OPERATIONS_EMAIL_FILE`.
- No production actions; only isolated resources owned by this session.
- Preserve unrelated changes; #42 owns server registry/infrastructure permissions.

## Review focus

- Failed Telegram startup must not falsely report running or stop HTTP.
- Simultaneous cancellation/scheduler completion must still drain HTTP.
- SMTP failure must not hang shutdown or disclose error/recipient contents.
- Readiness must return 503 while shutting down or after River stops.
- Repeated restart must preserve persisted operations and avoid a second worker.

## Task 1: coherent backend runtime boundary

**Files:** new `backend/internal/modules/operations/*.go` and focused tests;
`backend/cmd/server/main.go`, healthcheck command and tests;
`backend/internal/app/config.go`, `lifecycle.go`, their callers/tests;
`backend/internal/modules/telegram/runtime.go`, state/lifecycle tests;
`backend/internal/httpapi/api.go`, focused readiness tests, `docs/api/openapi.yaml`
only if needed by the existing contract/generated transport.

**Interfaces:** existing `notifications.SendSMTP`, Telegram `State()/Run()`,
`river.Client.Stopped()`, `pool.Ping`, `redis.Ping`. Add `server healthcheck`
for a bounded local `/readyz` request with exit 0 only on readiness success.
Expose `/readyz` as boolean/status only; safe runtime events are observed via
protected host logs/SMTP, not a new public detailed-status endpoint.

- [x] Add focused failing tests for readiness dependency/cancellation, state
  transitions/labels/recovery, SMTP failure/redaction/config and healthcheck.
- [x] Run the tests and record the actual RED cause before implementation.
- [x] Implement the smallest cohesive operations integration and update callers.
- [x] Verify the focused race matrix across all five affected packages,
  with the coordinator's test-only URL files; `go -C backend vet ./...`.
  Exact filters/results are in `docs/evidence/s44-acceptance.md`; full project
  matrix runs in CI on the final PR HEAD.

## Task 2: Compose, protected procedure and isolated acceptance

**Files:** `deploy/acceptance/compose.acceptance.yml`,
`deploy/acceptance/Caddyfile`, frontend Caddyfile only if it owns the route;
`docs/runbooks/s44-process-operations.md`, `docs/evidence/s44-acceptance.md`.

- [x] Add backend healthcheck/adequate stop grace; optional email secret wiring
  without requiring new credentials in existing fixtures. Parser validation.
- [x] Write exact protected inspection/restart/recovery commands, project/host
  checks, actor/reason journal, persisted-data warning and rollback procedure.
- [x] Launch a unique project with own PostgreSQL/Redis/TLS SMTP and compiled
  backend; validate readiness, dependency failure/recovery, notices, stop/start
  and persisted data/jobs. Never use the fixed-port shared native harness.
- [x] Run generated-code drift, focused Go race matrix and appropriate parser/static gates;
  record real evidence and limitations. Self-review plan/spec coverage.
- [x] One fresh Astra/high branch review; fix material findings and verify.
- [ ] Conventional commit with Co-Authored-By, SSH push, PR into `v2`, attach PR,
  wait for exact-HEAD CI and report to coordinator. No merge/Done/archive.
