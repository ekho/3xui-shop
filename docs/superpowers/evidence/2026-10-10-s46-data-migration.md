# С46 — локальная репетиция и границы evidence

Issue #53; архитектура `2026-10-05-modular-monolith-v1`;
контракт `2026-10-10-s46-data-migration-v1`; новая реальная DDL **00043**.
Source checkout: `feature/s46-data-migration` от
`6236d8c0e3100ef816d036fe0b8fa11163f0f44a`. Старые migration файлы не изменены.

## Подтверждённые сценарии

- Собственный synthetic SQLite всех текущих ORM source tables; динамические
  VPN/subscription/provider keys остаются только в приватном runtime fixture.
  Реальный read-only exporter → полный package → реальный CLI → PostgreSQL.
- Dry-run оставляет все таблицы без изменений. Apply сохраняет оригинальные
  identity/server/registration facts, суммы и units, четыре transaction status,
  raw packed subscription, recurring NULL/flags/charge/expiry, source metadata,
  referral/promo/campaign history, support orphan/topic и audit actors/payload.
- Повтор и новый экземпляр CLI/application сохраняют прежний digest без новых
  records. Изменённый source конфликтует; поздняя ошибка audit откатывает всех
  владельцев. Отозванная/неоператорская роль отказывается до записи.
- Unknown trial false/NULL и recurring остаются закрытыми. Нет искусственных
  native orders/receipts/grants/доставок/jobs для импортированных аккаунтов.
- Реальные synthetic HTTP/signed Telegram/worker/TLS-panel сценарии до repeat:
  funded order, две выполненные referral rewards #51, выданный referred trial
  #52, pending trial/order, дополнительный native pending order после import.
  Повтор/перезапуск сохраняют полный digest всех public table rows.
- Реальные `server backup create` и `server backup rehearse` #45 через own
  operations image. Отдельная LOGIN CREATEDB NOSUPERUSER restore role и новая
  disposable DB; all-public-row digest после restore совпадает. Исключены лишь
  собственные audit events backup operation; сам #45 проверяет manifest,
  schema, таблицы, sequences, dump hash и attachments. Роль/target очищаются.
- Новые immutable provenance guards, запрет native UUID subId и retained-history
  DownTo(42) проверены; empty rollback/reapply работает. SQL module boundaries
  включают новые owner tables и operations ledger.

## Адресные проверки

`backend/cmd/server/legacy_migration_test.go`: strict complete packet, duplicate
keys/Unicode/microsecond precision, actual CLI, replay/operator and late rollback.
`backend/db/legacy_migration_test.go`: schema/key/history/downgrade guards.
Новые owner tests: users/servers/Stars/bonuses, six Tx ports and campaign NULL trial.
`backend/tests/legacy_migration_test.go`: populated native facts + real #45;
подтверждён отдельный прогон 11.945 s в собственном fixture.
`tests/test_legacy_snapshot_export.py`: source schema/links/enums/numeric/private
paths, unsupported views, support timestamp order and ambiguous group regressions.
Генерация OpenAPI/sqlc, `go vet` и semantic-name check выполнены.

Независимое ordinary read-only review нашло три P2: timestamp order, ambiguous
user groups и nested payload duplicate keys. Для каждого добавлен failing-before/
passing-after test; исправления не расширяют runtime API либо import authority.
Итоговые local race results, точные commit/tree и Platform/Images run URLs
фиксируются в #53/PR после завершения проверок, без изменения tested source.

## Ограничения

Production, реальные Telegram/PSP/panels, личные DB/backup/secret files и cutover
не затрагивались. Неизвестные/частичные/исторические schema shapes, HTTP source
URLs, NULL invite counters/state и неоднозначные plan profiles явно отклоняются.
UI не меняется; полные существующие ru/en/browser/a11y и connected-consumer gates
выполняются в principal Platform CI на точной source revision.
Перенос Python/production готовности принадлежит #54/С47; локальная репетиция и
закрытая #53 после merge не являются production-приёмкой.
