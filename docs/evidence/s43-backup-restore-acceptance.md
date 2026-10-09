# С43 — локальная приёмка backup/restore

Задача: [#45](https://github.com/ekho/3xui-shop/issues/45). Проверка 2026-10-09–10,
исходная `origin/v2` — `9305fa5b6a5dec44648aa9a57bb1501164e455f6`.
Это приёмка synthetic данных и работающей CLI; merge и production не выполнены.

## Изоляция и доказанный результат

`python3 deploy/acceptance/backup_restore.py up` создаёт свой случайный Compose
project, свободный subnet, PostgreSQL 17.11 и Redis, dynamic loopback ports и
private test URL files. Все аккаунты, сообщения, UUID и бинарные bytes synthetic.
Реальные Telegram/SMTP/PSP/3X-UI secrets и чужие данные не используются.

`python3 deploy/acceptance/backup_restore.py check` строит operations target
из текущего backend и выполняет настоящий pg_dump/pg_restore. Итоговый прогон
после исправлений review прошёл за 434 секунды и доказал:

- Восстановление в новую DB: schema, все table counts/content hashes и sequence
  states совпали; отдельно сверены три account identity, support FK-связи и
  SHA-256 двух BYTEA-вложений (262144 и 529 bytes).
- Конкурентный writer действительно ожидал SHARE lock в pg_stat_activity.
  Пакет сохранил предыдущую версию сообщения/bytes, а source принял запись после
  снятия lock. PG snapshot остаётся живым до окончания dump.
- Customer/unknown/revoked/restricted role не создали пакет. Повтор create/rehearse
  сохранил существующий dump/DB.
- Missing/truncated/unknown/incomplete manifest, schema/PG mismatch, dump size/hash,
  дополнительный file, mode 0644, symlink и traversal были отклонены до CREATE DB.
- Remote-only Telegram attachment вызвал BACKUP_INCOMPLETE без готового package.
  Backup и rehearsal оставили audit; запускались только migrate/backup CLI.
- Live trigger enable state и переименованный River enum вызвали SCHEMA_MISMATCH
  до CREATE DB. После возврата исходной schema обычное восстановление прошло.
- Неподдерживаемые, repeated, multi-host URL options и verify-ca/full без явного
  root certificate отклонены для source и restore до audit/package/CREATE DB.
- Failed pg_restore и failed inventory verification оставили новые DB в
  quarantine; повтор вернул TARGET_EXISTS. Ни одна failed operation не имела
  success audit. Create UUID из manifest связан с started/succeeded audit.

Проверка raw schema fingerprint выявила изменение избыточных скобок трёх CHECK
constraints при pg_restore. Source сравнивается точно, восстановленная schema
сравнивается с PostgreSQL constraint deparser. Regression test сохраняет отличие
`(A AND B) OR C` от `A AND (B OR C)`; группировка не удаляется вручную.

Независимое review выявило два пробела: fingerprint пропускал trigger enable state
и текущий River enum; pgx/libpq не сохраняли все принятые URL options. Оба исправлены
с focused RED/GREEN checks. Hash теперь включает trigger enable state и ordered
enum labels. Connection contract отклоняет неподдерживаемые/repeated/multi-host
options и ambient settings до побочных действий. Final real rehearsal включает
эти проверки; affected race tests прошли. Актуальный полный Go/race прошёл на
свежем собственном fixture. Исторический funding guard FAIL и неизвестная причина
сохранены ниже; новый PASS не доказывает устранение того сбоя.

## Другие локальные проверки

Все Go/Python connected tests используют private TEST_DATABASE_URL_FILE и
TEST_REDIS_URL_FILE собственного fixture. Значения URL в evidence не включаются.

| Проверка | Результат |
| --- | --- |
| `go -C backend test ./internal/modules/operations ./cmd/server ./internal/modules/support ./db -count=1` | PASS на замороженном source, 73 секунды |
| `go -C backend test -race ./internal/modules/operations ./db -run 'Test(ReadPackage\|BackupSchema)' -count=1` | PASS |
| `go -C backend test -race ./internal/modules/operations ./cmd/server -count=1` | PASS после final URL root-certificate guard, 54 секунды |
| `go -C backend vet ./...` | PASS |
| `python3 deploy/acceptance/check_names.py`, `git diff --check` | PASS |
| `make -C backend generate`, `npm --prefix web run api:generate` | PASS; generated files без diff |
| web typecheck, build `--mode test`, runtime-config test | PASS |
| `npm --prefix web run test:e2e` | PASS: 541 tests, 6.0 минут |
| `poetry run python -m unittest discover -s tests -v` | PASS: 112 tests |
| `go -C backend test ./... -count=1 -race -timeout=40m` | FAIL: HTTP API общий timeout; restore-test требовал fixed port 55491; остальные Go packages PASS |
| `go -C backend test ./internal/httpapi -count=1 -race -timeout=60m` | FAIL за 27.7 минут: TestPlanChangeFundingGuards/heleket, plan_change_test.go:740; timeout не достигнут |
| `go -C backend test ./tests -run '^TestWebTrialBackupRestore$' -count=1 -race -timeout=10m` | PASS: реальный restore с private owned-container metadata, 43 секунды |
| `go -C backend test ./tests -run '^Test(OwnedPostgresFixtureGuard\|PrivatePostgresFixtureMetadata)$' -count=1 -race` | PASS: identity/ownership/port/metadata rejection guards |
| `go -C backend test ./... -count=1 -race -timeout=60m` | PASS: 17 packages с тестами, exit code 0; 1512.603 секунды, HTTP API 1509.675 секунды, connected tests 338.882 секунды |

В stack trace 40-minute HTTP timeout выполнялся Argon2; отдельный assertion/race
failure не зафиксирован. Для повторного прогона увеличен только test timeout;
параметры password hashing и проверки не менялись. Старый TestWebTrialBackupRestore
отклонил собственный dynamic port до dump. Теперь opt-in private container
metadata проверяет project/service/loopback binding; существующие
dump/restore/auth/migration/job-rescue assertions прошли без изменения.
После небольшого исправления source naming повторены только pure guards;
connected сценарий не повторялся без нового входа.

HTTP API повтор завершился с одним combined funding assertion, `err=nil`.
Payments/httpapi/app/wire source не имеет diff относительно исходной базы.
Один bounded diagnostic через private Go overlay добавил только selected synthetic
status/counter observations, сохранив исходный Fatal predicate. Команда
`go -C backend test -overlay=<private-file> ./internal/httpapi -run
'^TestPlanChangeFundingGuards$/^heleket$' -count=3 -race -timeout=5m -v`
дала три PASS: paid/needs_review/review=true/access absent, panel counters не
изменились при funding. Второй bounded diagnostic проверил гипотезу о влиянии
предыдущих provider subtests: тот же overlay, `-run '^TestPlanChangeFundingGuards$'
-count=2 -race -timeout=5m -v`, все пять методов в двух циклах. Оба цикла прошли
(package 19.777 секунды), гипотеза не подтвердилась. Файлы repository не менялись.
Эти результаты не закрывают FAIL полного пакета: исходное несовпавшее поле
неизвестно, root cause и связь с backup не установлены. Финансовое поведение и
assertions не ослаблялись. На том checkpoint полный Go оставался FAIL, новый общий
прогон не запускался без изменённого input. Дальнейшая диагностика причины старого
сбоя требует новых данных исходного failure context.

По отдельному мандату головного чата выполнен один полный race-прогон текущего
замороженного source с общим wall budget 60 минут. Новый input: свежий собственный
изолированный PostgreSQL/Redis fixture без прежних test DB, проверенные Poetry
Python 3.13.16 / aiogram 3.22.0 и импорт contract tests; перед стартом других
локальных Go-процессов не было. Все три private files и фактические
project/container/service/healthy loopback binding сверены. Команда использовала
`-count=1`, без результата из cache и без overlay; `RUN_BROWSER_TESTS=0`.
Полный набор завершился PASS за 25 минут 13 секунд. Production source, финансовый
код и исходный assertion не менялись. Это актуальная проверка локального delivery
gate, не доказательство причины или устранения исторического Heleket FAIL.

Для Python использована локальная 3.13 согласно CI/lockfile, без изменения
зависимостей. Web build выдал существующие Vite предупреждения о directives и
размере chunks; сборка завершилась успешно.

## Review, CI и cleanup

Независимое whole-branch review одобрило product source staged tree
`23423147dcbc643a862b6ce4cf00ac946fd94c6c` после двух исправлений выше. Добавляемый
test-fixture adapter и его runbook/evidence одобрены отдельным delta review
для tree `576b582a154d5d25b94c13c39bb740bd80ae0643`. Существенных findings нет;
reviewer самостоятельно сверил connected и guard race logs. Product source
между этими review не менялся. Проверки CI этим review не подтверждаются.

Push/PR и CI окончательного HEAD отложены по сообщению головного чата:
общий Docker Hub anonymous HTTP 429 имеет owner [#98](https://github.com/ekho/3xui-shop/issues/98)
и fix PR [#99](https://github.com/ekho/3xui-shop/pull/99). Read-only сверка
2026-10-09 22:16 UTC: #98 и PR #99 OPEN, PR head
`e810e4c9c528545500226e0608d99e0f76bf48ed`, три Image checks FAILURE, два
Platform checks IN_PROGRESS. Recovery ещё не подтверждён; собственные повторные
expensive GitHub runs без owning fix не запускались.
В последующем мандате головной чат сообщил об исправлении owning source в
`e810e4c` и продолжающихся final-head runtime checks. Это не подтверждение recovery
со стороны данного чата; push/PR требуют отдельной команды головного чата.
В Platform checks сохранены все прежние Go/generated/web/Python/image/native
scopes и добавлен отдельный backup rehearsal с always-cleanup.

Головной чат сообщил о test integration overlap с #44: его commit
`1b2e4105615a39850d137a206de48e50335db20f` тоже меняет container prerequisite в
`backend/tests/web_trial_integration_test.go`. Здесь этот caller использует
`backend/tests/postgres_fixture_test.go`. Сведение в один безопасный adapter
при доставке принадлежит головному чату; второй интерфейс не добавляется.
Текущий собственный fixture требует private canonical 0600 files
`TEST_DATABASE_URL_FILE`, `TEST_REDIS_URL_FILE`, `TEST_POSTGRES_FIXTURE_FILE`;
`backup_restore.py up` создаёт все три. После интеграции нужно повторить
affected guard/restore assertions; нынешняя local acceptance остаётся evidence
для текущего source, не для ещё не выполненного объединения.

После завершения проверок выполнен
`python3 deploy/acceptance/backup_restore.py down`. Удалены два собственных
containers, одна network и один volume; для точного Compose project осталось
ноль ресурсов. Сверка до/после не выявила удаления ни одного чужого существовавшего
container/network/volume. Удалены обе собственные private fixture copies и
уникальный operations image; shared PostgreSQL/Redis images не удалялись.
Для следующего connected check fixture нужно создать заново через `up`.

После нового полного race-прогона свежий fixture также удалён: два собственных
containers, одна network, один volume и private files. Остаток своего project —
ноль; сверка до/после снова не обнаружила потерянных чужих существовавших ресурсов.
Shared images сохранены.

Implementation готова и review одобрена; delivery и acceptance остаются pending.
Текущая full local Go verification пройдена и разрешает scoped local commit.
Push/PR остаются на паузе до отдельной команды головного чата после #98.
Интеграция adapter с #44 и дальнейшее решение по историческому funding failure
принадлежат головному чату; backup scope не расширялся.

## Границы

Полный native `local.py` и RUN_BROWSER_TESTS=1 backend scope локально не запущены;
они остаются обязательными в CI, отдельно от локального PASS. Нельзя считать
typecheck или изолированный backup доказательством всех этих scopes.

Production, реальные данные, удалённые медиа и live cutover не проверены.
Redis, mounted secrets/config/certificates и внешние состояния провайдеров
не входят в DB package. Remote-only bytes отклоняются, а не считаются сохранёнными.
SHA-256 проверяет повреждение доверенного package, не автора произвольного dump.

Table locks приостанавливают writers на время копирования. Неполный private
directory может быть виден во время create, но reader его отвергает. Failed
restore DB сохраняется изолированной; её нельзя подключать к serve/reconcile.
Старые auth/proofs/jobs сохранены для сверки; дальнейший перенос требует отдельной
авторизации и runbook С45–С47. Unknown UUID не может записаться в account-backed
audit FK; отказ остаётся стабильным CLI code. Известные actor failures журналируются.
