# С43 — согласованный backup и проверка восстановления

Owner: `operations`, GitHub #45. Основа: принятое решение
`2026-10-05-modular-monolith-v1` (#55); исходная база `9305fa5`.
Мандат покрывает implementation/local acceptance/review/PR/CI. Merge и Done
выполняет головной чат. Production, реальные данные и перенос не разрешены.

## Выбранный путь

Две локальные команды существующего Go-бинарника: `backup create` и
`backup rehearse`. Эксплуатационный оператор имеет доступ ОС к приватным
файлам подключения и UUID аккаунта; accounts повторно проверяет действующую
web-роль, verified и restricted. Команды вызывают публичный `operations` owner,
не запускают HTTP, River, Telegram, SMTP или провайдеры. Такой путь следует
существующим локальным operator/catalogue/import-командам. HTTP export,
пользовательский Telegram, новое хранилище и UI не нужны.

Текущие поддерживаемые вложения — `support_messages.attachment_bytes BYTEA`.
Support owner предоставляет snapshot-инвентарь ID/размер/SHA-256 без текста,
имён файлов или выдачи чужих private SQL. Другого локального blob-хранилища
в Go-контуре нет. При `telegram_only=true` байты отсутствуют: полный backup
отказывает с `BACKUP_INCOMPLETE`; удалённый Telegram не объявляется сохранённым.
Медиа нужно отдельно получить и согласованно импортировать до повторной копии.

## Создание

Один PostgreSQL repeatable-read snapshot экспортируется через
`pg_export_snapshot`; `pg_dump -Fc --snapshot` и инвентарь читают один snapshot.
Это копирует DB, attachment BYTEA, очереди и связи одной версии.
На время копирования SHARE locks таблиц кратко задерживают application DML;
это не отдельный maintenance-контур. Sequences не MVCC: их состояние дополнительно
сравнивается до/после dump, standalone nextval вызывает отказ при изменении.
Несохранённые удалённые вложения проверяются в том же snapshot.

Новый приватный каталог содержит ровно `database.dump` и `manifest.json`.
Manifest version 1: operation ID/time, PG major, fingerprint встроенных
миграций/schema compatibility, размер и SHA-256 dump, полный инвентарь таблиц
(число строк и SHA-256 стабильного содержимого), sequences и вложений.
Все schema/data/constraints входят в dump; секреты/config/Redis и состояние
внешней 3X-UI/PSP/Telegram/SMTP не входят. Они имеют отдельный operational owner.
Это не snapshot всей инфраструктуры.

Каталог mode 0700, файлы 0600, тот же OS owner. Существующий destination не
перезаписывается. Symlinks, относительные пути и неполный пакет отклоняются;
статус успеха выдаётся только после завершённого dump, manifest и audit.
Audit через публичный audit_reports: start/success/failure, actor, operation ID,
стабильный reason code; никаких DSN, путей, содержимого или raw ошибок.

## Репетиция

Rehearse принимает приватный доверенный пакет и отдельное подключение к PG
с правом CREATEDB, но без superuser. PostgreSQL dump исполняет SQL, поэтому
hash — проверка повреждения, а не доказательство доверия к чужому dump.
Нельзя импортировать присланные посторонним пакеты; OS-доступ к ним ограничен.

До CREATE DATABASE: live actor/schema проверки, strict manifest parsing,
ровно два обычных файла, owner/permissions, фиксированное имя dump, size/hash,
схема и PG major. Restore destination — только новый `rehearsal_<suffix>`;
существующие БД, включая source, никогда не используются/очищаются.
`pg_restore --no-owner --no-privileges --exit-on-error --single-transaction`
восстанавливает в новую БД без запуска приложения. После восстановления
проверяются schema, все строки/связи/sequence states и attachment hashes;
недостаточно одного успешного exit code.

Rehearsal сохраняет исходные данные для доказательства полноты, включая старые
auth/proof/job facts. Ни `serve`, ни `reconcile` на этой БД запускать нельзя.
Перед отдельным согласованным переносом обязательны существующий
`db/maintenance/post_restore_auth.sql`, миграция, external-state reconciliation
и отдельные gates С45–С47. При ошибке новая БД остаётся изолированной для
диагностики; автоматического удаления/перезаписи и provider side effects нет.

## Приёмка

- AC1: real pg_dump/pg_restore synthetic DB со связанными accounts, support
  messages и несколькими бинарными вложениями; все inventories/schema совпали.
- AC2: конкурентный writer ждёт release locks; пакет содержит согласованную
  версию DB/вложений до записи, live source после release принимает изменение.
- AC3: неизвестный/обычный/revoked/restricted operator не получает копию;
  audit success/failure принадлежит реальному actor и operation ID.
- AC4: повреждённые/неполные/неизвестные manifest fields, dump hash/size,
  traversal, symlinks, чужие permissions, schema/PG mismatch отвергаются до
  создания restore DB. `telegram_only` не выдаёт полный backup.
- AC5: повтор create/rehearse не перезаписывает существующее; failed dump и
  restore не оставляют успешного evidence. Source DB не меняется кроме audit;
  restore не начинает queue/Telegram/SMTP/payment/VPN effects.
- AC6: полный текущий CI (Go, generated, web, Python, images и native) на
  окончательном PR HEAD; локальные результаты и ограничения записаны отдельно.

Миграция БД и новый OpenAPI контракт не требуются. #44 maintenance и #46
lifecycle/readiness остаются отдельными владельцами.
