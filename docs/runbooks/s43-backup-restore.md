# С43 — backup и изолированная проверка восстановления

Команды выполняет эксплуатационный оператор с доступом ОС к секретам БД и
приватному UUID-файлу действующего web-оператора. UUID не является login-токеном:
полномочия здесь даёт контролируемый доступ ОС к DB credentials, после чего
`accounts` проверяет роль, verified и restricted. Не выдавайте эти файлы обычным
пользователям или web/Telegram-клиентам.

## Что сохраняется

В копии одна PostgreSQL DB: application/River rows, schema/constraints,
sequences и все сохранённые support-вложения BYTEA. `database.dump` и
`manifest.json` фиксируют размер/SHA-256 dump, schema fingerprint, все таблицы,
sequence states и attachment ID/размер/hash. Manifest не содержит текста,
имён вложений, DSN или их байтов. Сам dump содержит персональные данные и
сохранённые секретные значения DB; ему нужны такие же ограничения, как БД.

Redis throttling, mounted secrets/certificates/configuration, панели 3X-UI,
PSP, SMTP inbox и Telegram remote history не восстанавливаются этим пакетом.
Их отдельная резервная копия/reconciliation входит в перенос С45–С47.
`telegram_only=true` означает отсутствующие bytes и вызывает `BACKUP_INCOMPLETE`.
Нельзя объявлять такую историю полностью сохранённой: получите разрешённые
медиа из исходного канала и согласованно импортируйте их перед повтором.

Поддерживается текущая встроенная migration-схема и её PostgreSQL major.
Пользовательские non-public schemas/large objects вне текущего Go-контура
отклоняются. Храните проверенный бинарник/ревизию вместе с записью о копии;
после изменения migration set используйте соответствующую ревизию для
rehearsal. Команда не мигрирует данные молча.

## Подготовка и создание

Нужны `pg_dump` и `pg_restore` того же PostgreSQL major в PATH. Для локального
host-бинарника: `go -C backend build -o /secure/tools/server ./cmd/server`.
Обычный scratch server image не содержит PG utilities. Операционный image
строится отдельно: `docker build --load --target operations -t cabinet-operations:local backend`.
Его ENTRYPOINT — тот же `/server`; задавайте OS UID/GID владельца private files.
Не запускайте операционный image с `serve`/`reconcile`.

Заранее создайте приватный root-каталог 0700 на защищённом диске. DB URL и UUID
оператора — absolute regular files 0600 того же OS owner. Paths и ancestors
должны быть canonical, без symlinks. Используйте TLS/verify-full для внешнего PG;
пароль находится только в secret file, никогда в argv или terminal history.
Подключение source должно видеть всю application DB, включая sequences, и
записывать audit; для production действие требует отдельной авторизации.

Backup поддерживает postgres/postgresql URI с одним явным host, user и database.
Разрешены только query options `sslmode`, `sslrootcert`, `sslcert`, `sslkey`,
без повторов; password необязателен. Verify-ca/full требуют явный `sslrootcert`;
`sslrootcert=system` требует verify-full.
По умолчанию sslmode — prefer, поэтому для внешней БД задавайте verify-full явно.
Multi-host/service URL, channel_binding, target_session_attrs и другие options
возвращают `INVALID_DATABASE_URL` до DB/audit/package side effects. Ambient
connection PG* variables и неявные `~/.postgresql` TLS files также отклоняются:
задайте нужные поддерживаемые TLS options явно в private URI. Такое ограничение
сохраняет одинаковые настройки pgx и PG utilities; [libpq contract](https://www.postgresql.org/docs/17/libpq-connect.html)
описывает значения исходных параметров.

```sh
DATABASE_URL_FILE=/secure/backup/database-url \
  /secure/tools/server backup create \
  --operator-file /secure/backup/operator-account \
  --directory /secure/backup/snapshot-2026-10-09
```

Destination должен отсутствовать. Copy использует SHARE table locks:
application INSERT/UPDATE/DELETE ждут до окончания инвентаризации/dump.
Lock wait ограничен 5 секундами, CLI deadline — 30 минут. Планируйте время
операции по объёму своей БД; не запускайте миграции параллельно. Snapshot
синхронизирует DB и attachment bytes; sequence checks ловят standalone nextval.
PostgreSQL описывает эти границы в [pg_dump](https://www.postgresql.org/docs/17/app-pgdump.html)
и [snapshot synchronization](https://www.postgresql.org/docs/17/functions-admin.html#FUNCTIONS-SNAPSHOT-SYNCHRONIZATION).

Exit 0 означает завершённый private package и success audit; это ещё не доказанное
восстановление. Пока команда работает, новый private каталог может быть виден,
но неполный пакет не принимается для rehearsal. При возвращённой ошибке новая
неполная package directory удаляется. Повтор
использует новый destination; existing копия никогда не перезаписывается.

## Rehearsal в новой БД

Используйте отдельный изолированный PG и отдельный private
`RESTORE_DATABASE_URL_FILE`. Destination role — LOGIN CREATEDB, без SUPERUSER;
для внешнего подключения действуют те же TLS и secret-file правила.
Имя новой DB обязательно начинается с `rehearsal_`, suffix — lowercase
alphanumeric/underscore, 1–50 символов, первый alphanumeric.
Такое имя никогда не допускается для существующей DB.

```sh
DATABASE_URL_FILE=/secure/backup/database-url \
RESTORE_DATABASE_URL_FILE=/secure/rehearsal/admin-database-url \
  /secure/tools/server backup rehearse \
  --operator-file /secure/backup/operator-account \
  --directory /secure/backup/snapshot-2026-10-09 \
  --target rehearsal_20261009
```

Перед CREATE проверяются private ownership/permissions, ровно два fixed files,
strict/canonical manifest, размер/hash, current source schema и PG major.
Restore выполняется без owner/ACL, в одной транзакции, без `--clean`/`--create`.
Exit 0 подтверждает восстановленные таблицы/row hashes/counts, schema,
sequences, attachment hashes и success audit в source DB. Source проверяется
по точному catalog fingerprint, restored schema — по fingerprint с PostgreSQL
deparser для constraints: лишние скобки при pg_restore допустимы, значимая
группировка условий сохраняется.

При restore/verification failure новая DB остаётся для диагностики и не
получает status успеха. Не подключайте к ней runtime; после проверки удалите
только собственный disposable fixture по его lifecycle. Повтор в то же имя
будет отвергнут, даже если предыдущая попытка упала.

Пакет должен происходить из доверенной DB и оставаться под контролем ОС.
SHA-256 обнаруживает повреждение; он не удостоверяет автора. Как предупреждает
[pg_restore](https://www.postgresql.org/docs/17/app-pgrestore.html), restore
исполняет SQL из dump. Нельзя восстанавливать чужой присланный пакет.

## Audit, ошибки и следующий перенос

`backup.create.*` / `backup.rehearse.*`: started/succeeded/failed. Source журнал
содержит действующего actor, correlation operation UUID в reason и стабильный
code; package содержит create UUID. Содержимое dump/DSN/path в audit не входит.
Успех создания и успех rehearsal — разные события. В самой восстановленной
копии есть journal на момент snapshot, а дальнейший результат находится в source.
Denied attempt известного account также попадает в source audit. Для неизвестного
UUID запись невозможна из-за существующего account FK; CLI возвращает и пишет
в stderr только стабильный code без UUID/DSN или содержимого.

`OPERATOR_REQUIRED` — проверьте роль/verified/restricted. `INVALID_PRIVATE_FILE`
или `INVALID_OPERATOR_FILE` — owner/0600/absolute/canonical file boundary.
`INVALID_DATABASE_URL` — устраните неподдерживаемые/ambient connection settings.
`INVALID_PACKAGE` — manifest/файлы/hash/permissions; `PACKAGE_EXISTS` и
`TARGET_EXISTS` запрещают overwrite. `SCHEMA_MISMATCH` требует подходящую
ревизию/schema/PG. `BACKUP_INCOMPLETE` — отсутствующее remote media;
`BACKUP_INCONSISTENT` — locks/sequence/inventory. Dump/restore errors возвращают
стабильные codes, без raw SQL или secret-bearing diagnostic output.

Rehearsal сохраняет старые sessions/proofs/jobs для проверки полноты.
**Не запускайте на ней `serve` или `reconcile`.** Перед отдельно разрешённым
переносом закрыть ingress, остановить writers/mail workers, сверить внешние
платежи/панель, выполнить согласованный migrate и
`backend/db/maintenance/post_restore_auth.sql`, затем own acceptance С45–С47.
Успешный локальный rehearsal не разрешает production и не доказывает live cutover.

Для повторяемой локальной проверки только synthetic данных:

```sh
python3 deploy/acceptance/backup_restore.py up
python3 deploy/acceptance/backup_restore.py check
python3 deploy/acceptance/backup_restore.py down
```

Fixture использует собственный случайный Compose project, свободный subnet и
динамические loopback ports. Cleanup удаляет только его containers/network/PG
volume и private synthetic package directory. Чужие сети/volumes не удаляются.

Для connected Go-проверок используйте созданные `up` private files и выполните
тесты до `down`:

```sh
fixture="$(pwd -P)/.superpowers/acceptance/backup-restore"
TEST_DATABASE_URL_FILE="$fixture/test-database-url" \
TEST_REDIS_URL_FILE="$fixture/test-redis-url" \
TEST_POSTGRES_FIXTURE_FILE="$fixture/test-postgres-fixture" \
  go -C backend test ./tests -run '^TestWebTrialBackupRestore$' -count=1 -race
```

Этот же тест принимает `TEST_POSTGRES_COMPOSE_FILE` с Compose-файлом своего
fixture вместо metadata или вместе с ними. При двух входах project/container
должны совпасть. Compose-файл может иметь mode 0644, но должен принадлежать
текущему OS user, быть обычным файлом без symlink и group/world write.
Helper сверяет выбранный project, service/container и единственный фактический
`127.0.0.1:<port>` с подключением теста до dump. Без обоих входов используется
repository test Compose на порту 55491. Ошибка выбранного входа не допускает
переход к другому контейнеру.

Restore-тест проверяет приватный файл и фактические container ID, Compose
project/service и loopback port. Явно заданный неверный fixture останавливает
тест; поиск чужого контейнера по порту не выполняется. Без opt-in CI использует
прежний controlled Compose fixture.
