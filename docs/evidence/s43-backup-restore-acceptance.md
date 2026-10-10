# С43 — локальная приёмка backup/restore

Задача: [#45](https://github.com/ekho/3xui-shop/issues/45). Проверка 2026-10-09–10,
исходная `origin/v2` — `9305fa5b6a5dec44648aa9a57bb1501164e455f6`.
Это приёмка synthetic данных и работающей CLI; С43 ещё не слита, production не выполнен.

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

Общий Docker Hub anonymous HTTP 429 имел owner
[#98](https://github.com/ekho/3xui-shop/issues/98). На прежнем checkpoint
[PR #99](https://github.com/ekho/3xui-shop/pull/99) был draft с failed Image jobs;
повторы без owning fix не запускались. Recovery теперь подтверждён live:
проверенный source `b320f8eef415bb48e4c1cebdb12409b143eb9501` merged как
`376df1eeaa0ea825f85e24d776aa46aee132e576`; его
[post-merge preview](https://github.com/ekho/3xui-shop/actions/runs/38005580860)
завершился SUCCESS. Это подтверждает owning fix, но не CI будущего HEAD #45.
Новый мандат разрешает этой сессии push/PR, собственный ручной merge в `v2`,
закрытие issue и Project Done после gates. Preview GHCR/prerelease разрешён,
production и auto-merge не разрешены. Root координирует и архивирует.
В Platform checks сохранены все прежние Go/generated/web/Python/image/native
scopes и добавлен отдельный backup rehearsal с always-cleanup.

Интеграция общего restore fixture с #44 принадлежит этой сессии #45.
Прочитаны минимальный `1b2e4105615a39850d137a206de48e50335db20f` и actual
`7e7cf856a274d99c5f23433c0c662feb4d26a731` source #44. До изменения записан
[совместимый контракт](https://github.com/ekho/3xui-shop/issues/45#issuecomment-6091389100)
`2026-10-10-owned-postgres-restore-v1`, связанный с #44. Один helper сохраняет
существующие private metadata и optional Compose inputs с общими ownership,
identity и точными loopback/port guards. Все restore assertions сохраняются;
запрет downgrade проверяется `DownTo(ctx, 34)`. Технический порядок:
#42 → #44 → #45 → #47; полная несмерженная ветка #44 в #45 не переносится.
До дальнейшей правки записано
[дополнение v2](https://github.com/ekho/3xui-shop/issues/45#issuecomment-6091458915):
backup constructor теперь `operations.NewBackup`, чтобы совместиться с
`operations.New` maintenance #44 без изменения его callers или поведения.
Текущий собственный fixture использует private canonical 0600 files
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

Исходная implementation и full local Go verification прошли; локальный commit
`69bf87505f45aacde47f70e32075b5a7c77375a7` сохранён. Изменённый общий fixture
требует focused checks и delta review; delivery и acceptance остаются pending
до CI окончательного HEAD и merge в `v2`. Разрешённая автоматическая
preview-публикация идёт отдельно и не добавляет gate к исходному DoD.
Исторический funding failure сохраняет неизвестную причину; финансовое
поведение и исходный assertion не меняются в backup scope.

## Интеграция текущего v2

При интеграции merged `376df1eeaa0ea825f85e24d776aa46aee132e576` сохранены
readiness/lifecycle/shutdown и owning CI cache fix. Backup dispatch остаётся
до конфигурации и инициализации runtime. Все прежние CI scopes сохранены;
backup rehearsal добавлен отдельно. Независимый review обнаружил, что ошибка
нового первого cleanup могла пропустить прежние cleanup из-за shell `-e`.
Теперь backup, native acceptance и test services имеют отдельные `always()`
steps; YAML self-check проверил их независимость.

После интеграции прошли: cmd/server, operations, app и Telegram `-race`
(четыре пакета, 18 секунд); HTTP readiness `-race` (3.186 секунды);
10/10 CI script tests; workflow YAML / BuildKit TOML / producer syntax;
semantic naming и diff-check. После переименования constructor повторены
только cmd/server и operations `-race` — PASS.
Metadata-only real restore вместе с пятью fixture guard tests прошёл
с `-count=1 -race`: 41.941 секунды. Отдельные real restore прогоны с
Compose-only и двумя согласованными входами прошли на текущем source:
37.204 и 37.212 секунды. Во всех трёх случаях сохранён полный restore
test, включая downgrade, replay, reservation, running job и повторный login.
Неизменённые дорогие полные локальные Go/web/Python scopes не повторялись;
окончательный HEAD требует полного CI.

### Интеграция доставленной #42

Вторая интеграция использует actual merge #42
`a6ef9bf3dab4407dcf664dda04c93619f549deb8`. Сохранены infrastructure CLI и все
новые server-management CI scopes. Restore consumer сохраняет новые assertions
о retained reservation, успешном откате к migration 36 и выборе её guard;
следующий запрет отката проверяется через `DownTo(ctx, 34)`. После отказа
дополнительно проверяется, что версия осталась 36: одинаковая строка ошибки
guard 35 не должна скрыть ошибочно успешный откат 36.
Backup dispatch по-прежнему выполняется до runtime/config.

После разрешения конфликтов cmd/server и operations `-race` прошли за 8.822
и 4.194 секунды. Полный `TestWebTrialBackupRestore` на новом source прошёл
за 55.20 секунды вместе с fixture guards; собственный fixture удалён,
остаток resources — ноль. YAML self-check сравнил все именованные CI scopes
с actual #42 и подтвердил четыре независимых `always()` cleanup.
Delta review выявил потерю специфичности guard при `DownTo(34)`;
после добавления проверки версии полный real restore повторён с `-race`
и прошёл за 39.91 секунды. Собственный fixture снова удалён без остатка.
Окончательный consumer #44 проверяется после её actual merge;
несмерженная ветка #44 не переносится, полный CI нового HEAD ещё требуется.

### Интеграция доставленной #44

Последняя интеграция использует только actual merge #44
`9a078b35aac2cf2d96fcf2b307ffbf1913e8d009`: его tree совпадает с проверенным
final source `0d062ad83de803b07447ecda2bf611653167ff4a`. Добавлена доставленная
migration 38; собственных migrations С43 нет. Единственный конфликт — выбор
PostgreSQL fixture в restore consumer. Вызов `controlledPostgresContainer`
сохраняет проверку ownership/container/loopback port для metadata, optional
Compose и согласованных двух входов. Все новые assertions #42/#44 сохранены,
включая reservation, `DownTo(36)`, выбор guard и отказ `DownTo(34)`; после отказа
версия дополнительно обязана остаться 36. Полный consumer остался идентичен
ранее проверенному blob `858068ecc0d67202306faee3918e102567500d6d`.

На объединённом tree `ced49e364d65b88b1ea7fa2344d5393afcdf5a5e`:

- cmd/server, operations, db, app и Telegram `-count=1 -race` — PASS за
  44.50 секунды. `TestServerManagementMigrationEmptyRollbackAndHistoryGuard`
  прошёл за 1.51 секунды и действительно выбирает rollback до 36.
- Полный `TestWebTrialBackupRestore` — PASS за 37.54 секунды. Вместе с пятью
  metadata/Compose/selector/ownership guards весь scope прошёл за 44.35
  секунды, без skip. Restore включает migration 38 и повторный migration replay.
- Собственный fixture `cabinet-backup-8ff7818b` и private files удалены;
  остаток принадлежащих ему containers/networks/volumes — ноль.
- Все проверяющие CI stages actual #44 сохранены без изменения. Команды её
  общего Cleanup сохранены в отдельных `always()` steps; с backup cleanup
  их четыре. 10/10 CI script tests, naming, YAML/TOML/producer syntax и
  diff-check — PASS.

Независимое native review неизменённого backup core не нашло замечаний.
Inventory перечисляет все public ordinary/partitioned tables и копирует все
columns/rows; pg_dump не фильтрует бизнес-виды или таблицы. Новые maintenance
и другие additive public DB facts входят в package этим общим механизмом.
CLI `NewBackup` и runtime maintenance `New` остаются разными конструкторами;
backup dispatch выполняется до общего runtime. Неизменённые дорогие локальные
Go/web/Python scopes повторно не запускались: окончательный source требует
полного PR CI, включая настоящий CLI rehearsal.

## Operations image в контейнерном BuildKit

CI source `900a7be53405226dfa8cc0e5aca5fc4967d95d2c`,
[run 38008342103](https://github.com/ekho/3xui-shop/actions/runs/38008342103),
прошёл static/generated, connected consumers, container readiness и native
acceptance. Backup rehearsal остановился на первом `docker run` с exit 125;
сама backup-проверка ещё не началась. Все три независимых cleanup прошли.
Три отдельные PR image checks также прошли.

CI выбирает `docker-container` builder, а operations build не задавал export.
На отдельном собственном builder минимальный `docker build` с тем же
`docker-container` driver успешен, но image отсутствует в local Docker;
`--load` делает его доступным.
Это соответствует [документации Docker](https://docs.docker.com/build/builders/drivers/docker-container/).
В producer и команде runbook добавлен только `--load`;
scratch builder/image/context удалены.

На source `900a7be` с исправленным producer полный native `up` / `check`
через отдельный `docker-container` builder прошёл за 579.8 секунды. Проверены
реальные pg_dump/pg_restore, данные/вложения, URL/schema guards, concurrent
writer, права/повторы, повреждённые пакеты, quarantine и correlated audit.
Собственные fixture containers/network/volume, operations image, builder и
private files удалены; остаток fixture resources — ноль. Полный CI
окончательного HEAD после интеграции доставленной #44 ещё требуется.

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
