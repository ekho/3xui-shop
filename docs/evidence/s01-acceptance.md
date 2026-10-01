# С01 — evidence и незавершённая реальная приёмка

Дата локальных проверок: 2026-10-01. Ветка: `feature/web-trial-s01`.
Base: `74c124906462f5b75a323aa9b80944dd08df2037`.
Проверенная ревизия кода и запуска: `0ce85feea5aff8165773121e0e5b9eadab438e06`.
Полная спецификация: [S01](../superpowers/specs/2026-10-01-s01-web-trial-design.md).

**Implementation:** Tasks1–7 локально реализованы и проверены; локальная подготовка
Task8 проверена. Независимое ревью выполнено; четыре Important исправлены
с проверками RED→GREEN и полным зелёным набором. Два Minor отложены ниже.
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
| `go -C backend test ./... -race -count=1` | PASS после исправлений ревью, включая running-job dump/restore |
| Python `unittest discover -s tests -v` | PASS, 102 tests |
| Web mock `test:e2e` | PASS, 10 tests |
| `S01_E2E_MODE=real npm run test:e2e` из web | PASS, real HTTPS API/PG/Redis/River/Python + browser; external services fixtures |
| `TestS01BackupRestore` | PASS, actual local pg_dump/restore с running River job, HTTPS panel fixture |
| Docker builds (backend/web/bot) | PASS, local ARM64 images, no publish |
| Compose config / container smoke / bounded rollback | PASS, disposable local project, real TLS validation; restore runtime обрабатывает provision, оставляет mail без попыток, не открывает HTTP |
| Independent whole-branch review | Проверен диапазон `74c1249..a6ca9d0`; Critical0, Important4 исправлены автором с RED→GREEN, Minor2 отложены. Повторного ревью не проводилось |

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
| 16 duplicated card/edit fail | lease/ack tests + Python claim/complete replay; delayed decision, deleted-card replacement, unchanged-card ack, lost replacement reply | Local PASS; real Telegram edit pending |
| 17 legacy/unknown groups | SQL cohort separate, no legacy apply call, fixture rejects other writes/groups | Local PASS; panel isolation attestation pending |
| 18 logout/TTL/keyboard/ru/en/error | session tests +10 browser tests; inspected375/1280 screens | Local PASS |
| 19 PG restore retains operation | real local dump after external add before applied, running River job; штатный rescue, same target/grant/session | Local PASS with fake panel; real panel restore pending |

## Исправления независимого ревью

1. Dedicated acceptance entrypoint больше не падает на неинициализированном
   `IsDev`: свежий процесс проверяет разрешённого и постороннего оператора.
2. Очередь доставки — единственный writer карточки. Запоздалый ответ approve
   не переписывает уже доставленную `needs_review` и её кнопку сверки.
3. После удаления карточки отправляется замена с актуальными кнопками и новым
   message ID. «Message is not modified» подтверждает прежнюю карточку;
   потерянный ответ замены оставляет lease неподтверждённой.
4. Поставляемый режим `reconcile` запускает только provision queue. Docker smoke
   проверяет running-job rescue, отсутствие HTTP и untouched mail; dump/restore
   использует настоящий River worker. В тесте сдвигается только `attempted_at`
   для ожидания порога, state/operation/grant/target сохраняются.

## Зафиксированные решения

| Решение | Основание | Цена ошибки |
| --- | --- | --- |
| Продолжение означает реализацию и локальные commits; публикация отдельно | Подготовка плана завершена, пользователь поручил продолжать | Обратимые локальные изменения |
| Реальная приёмка остаётся открытой без production substitute | Пользователь предоставит ресурсы позже | Пока нельзя закрыть С01 |
| DecisionResult сохраняет card и delivery_state | Полная спецификация важнее сокращённой сигнатуры плана | Дополнительные внутренние поля |
| Implicit-owner key endpoint без готовой операции возвращает409; чужой resource path404, account_id query400 | В принятом API нет параметра владельца/ресурса для примера404 из плана | Клиент должен обрабатывать409 |
| compose.acceptance.yml отделён от compose.test.yml | Обязательные внешние secrets не блокируют fixture tests | Дополнительный manifest и дублирование image pins |
| Реальные SMTP/Telegram/panel version/uniqueness/Happ/VPN/restore/target performance пока не оценены | Владелец отложил ресурсы; fixtures не доказывают эти свойства | Несовместимость выяснится при реальной приёмке |
| Production, shared legacy panel, publication и remote CI не оценены | За пределами локального мандата | Нет доказательства поведения в этих средах |
| Payments/import/MiniApp/web-admin/password recovery не входят в С01 | Явно отложенные сценарии | Эти пути недоступны в С01 |
| River rescueAfter=3min вместо default1h | Больше worker timeout125s, совпадает с operation lease; штатный механизм River | При увеличении worker timeout порог тоже нужно пересмотреть; иначе возможен повтор активной задачи, физическая блокировка защищает выдачу |

## Отложенные Minor

- Logout уже ограниченного аккаунта возвращает403 до отзыва текущей сессии и
  удаления cookie. Обычный logout проверен; доступ ограниченного аккаунта закрыт.
- Текст регистрации ru/en утверждает отправку письма после202 enqueue,
  когда SMTP delivery ещё не подтверждена. Требуется формулировка о принятии запроса.

## Внешние prerequisites и владелец

Владелец проекта предоставляет выделенную panel/client-centric version и
credentials, SMTP/test mailbox, отдельный bot и operator, DNS/HTTPS certs,
опубликованные policies/support, secret-file paths и их readable UID/GID.
До этого приёмка не закрывается. Test panel version: pending. Happ version:
pending. Реальная duplicate uniqueness guarantee: pending (config false).
CI workflow только проверяет, не публикует images и не вызывает deploy;
существующий main/tag Docker Publish workflow не менялся.
