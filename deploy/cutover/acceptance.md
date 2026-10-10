# С47: локальная приёмка

Дата: 2026-10-10. Задача [#54](https://github.com/ekho/3xui-shop/issues/54).
Контракт `2026-10-10-cutover-v2`; исходная база
`e53746c4a13d4209438012a33390ec84eb38539c`.
Implementation checkpoint: `aa559ea45df75b162a3e3f3e73d0a46d46e435a4`.
Delivery/CI/merge и окончательные SHA фиксируются в задаче и PR отдельно.

## Окружение и ресурсы

Собственный macOS/Docker fixture: Go1.27.1, PostgreSQL17, Redis, Caddy/web,
настоящие тестовые 3X-UI3.7.0/TLS SMTP и Chromium. БД, credentials, VPN clients,
PSP/Bot API ответы и SQLite snapshots синтетические. Telegram/PSP пользователя,
production, установленные VPN-клиенты и доверие сертификатам Mac не используются.
Файлы credentials/dumps/logs — вне Git, mode0600 в own mode0700 state.

Тестовые Compose projects: `cabinet-cutover-tests-9147`,
`cabinet-native-cutover-9147`, `cabinet-cutover-panels-9147`,
`cabinet-backup-5bf51f1d`. Перед cleanup проверяются полные container IDs и
project labels; удаляются только эти ресурсы. Чужой `shc-pg-test-460` не менялся.
Primary worktree остаётся на `main`/`74c1249`; работа идёт в отдельном
`feature/s47-cutover-rollback`, PR/manual merge только `v2`.

## Выполненные проверки исходников

| Проверка | Результат и доказанное свойство |
| --- | --- |
| Go exporter и 20 CLI SQLite cases | PASS: strict schema/private paths, read-only source, exact REAL precision, decimal/time/Unicode/nulls, complete packet |
| 31 retained stdlib tests | PASS, 10.604s: independent fixture/exporter/deployment tooling без Python product dependencies |
| Payments/HTTP/Telegram focused race tests | PASS: old signed paths/labels, real verified refund ID, distinct aggregate `refund_observed`, authenticated Stars actual amount, duplicates/conflicts/recurring/refunds; no synthetic entitlement |
| Migration00044 and operator report | PASS: immutable first proof, source linkage, guarded Down, operator authority, seven safe report fields; no raw provider IDs/proofs |
| cmd/server + operations race | PASS, 84.392s/32.381s: real competing serve/reconcile, SIGTERM restart, lock-loss window before new effects, offline export/report/manifest |
| Populated complete migration/backup restore | PASS, 40.860s race: persisted maintenance, late money, source replay, current native facts and full public-table digest in restored new DB |
| Generated Go/TS, Go vet, web typecheck/build | PASS: generation reproduces current files byte for byte; remaining contracts and public runtime configuration compile |
| Web E2E | PASS, 592/592: remaining user/operator, errors/empty/accessibility/ru-en scenarios |
| Native Go + actual 3X-UI/TLS SMTP | PASS, 21 tests/subtests, zero failures/skips; same operation/grant/keys after real process restart; dated reminder yields one event/mail |
| Two actual TLS panels + Chromium | PASS: server management/group reconciliation through native ports |
| Gateway/Compose smoke | PASS: single runtime, persisted current-data recovery, HTTPS/public configuration, maintenance503, eight authenticated callback routes405 on GET, retired transport404 |
| Linux panel SQLite fixture | PASS in Docker VM with network none/non-root/read-only rootfs: Go caller preserves unrelated rows and rolls back identity mismatch atomically |
| Full backup CLI rehearsal | PASS: real dump/restore, all logical/byte inventories, current migration/structure guards, SHARE snapshot with concurrent writer, role/security/failure/quarantine matrix, repeated destination protected |
| Native interrupted-operation restore | PASS: real dump with reserved grant/running River job and external panel client; old sessions/proofs rejected, same target/client/grant/VPN after reconcile |
| Public web-only operator restore caller | PASS: actual operator wrapper with empty configured/running BOT_OPERATOR_IDS and independently TELEGRAM_ENABLED=false; public decision, prior VPN bytes/connection and cleanup retained |
| Downstream account restriction transport | PASS on actual own fixture; catalogue/access/subscription consumers assert the same native Telegram guards; JS/Python syntax checked |

Один общий local Go race run на редактируемом source завершился с устаревшим
wire expectation и fresh-server compile failures во время регенерации contracts.
Wire исправлен и проверен отдельно; native scope повторён на согласованных
contracts и прошёл без ошибок. Остальные packages, включая полный HTTP scope
(2013.583s), прошли. Это не называется полным зелёным прогоном: окончательная
сборка проходит один полный principal CI на exact final source. Неизменённый
дорогой local suite повторно не запускался.

## Реальная смена rollback artifact

`checkpoint.txt` содержит конкретный совместимый commit, а не исходный `e53746c`.
`TestCutoverArtifactRollback` явно включается `RUN_CUTOVER_ARTIFACT_TESTS=1`;
principal CI включает этот профиль и получает всю Git history.

Тест собирает current и checkpoint из отдельных `git archive` с CGO off и
`-X main.sourceRevision=<exact SHA>`, сравнивает разные executable SHA256 и
read-only manifests на одной нынешней БД. При активном current PID он отвергает
unsafe baseline до остановки, проверяет retained state/HTTP callback, затем
SIGTERM/wait и запуск другого binary/PID. Прежний и новый подписанные late
callbacks после rollback обязаны сохранить первый proof, две review receipts,
все native account/order/receipt/access/source facts и ноль новых jobs.

Actual profile на `ca36eaf2f5693343c8cfea952ad33ad6e6c51bbf` прошёл: 26.960s
(29.435s race package), unsafe baseline rejected при работающем current,
retained state stable, две поздние review receipts, ноль новых jobs.

| Роль в local rehearsal | Source revision | Executable SHA256 | PID |
| --- | --- | --- | --- |
| Current | `ca36eaf2f5693343c8cfea952ad33ad6e6c51bbf` | `a0510944a65e01b9033ad663085c022a76eecba0b0d7899a7a0b720195e6521d` | 68798 |
| Compatible rollback | `aa559ea45df75b162a3e3f3e73d0a46d46e435a4` | `e9284cfa2fee1996f61f0e99292525861ceb2e70ed04afc66008cff260a5728e` | 68840 |

Эта таблица относится к собственным macOS executables. Последующий evidence-only
commit и exact final CI заново собирают current SHA; их результат и gates
фиксируются в задаче/PR без изменения runtime implementation.
Эти commit используют одинаковую business implementation. Доказательство
покрывает реальную замену artifact/process и late-money compatibility выбранного
checkpoint; оно не доказывает безопасность исходного старого payment code.
Schema fingerprint описывает live DB, поэтому произвольная старая версия не
становится разрешённым target от одного совпадения manifest.

## Review и ограничения

Независимый whole-branch reviewer проверяет staged/unstaged/untracked inventory.
Закрыты найденные проблемы: Stars actual amount, real YooKassa refund callback,
SQLite REAL precision, ранние эффекты при lock-session loss, public native restore
caller, independent empty-operator/Telegram-disabled guards, current rollback docs.
Окончательный verdict и SHA/evidence — после actual artifact profile.

Production cutover не выполнялся и требует отдельного разрешения.
Session lock и monitored3s warm-up не дают fencing зависшего/SIGSTOP/partitioned
процесса: перед takeover нужны подтверждённый stop старого writer и supervisor.
Callback retry/reconciliation и реальная конфигурация providers/bots/SMTP/panel
проверяются отдельно в разрешённом окружении. Down или старый SQLite поверх
нынешних financial facts не являются recovery.
