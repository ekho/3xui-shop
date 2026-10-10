# С46: контролируемый перенос SQLite → PostgreSQL

Сценарий #53, контракт `2026-10-10-s46-data-migration-v1`, владелец `operations`.
Операция переносит поддерживаемый снимок последней SQLite-схемы в одну PostgreSQL
транзакцию. Репетиция использует только собственные синтетические данные.
Production-перенос, остановка Python и cutover относятся к #54 и здесь не выполняются.

## Вход и доступ

- Нужен отдельный законченный SQLite snapshot по явному абсолютному каноническому
  пути: обычный файл текущего OS-пользователя, права `0600`, без symlink в пути.
  Экспортёр открывает его read-only и проверяет schema/integrity/foreign keys.
- В PostgreSQL уже применены миграции до **00043**. Есть активный проверенный web
  account с операторской ролью; его UUID хранится в отдельном приватном `0600`
  operator file. Доступ к файлам БД сам по себе не заменяет проверку роли.
- `DATABASE_URL_FILE` — отдельный абсолютный приватный `0600` файл. Используется
  та же проверка private path/connection URL, что в backup #45. Inline
  `DATABASE_URL`, настройки `PG*`, меняющие соединение, и дополнительные URL
  параметры отклоняются. Credentials не передаются в аргументах команды.
- `--source` — стабильное имя снимка, ASCII slug до 64 символов. Для dry-run,
  apply и повтора используйте одно имя и один package. Support bot/group задаются
  явно: SQLite ticket не содержит этих идентификаторов.

Пример ниже относится к собственному синтетическому snapshot. Все пути необходимо
заменить на канонические пути своего disposable fixture; файл package тоже приватный.

```sh
umask 077
python3 deploy/data-migration/export_legacy.py /absolute/private/source.sqlite \
  --source synthetic-source --support-bot-id 12345 --support-group-id -10012345 \
  > /absolute/private/package.json
DATABASE_URL_FILE=/absolute/private/database-url \
  server import-legacy --dry-run --operator-file /absolute/private/operator \
  < /absolute/private/package.json
DATABASE_URL_FILE=/absolute/private/database-url \
  server import-legacy --apply --operator-file /absolute/private/operator \
  < /absolute/private/package.json
```

Проверьте exit code экспортёра прежде, чем запускать importer. Ошибка экспорта —
`EXPORT_FAILED`, без package или значений источника. CLI выдаёт JSON с одним
стабильным `error` либо redacted report; runtime/HTTP/River/Telegram не запускаются.
Не публикуйте package, снимок, operator/URL files, dump, provider IDs или VPN-ключи.

## Что сохраняется

Все source tables представлены в package. Accounts сохраняют исходные legacy ID,
Telegram ID, canonical VPN UUID, subscription ID (native 16 символов либо старый
canonical UUID), panel key `str(tg_id)`, server ID и registration time. Snapshot
сохраняет nullable поля, имена, локаль, группы и acquisition. Новые внутренние
account UUID не заменяют исходные VPN/Telegram идентификаторы.

Catalogue сохраняет source IDs/groups/raw price JSON и точные цены RUB/USD/XTR.
Payments сохраняет четыре исходных статуса, payment ID, packed subscription,
timestamps и recurring charge/expiry/flags. Transactions не имеют доказанных
amount/currency/funding: из их packed quote не создаётся native paid order.
Referrals/promocodes/rewards, support topics/orphans и audit actors/raw payload
сохраняются у своих владельцев. `MONEY` не имеет известной currency, поэтому
report не складывает его с `DAYS` или ценами каталога.

`is_trial_used=true` означает used; false/NULL означает unknown. Legacy recurring
остаётся `legacy_unknown`. Импорт не создаёт grants, receipts, выдачу бонусов,
jobs, уведомления, credentials либо роли. Pending legacy rewards не исполняются.

## Повторы, ошибки и ограничения

Одна READ COMMITTED транзакция вызывает публичные Tx-порты владельцев. Dry-run
выполняет те же записи и полностью откатывает их; зависимые новые accounts видимы
внутри этой транзакции. Любой конфликт, в том числе в последней audit-секции,
откатывает весь перенос. Отмена/timeout также откатывает транзакцию.

Apply фиксирует immutable source provenance, digest, source catalogue metadata
и report. Тот же package/source возвращает `replayed=true` без новых записей,
а изменение snapshot под прежним именем даёт `IMPORT_SOURCE_CONFLICT`.
Не меняйте source name для обхода конфликта. Повтор и перезапуск сохраняют
поздние native facts, изменения live metadata и original identity history.

Поддерживаемая схема определяется текущими ORM/migrations; отсутствующие/лишние
таблицы или колонки, dangling links, cycles, duplicate JSON keys, невалидные
enums/Unicode, точность timestamp хуже микросекунды и numeric overflow отклоняются.
Старые исторические schema variants не угадываются. URL серверов должны
соответствовать текущему безопасному HTTPS-контракту, без credentials/query/
fragment; HTTP источник явно отклоняется. Неоднозначные/неизвестные plan profiles
и NULL `invites.clicks/is_active` также отклоняются; NULL invite time сохраняется.

После populated-переноса проверка #45 должна сохранить всю схему до 00043,
source provenance и уже существующие native funded/pending/grant/reward facts.
Restore всегда создаёт отдельную новую БД и требует NOSUPERUSER CREATEDB роли.
Down ниже 00043 блокируется при сохранённой source history, старых UUID subId
или nullable campaign trial facts; старые миграции и их guards не изменены.

## Локальная проверка

`tests/fixtures/legacy_snapshot.py` создаёт новый synthetic SQLite по явному пути,
генерируя ключи только во время теста. `tests/test_legacy_snapshot_export.py`
проверяет exporter. `backend/cmd/server/legacy_migration_test.go` вызывает реальный
CLI и проверяет dry/apply/replay, права, строгий input и поздний rollback.
`backend/tests/legacy_migration_test.go` переносит непустой source вместе с
настоящими test HTTP/Telegram/worker фактами #51/#52 и выполняет real #45 backup/
restore через локальный `operations` image. Требуются собственные PG/Redis fixture
files, подтверждённый Compose/container selector и собранный operations target;
Platform CI подготавливает их до Behavior checks. Другие fixtures не используются.
