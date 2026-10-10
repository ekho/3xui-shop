# С46 — атомарный итоговый перенос SQLite → PostgreSQL

Дата: 2026-10-10. Контракт `2026-10-10-s46-data-migration-v1`.
Issue #53; base `6236d8c0e3100ef816d036fe0b8fa11163f0f44a`.
Архитектура `2026-10-05-modular-monolith-v1`, владелец `operations`.
Приёмка ограничена собственными synthetic fixtures. Production/cutover не разрешены.

## Исходный контракт и реальные пробелы

Авторитетны `app/db/models/*.py` и `app/db/migration/versions/*.py`, а не
предположение о старой БД. В этой базе нет общего SQLite importer. Есть JSON
импорты approvals, payment history, catalogue, campaigns, support и audit;
каждый открывает собственную транзакцию. Их CLI и проверки сохраняются.

| SQLite source | PostgreSQL owner / сохранение |
| --- | --- |
| users.id/tg_id | accounts.legacy_user_id/telegram_id и неизменяемый исходный снимок; новый accounts.id UUID является внутренним ID |
| users.vpn_id/sub_id | accounts.vpn_id/sub_id без генерации/ротации; VPN ID canonical UUID, subId native16 либо старый canonical UUID; иначе явная ошибка без изменения БД |
| panel key | В SQLite поля нет: Python `vpn.py` создаёт/ищет `email=str(tg_id)`; это accounts.panel_key |
| users.server_id | assigned_panel_id = десятичное представление исходного server ID; NULL сохраняется |
| first_name/last_name/username/language_code/created_at/inbound_groups/source_invite_name/is_trial_used | исходный снимок accounts, исходное время регистрации; локаль интерфейса ru/en с отчётом о fallback; групповые данные не выдаются за подтверждённое состояние панели |
| approval status/requested/decided/actor | существующие legacy_approval_snapshots/events; rejected не получает native доступ/роль |
| servers.id/name/host/max_clients/location/online/subscription_url | vpn_servers с исходным decimal ID и snapshot всех source полей; без сетевого probe/credentials |
| plans.id/devices/traffic_gb/prices/hidden/inbound_groups; plan_durations.id/days | существующий catalogue import, исходные decimal prices и IDs/group metadata в снимке переноса; точные minor units RUB/USD/XTR; неподдерживаемые группы/цены отклоняются |
| transactions.id/tg_id/payment_id/subscription/status/created_at/updated_at | существующие legacy_payment_transactions; все четыре статуса и raw packed payload/ID; без создания funded order/receipt/job |
| users.stars_charge_id/is_stars_auto_renew/stars_expires_at | payments-owned legacy snapshot; сохраняются NULL и исходные Unix seconds; unknown recurrence остаётся закрытой существующими guards |
| referrals.id/referred_tg_id/referrer_tg_id/created_at/referred_rewarded_at/referred_bonus_days | bonuses.referrals + исходный снимок; immutable original inviter, cycle/FK checks, без создания льготы |
| referrer_rewards.id/user_tg_id/reward_type/reward_level/amount/payment_id/created_at/rewarded_at | bonuses.referrer_rewards + исходный снимок; DAYS/MONEY и nullable level; numeric(38,18), pending/finished; без source_order_id и исполнения |
| promocodes.id/code/duration/is_activated/activated_by/created_at | bonuses.promocodes + source snapshot/history; nullable actor и неизвестное activated_at сохраняются; без активации/дней |
| invites.id/name/hash_code/clicks/is_active/created_at; users.source_invite_name | существующий campaigns import, raw IDs/name/hash/state/NULL time и историческая acquisition; отсутствующий invite остаётся orphan, поздний link не меняет source |
| support_tickets.id/tg_id/thread_id/status/created_at/updated_at | существующий support import, source-scoped bot/group из явного input; original account proof, unknown guests/orphans без фиктивного account |
| audit_log.id/created_at/action/target_id/actor_type/actor_id/actor_name/source/payload | существующий private legacy audit snapshot/digest, исходные акторы/NULL/raw payload; approval events также в accounts-owned history |
| alembic_version | проверка формы source; не runtime-таблица PostgreSQL |

В SQLite transaction нет самостоятельных amount/currency/funding fields.
Цена в subscription — историческая quote, а не доказательство денег. MONEY reward
не имеет currency. Отчёт не смешивает его с DAYS и native receipts/currencies.
Реальный SQLite Numeric может храниться как INTEGER/REAL: экспорт фиксирует
Decimal(38,18), наблюдаемый текущим ORM, без дополнительного округления.
`is_trial_used=true` — доказан использованный trial; false — unknown, поскольку
старые строки backfilled false. Unknown не превращается в unused или grant.
Исходные NULL остаются NULL; неподдерживаемая/частичная схема не угадывается.

## Операция и совместимость

Private read-only exporter выдаёт полный version=1 packet в приватный файл/pipe.
Отсутствующие таблицы/колонки, неизвестные columns/tables, невалидные enums,
несогласованные ссылки, lossy values, duplicate JSON keys и oversized input —
явная ошибка без частичного package. Никаких default пользовательских DB paths.
Support bot/group задаются явно; их нельзя восстановить из SQLite ticket.

`server import-legacy --dry-run|--apply --operator-file <private file>` читает
packet со stdin и `DATABASE_URL_FILE`. OS-доступ к credentials и проверенная
accounts-роль оператора — та же граница, что backup #45. Это не HTTP/Telegram
endpoint. Runtime/worker/доставки не запускаются. SQL остаётся у владельцев;
operations вызывает публичные Tx методы. Одна READ COMMITTED транзакция содержит
все source checks, source snapshots, domain rows, redacted audit и report.
Dry-run выполняет ту же транзакцию и откатывает её, включая зависимости новых
accounts; никакие jobs/messages/panel calls не создаются.

Новая реальная DDL **00043** хранит недостающий source provenance и
legacy recurring snapshot, ledger полной операции, а также допускает старый
UUID subId только для legacy account и NULL historical trial в campaign acquisition.
Существующие IDs не заменяются. Все provenance rows неизменяемы. Одинаковый packet/source
возвращает сохранённую сверку без новых записей. Изменённый snapshot под прежним
source — конфликт. Ранее частично доставленные импорты проверяются через
сохранённые owner snapshots, а не перезаписываются. Данные, созданные native
сценариями после import, сохраняются. Смена Telegram alias не переносит историю.

Public packet содержит `users`, `servers`, существующие `catalogue`, `approvals`,
`payments`, `campaigns`, `support`, `audit`, новые `stars` и `bonuses`, а также
`catalogue_source` (исходные duration IDs/plan group+price JSON). Все секции
обязательны; идентичности users/approvals/payments согласованы. В отчёте только
counts/digests/aggregated exact reward units и catalogue currencies, явные
unknown/quarantine counts; никаких ключей, provider IDs, имён или payloads.

## Приёмка

1. Собственный populated SQLite последней ORM-схемы: все source таблицы,
   ненулевые prices/rewards, разные currencies, pending/completed/canceled/refunded,
   recurring/NULL, исходные UUID/subId/server/key, referrals/promos/campaigns,
   support orphan/topic, audit/approval actors и microsecond timestamps.
2. Реальный exporter → CLI → owner ports → PostgreSQL. Dry-run, apply, replay,
   restart, invalid operator/input, partial/conflicting late section: весь import
   откатывается; идентификаторы сравниваются внутри assertions без публикации.
3. Реальные HTTP/Telegram и worker boundaries: history/identity/unknown-trial и
   recurring guards; import не выдаёт доступ, бонусы, оплаты или сообщения.
4. До repeat создать непустые текущие grants/referrals/reward delivery/native
   funded+pending факты #51/#52. Repeat/restart и общий backup #45 + restore
   сохраняют IDs, provenance, exact amounts/status/payloads всего public schema.
5. Независимое read-only review; full exact-source Platform и три Image checks;
   manual merge только в v2, затем issue Closed/Project Done, own fixture cleanup.
   UI не меняется; существующие ru/en/error/empty/keyboard/a11y gates сохраняются.
