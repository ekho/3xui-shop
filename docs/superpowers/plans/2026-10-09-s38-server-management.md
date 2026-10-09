# С38 — ограниченный план

Spec: `../specs/2026-10-09-s38-server-management-design.md`, v1, #42.
Цель: проверенный PR `feature/s38-server-management` → `v2`.

## Границы и владельцы

1. Backend specialist: `backend`, OpenAPI и generated TS schema; accounts
   role/CLI, vpn operations/queries/migration, HTTP и private Telegram adapters.
   Coupled source/tests остаются здесь. Сначала authoring contract, затем
   generation и adapters. Запрет private cross-module imports сохраняется.
2. Frontend specialist: `web/src` кроме generated schema, `web/tests/server-management.spec.ts`.
   Начинает consumer после стабильного OpenAPI; остальные web сценарии не меняет.
3. Coordinator: документы, интеграция, собственная локальная приёмка, whole-branch
   review, commit/push/PR/CI. Merge/Done/archive выполняет внешний координатор.

## Шаги и проверки

- Прочитать #42/#6/#58/#41/#55, roadmap и С39 owner ports; fetch/base/dirty check.
- Заморозить additive DTO/paths и narrower accounts permission в spec; проверить
  generator diff. Новых процессов, зависимостей или произвольного editor нет.
- Backend RED: HTTP tests на deny/allow, repeat/concurrency/input conflict,
  audit rollback; assigned/reserved/in-flight/provider/offline delete refusal,
  empty retirement/stale sync/restart. Telegram tests на private sender,
  stale proof, locale, action parser, replay и confirmation.
- Backend GREEN: только публичные module operations/owner SQL; narrow CLI
  выдачи права; atomic registry/action/audit writes, network вне pool Tx lock.
- Frontend RED/GREEN: hidden ordinary-operator navigation, list/card/add,
  ping/sync, busy refusal, confirmation и response-loss retry; ru/en,
  keyboard и accessible names/status/error. Штатный `npm run api:generate`.
- Focused: `go -C backend test -race ./internal/modules/accounts ./internal/modules/vpn
  ./internal/modules/telegram ./internal/httpapi ./cmd/server -run
  'ServerManagement|Infrastructure|ServerPool' -count=1 -timeout=15m` с test-only
  TEST_DATABASE_URL_FILE/TEST_REDIS_URL_FILE; `npm --prefix web run typecheck`;
  `npm --prefix web run test:e2e -- --grep server` по доступному runner API.
- Integration: `make -C backend generate`, TS generate, generation drift,
  `go -C backend vet ./...`, полный race suite с 60m timeout, web build/full
  Playwright и Python suite. Точный actual command/selection фиксируется в evidence.
- Actual TLS 3.7.0 fixture + реальные Go HTTP/browser consumers; simulated Bot API.
  Не перезапускать существующий cabinet-native stack чужой сессии.
- Один свежий whole-branch reviewer Astra/high; подтверждённые Critical/Important
  исправить одним проходом с regression RED→GREEN, Minor записать.
- Commit Conventional Commits + Co-Authored-By; проверить SSH remote/upstream,
  push и exact remote SHA; создать PR в v2, прикрепить к чату, дождаться source CI.
  #42 оставить открытой, Project не переводить в Done, merge/archive удержать.

## Review focus

Границы прав и Telegram proof на replay; delete race с первой выдачей;
неизвестная/недоступная панель не означает ноль клиентов; старый sync после
tombstone; ошибки audit/response loss не оставляют неучтённую mutation.
Сохраняются legacy текстовые server IDs и immutable host/assignment.
