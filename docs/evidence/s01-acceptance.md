# С01 — evidence и незавершённая реальная приёмка

Дата локальных проверок: 2026-10-01. Ветка: `feature/web-trial-s01`.
Base: `74c124906462f5b75a323aa9b80944dd08df2037`.
Tasks1–7: `44278644d147edb692acee327378f8356215c3d5`; Task8 readiness — подготовлена в локальном checkpoint этой ветки. Полная спецификация: [S01](../superpowers/specs/2026-10-01-s01-web-trial-design.md).

**Implementation:** Tasks1–7 локально реализованы и проверены; Task8 readiness
подготовлена, независимый review ещё ожидается.
**Delivery:** локальные commits, без push/PR/merge/remote CI/deploy.
**Acceptance:** OPEN — владелец предоставит test panel/SMTP/bot/operator позже.
Фактический VPN и restore на реальной панели не проверялись.

## Проверенные локальные команды

URL-file prerequisites и setup: [runbook](../runbooks/s01-test-rollout.md).
Все команды используют свои случайные test DBs, не production.

| Проверка | Результат |
| --- | --- |
| `make -C backend generate`, TS `api:generate`, generated no-diff | PASS |
| `go -C backend vet ./...`, web `typecheck`/`build --mode test` | PASS |
| `make -C backend test-integration` (race) | PASS; Task8 integration/restore отдельно повторены с race |
| Python `unittest discover -s tests -v` | PASS, 98 tests |
| Web mock `test:e2e` | PASS, 10 tests |
| `S01_E2E_MODE=real npm run test:e2e` из web | PASS, real HTTPS API/PG/Redis/River/Python + browser; external services fixtures |
| `TestS01BackupRestore` | PASS, actual local pg_dump/restore, HTTPS panel fixture |
| Docker builds (backend/web/bot) | PASS, local ARM64 images, no publish |
| Compose config / container smoke / bounded rollback | PASS, disposable local project, real TLS validation |
| Independent whole-branch review | Pending |

Tool versions: Go1.27.1, Node24.11.1, Python3.13, Poetry2.5.1, Chromium153
(Playwright1.63.0); React19.3.0, Vite8.3.2, TS5.9.3. Official container digests
закреплены в Dockerfiles/Compose. Измерение Argon2id на local Apple M3 Pro:
`BenchmarkPasswordHash`50.2ms/op,19,926,727B/op,32allocs/op. Hash parameters
19456KiB/2/1, максимум два параллельных вычисления. Замер на целевой test машине
остаётся pending; это измерение не гарантирует её latency.

## AC — статус автоматизации и реальных проверок

| AC | Автоматическое доказательство | Статус / остаётся |
| --- | --- | --- |
| 1 no-Telegram → trial → VPN | Real API browser + Python actor + concrete panel HTTP fixture | Local PASS; реальный Happ connection pending |
| 2 bot down before decision | `TestS01FlowAndFailures`, durable card exists before consumer start | Local PASS; real delivery pending |
| 3 bot down after approve | Worker starts after Python consumer exits, grant applied without bot | Local PASS; real bot stop pending |
| 4 scanner/duplicate/parallel verify | registration tests; browser fragment cleared, no automatic POST | Local PASS |
| 5 token/code TTL/reuse/brute force | registration tests with controlled clock | Local PASS |
| 6 Origin/CSRF/owner/key privacy | HTTP boundaries, key tests, real browser private ingress404/no-store/logout | Local PASS |
| 7 request/idempotency duplicates | trial atomicity tests + stable browser retry key | Local PASS |
| 8 forged actor/anonymous/old callback | handler tests, actual parsed aiogram Update through HTTPS client | Local PASS; real operator identity pending |
| 9 concurrent decisions/lost reply | PG decision tests, repeated real consumer callback, internal-only winning409 | Local PASS |
| 10 reject final/account unchanged | trial tests + mock browser reject/support | Local PASS |
| 11 support reconsider/old card/used guard | trial/FSM tests reason+stable confirmation key | Local PASS; actual operator interaction pending |
| 12 panel down/no regular/bad config | provision/panel/config tests | Local PASS; real preflight pending |
| 13 interrupted panel add/restart | lost reply1create, apply rollback/reconcile, physical PG session loss | Local PASS; real panel restore pending |
| 14 partial/foreign/no hop | concrete panel partial attach/preserved protocol fields/mismatch tests | Local PASS; real API compatibility pending |
| 15 immutable config/bytes/N+1 | snapshot/zero/overflow/expiry provisioning tests | Local PASS |
| 16 duplicated card/edit fail | lease/ack tests + actual Python claim/complete replay + transport loop tests | Local PASS; real Telegram edit pending |
| 17 legacy/unknown groups | SQL cohort separate, no legacy apply call, fixture rejects other writes/groups | Local PASS; panel isolation attestation pending |
| 18 logout/TTL/keyboard/ru/en/error | session tests +10 browser tests; inspected375/1280 screens | Local PASS |
| 19 PG restore retains operation | real local dump after external add before applied; same target/grant/session | Local PASS with fake panel; real panel restore pending |

## Внешние prerequisites и владелец

Владелец проекта предоставляет выделенную panel/client-centric version и
credentials, SMTP/test mailbox, отдельный bot и operator, DNS/HTTPS certs,
опубликованные policies/support, secret-file paths и их readable UID/GID.
До этого приёмка не закрывается. Test panel version: pending. Happ version:
pending. Реальная duplicate uniqueness guarantee: pending (config false).
CI workflow только проверяет, не публикует images и не вызывает deploy;
существующий main/tag Docker Publish workflow не менялся.
