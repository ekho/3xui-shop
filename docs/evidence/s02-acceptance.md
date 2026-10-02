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
- Локальная реализация Tasks1–7/AC1–16: **PASS**. Общий Go-race/Docker/Python
  regression и свежий review: PASS. Заключительные docs/CI commits не меняют
  проверенный product source61a9ac5; это сверяется перед завершением.
- Delivery: локальные commits в `feature/web-trial-s01`, без публикации.
- External acceptance: OPEN. Внешний SMTP/mailbox и целевой Argon2 benchmark
  предоставляет пользователь до внешней приёмки. По решению владельца
  2026-10-02 переключение живого Happ исключено из приёмки С01.

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

Свежий read-only whole-branch reviewer **Astra/high** завершил единственный
review диапазона `74c124906462f5b75a323aa9b80944dd08df2037..9b2f0a1a445692e288cd6407773db9a0b4fb2cae`.
Вердикт: **Ready Yes для завершения локальной реализации**. Critical0, Important0,
Minor0; coordinator regrading не изменил результат, fix pass не требуется.
Deferred minors: нет. Первая попытка запуска не выполнила review из-за capacity;
единственный разрешённый retry успешно запустил ту же Astra/high.

Reviewer просмотрел всю ветку в несколько проходов, включая все Ruling, С01,
SQL/API/mail/login/reset/email/rotation/logout, React, Python adapter, provision,
panel/subscription, Compose/restore/CI и реальные assertions RF1–RF5. Независимо
подтвердил15 прежних API paths/32schemas, TS-контракт и Go embedded contract после
нормализации генератором, CSP-хеш HTML/Caddy и clean exactHEAD. Полные suites
не повторял: это coordinator evidence, чьи scenarios/assertions были прочитаны.
Private runtime files/logs не открывал; сетевых/native действий не выполнял.

RF1: reset A не выдаёт cookie и сохраняет владельца B. RF2: login после hash
повторно сверяет email/hash/version под account lock. RF3: все affected recipient
locks отсортированы; SMTP не держит account lock, actual TLS race покрыт. RF4:
ротация сохраняет absolute expiry, lost response возвращает к обычному login,
restricted logout получает CSRF, pending poll отменяется до mutation. RF5:
поздний23505 обработан savepoint и commit отмены пары, restore отзывает
sessions/proofs до reconcile/serve, ownership/VPN сохраняются.

Отдельные пределы reviewer — внешняя почта/benchmark, Happ/system VPN/trust,
production/publication/remote CI/rollout, panel версии кроме3.7.0 и исключённые
из С02 сценарии — рассмотрены координатором и сохранены решениями ниже. Отсутствие
AGENTS.md/SECURITY.md в checkout зафиксировано; переданные инструкции, общая
security policy и принятые spec/plan/runbook применены. Регрессией это не признано.

Окно обслуживания, additive migrate, immutable ownership, обязательный
post-restore SQL до reconcile/serve, новый login и совместимый откат:
[runbook С02](../runbooks/s02-account-security.md). Никакого destructive Down
или двух одновременных владельцев выдачи. Новый Telegram poller не запускался;
реальные операторские кнопки ранее проверены в С01, С02 их не переключает.

Установленный Happ и настройки Mac/VPN/доверия в С02 не менялись. Docker VPN
доказывает только свой data plane. Внешняя доставляемость, целевой performance
benchmark остаются явными внешними prerequisites. Переключение живого Happ
исключено из приёмки С01 решением владельца 2026-10-02; Docker VPN proof сохранён.

## Решения Native

1. Использована существующая account-блокировка, без второго wrapper. Цена: новых различий поведения нет.
2. Restore-тест ждёт60s из-за15s lease и30s rescue. Цена: сбой восстановления определяется медленнее.
3. Убрана фиксированная численность15 API operations; проверяются все source paths и их сохранение. Цена: совместимость доказывается сравнением и HTTP checks, а не числом.
4. RF3 проверен в Task4 с настоящей парой email, вместо искусственной пары в Task1. Цена: этот gate выполнен позже в том же плане.
5. Быстрый Go reset использует существующий verified account. Цена: путь нового аккаунта и cooldown доказывает более медленный Docker.
6. Shared validation возвращает COMMON_PASSWORD вместо прежнего generic INVALID_INPUT. Цена: некоторые400 responses имеют более точный code; envelope прежний.
7. Создание proofs получает общий timestamp. Цена: внутренние callers явно передают время, публичный контракт прежний.
8. Второй fragment на proof-странице вызывает native reload. Цена: открытие новой ссылки очищает незаполненную форму.
9. Быстрый integration fixture сдвигает только собственные mail reservation scores, сохраняя часовой бюджет. Цена: фактическую минуту ожидания проверяет Docker, а не быстрый Go test.
10. У Caddy есть maintenance503 для кабинета, panel route остаётся для reconcile. Цена: флаг должен оставаться включён до успешной проверки; writers останавливаются отдельно.
11. Restore С01 теперь требует нового login того же owner. Цена: старая browser session намеренно не сохраняется, VPN сохраняется.
12. Real S01 browser запускается существующим Go fixture driver. Цена: driver отвечает за временные SMTP/API/actor prerequisites.
13. Единственный whole-branch review выполняется внутри Task7 перед завершением. Цена: последующее документальное bookkeeping проверяет координатор, без второго reviewer.

14. Внешняя SMTP-доставка и целевой Argon2 benchmark оставлены OPEN: ресурсов нет. Цена: локальный PASS не доказывает эти свойства внешнего запуска.
15. Happ и системные VPN/trust настройки не проверялись: запрет владельца сохранён. Цена: Docker не доказывает туннель установленного Happ.
16. Production, публикация, remote CI и фактический rollout вне мандата. Цена: результат готов только локально.
17. Native panel compatibility доказана только для3X-UI3.7.0, как требовал владелец. Цена: другие версии требуют собственной проверки.
18. Legacy import, linking, MFA, платежи, React-admin и операторское восстановление остаются будущими сценариями. Цена: эти функции не входят в готовность С02.
19. После clean review полные зелёные suites не повторяются: финальная проверка сравнивает все product paths с tested source61a9ac5, CI с reviewed9b2f0a1 и clean state. Цена: доказательство повторно применимо только пока это сравнение даёт no diff; любые product изменения требуют новых checks.
