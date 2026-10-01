# С02: локальная приёмка управления входом и сессиями

Дата: 2026-10-02. Мандат: принятые [спецификация](../superpowers/specs/2026-10-02-s02-account-security-design.md)
и [план](../superpowers/plans/2026-10-02-s02-account-security.md), Native, локальная
реализация и один свежий whole-branch review. Push/PR/merge/release/production
не входят в этот этап. [Приёмка С01](s01-acceptance.md) остаётся OPEN.

## Ревизии и состояние

- База проверенной реализации С01: `333c96bb11789707cc406b9f2bb55f6e60015aaa`.
- Product source Tasks1–6: `61a9ac533c5b792d793ecec151553666d2a0e8d5`.
  Генерация Go/sqlc/TypeScript повторена без diff; все15 прежних source paths,
  operations и schemas совпадают с базой С01, теперь24 операции.
- Локальные целевые проверки Tasks1–6: PASS. Общий Go-race/Docker/Python regression: PASS; свежий review:
  **ожидается**. Итоговый статус обновляется только после их результата.
- Delivery: локальные commits в `feature/web-trial-s01`, без публикации.
- External acceptance: OPEN. Внешний SMTP/mailbox и целевой Argon2 benchmark
  предоставляет пользователь до внешней приёмки. Happ tunnel check запрещён.

## Матрица локальных доказательств

Ниже — выполненные целевые проверки; свежий общий regression указан отдельно.
Каждый Go fixture использует собственную новую PostgreSQL БД и Redis namespace.
Проверки отказов — assertions на HTTP/domain result, не наличие исходного кода.

| AC | Доказательство |
| --- | --- |
| 1 | `TestPasswordReset`, `TestPasswordResetQueueFailure`: known/unknown202 и503, dummy challenge/job без SMTP/создания account; общие IP/mail limits |
| 2 | Token/code reset, все старые sessions401, old password401/new login200; `TestS02ResetFlow` с actual API/River/TLS SMTP; Docker browser без auto-login |
| 3 | `TestCredentialProofBoundary`, reset/browser cases:30min token/10min code/5 guesses, replacement/replay/purpose; GET страницы не подтверждают |
| 4 | `TestPasswordChange`, `TestPasswordChangeSharedBudget`, HTTP/browser: current password, Unicode/common password, new ID/CSRF, прежний absolute expiry |
| 5 | `TestEmailChange`, `TestEmailChangeHTTP`: оба порядка, token/code и разные cookies; Docker два actual TLS Mailpit mailbox/context, обычный login новым email |
| 6 | Pair cancel/expiry/replacement, включая first-confirmed; revoke-others сохраняет pending; нет обхода старой почты, UI объясняет обращение в поддержку |
| 7 | Busy target, registration race и actual PostgreSQL23505 late conflict: commit отмены всей пары, старый email/владелец сохранены; freed email получает новый account/VPN identity |
| 8 | `TestCredentialConcurrency`, pending-target actual TLS SMTP races: locked credential revalidation после hash, общий порядок account/affected email/proof/mail, stale login401 и общий budget |
| 9 | `TestRevokeOtherSessions`: old current+other sessions401, fresh current ID/CSRF, password/version и first-confirmed pair сохранены; HTTP foreign IDs отказали |
| 10 | `TestRestrictedLogout`/HTTP и browser restricted reload: CSRF-only context, logout при Redis outage, cookie/session удалены; reset не снимает restriction |
| 11 | Actual HTTP A-proof/B-cookie: B owner/session сохраняются, no Set-Cookie/auto-login; purpose/foreign account/target IDs отказали |
| 12 | Queue/Redis/SMTP outages и actual TLS retry, missing delivery после202 не обещается; UI429/503/lost response не повторяет POST и восстанавливает управление обычным login |
| 13 | `TestCredentialMailIsolation`/mail regressions: cancellation scoped по kind/purpose, revoked/expired не отправляются, security notices и registration jobs сохраняются |
| 14 | `TestS02AccountSecurityFlow`: granted account до/после каждого действия, account/Grant/Operation/trial-used/VPN/subscription/limits совпадают; positive panel-counter baseline и отсутствие новых calls; Docker native3X-UI3.7.0 readback |
| 15 | `TestAccountSecurityMigration`, `TestAccountSecurityRestore`, обновлённый `TestS01BackupRestore`; реальный Docker dump/restore с running job/reserved grant, maintenance дважды до reconcile/ingress, old cookie/proofs отказали/new owner login |
| 16 | Generated contract без diff; strict body/query/Origin/CSRF/no-store HTTP tests; RU/EN/mobile375px/keyboard/labels/aria-live/fragment browser cases, включая same-path второй proof и delayed old-cookie poll401 |

## Выполненные проверки и безопасные результаты

- Task1 полный `go test ./...`: PASS63.012s; Tasks3/4 Service/HTTP race bundle:
  PASS47.659s/10.320s. Task5 actual API/River/TLS SMTP account-security flows:
  PASS6.813s. Последующий общий regression не заменяется этими ранними runs.
- `make -C backend generate`, `npm --prefix web run api:generate`, diff generated
  source, `make -C backend vet`, frontend typecheck/build и diff whitespace: PASS.
- `npm --prefix web run test:e2e`:29/29 PASS9.1s —10 прежних С01 и19 С02.
- Task5 actual Docker `node deploy/s02/browser.mjs all`: PASS133.552s на финальном
  frontend Tasks1–5, native3X-UI3.7.0 и Mailpit1.31.1 с закреплёнными digest.
- Task6 actual Docker `python3 deploy/s01/local.py restore`: PASS167.221s.
  Кабинет503 до dump; тот же SQL дважды возвращает только counts, второй раз0/0/0;
  старые cookie/reset/оба email proofs отказали, новый login того же owner200;
  прежний target, один client/Grant и собственный Docker VPN сохранены.
- `TestAccountSecurityRestore`: PASS1.824s после commit61a9ac5;
  `TestS01BackupRestore`: PASS32.905s с реальным pg_dump/pg_restore, TLS panel
  fixture и новым login. Расширение deadline60s учитывает15s leader lease и30s
  River rescue interval; actual observed recovery≈35s.

- `S01_RUN_BROWSER=1 make -C backend test-integration` с private URL_FILE для
  собственного PG/Redis: PASS109.239s (`tradeos-check-4kjI7B`), все Go packages
  с race detector; driver создаёт реальные HTTP/River/TLS SMTP/Python actor
  prerequisites для existing real S01 browser, без environment skip.
- Финальный `node deploy/s02/browser.mjs all` после restore и нового gateway:
  PASS145.186s (`tradeos-check-WfU8TC`), обычный кабинет после снятия maintenance,
  actual native3X-UI3.7.0, два mailbox и неизменные owner/key/target/limits.

- `poetry run python -m unittest discover -s tests -v` с теми же test-only
  PG/Redis URL_FILE:102/102 PASS10.393s (`tradeos-check-C6wCmQ`). Первая попытка
  без URL_FILE дала один prerequisite failure; он исправлен параметрами запуска,
  а не обходом или изменением теста. Полные логи private; сюда переносится только redacted verdict.
Тестовые passwords/tokens/cookies/CSRF/email/VPN UUID/subId/URLs/dump не публикуются.

## Review, решения и rollout

Свежий read-only whole-branch reviewer Astra/high: **ожидается**. После результата
будут сохранены диапазон review, regrading, один TDD fix pass для Critical/Important
и deferred minors. Native решения сохранены в итоговом отчёте до удаления scratch.

Окно обслуживания, additive migrate, immutable ownership, обязательный
post-restore SQL до reconcile/serve, новый login и совместимый откат:
[runbook С02](../runbooks/s02-account-security.md). Никакого destructive Down
или двух одновременных владельцев выдачи. Новый Telegram poller не запускался;
реальные операторские кнопки ранее проверены в С01, С02 их не переключает.

Установленный Happ и настройки Mac/VPN/доверия в С02 не менялись. Docker VPN
доказывает только свой data plane. Внешняя доставляемость, целевой performance
benchmark и ограничения приёмки С01 остаются явными внешними prerequisites.
