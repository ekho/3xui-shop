# С22 — оператор ведёт промокоды

Основание: #48, `2026-10-05-modular-monolith-v1`, общий каркас
`2026-10-10-bonuses-scaffold-v1` в комментарии #48/6094467060.
База: e7a46c338420cb459cbf86fdae3bae9254ab9f86. Миграция: 00040.
Полностью автономная реализация и manual merge в v2 разрешены передачей задачи.

## Поведение

Оператор создаёт автоматически сгенерированный одноразовый код на 1..365 дней,
видит список, состояние и историю, меняет длительность только до активации.
Код не переименовывается. Для каждой записи требуется текущая revision,
для каждой mutation — причина и Idempotency-Key.

Удаление неиспользованного кода сохраняет terminal tombstone и историю.
Использованный код возвращает PROMOCODE_USED для edit/delete; его activation
marker и связь с клиентом сохраняются. Deleted код не восстанавливается и не
переиспользуется. Повтор уже выполненной mutation возвращает сохранённый ответ,
не выполняет её снова. UI после ответа обновляет карточку из текущих данных,
поэтому старый ответ не разрешает новое изменение использованного кода.

Создание/изменение кода не выдаёт доступ, не сбрасывает трафик и не создаёт
платёж. Клиентская активация и bonus execution принадлежат #49; её выдача дней
использует публичный subscriptions contract #14 (компенсация без reset,
ограничения ban/unlimited/perpetual, стабильные VPN identities).

## Владелец и данные

Единственный владелец — bonuses. Общий конструктор:
`New(pool *pgxpool.Pool, authority *accounts.Service, now func() time.Time) *Service`.
Общие поля: pool/authority/now. contracts.go содержит общий Error, feature DTO
находятся в promocodes_contracts.go. #50 владеет собственными referral файлами.
app.Modules.Bonuses и sqlc-регистрация собирают один модуль; второго store нет.

promocodes: UUID id, immutable code, duration_days, revision, created_at,
deleted_at, is_activated, activated_account_id, activated_by_tg_id, activated_at,
nullable legacy_source/legacy_promocode_id. Исходный legacy id — decimal string
в DTO без потери bigint в JavaScript. Legacy created_at/activated_at могут
оставаться неизвестными; is_activated=true не предполагает наличие известного
activator. Positive legacy duration сохраняется даже за пределами native 365.
Полный SQLite importer и установление исходного владельца остаются #53.

promocode_events сохраняет append-only action, actor, reason, время и
before/after metadata без redeemable code. Global audit записывается через
audit_reports.RecordTx в той же транзакции; event id совпадает с audit id.
Глобальный account target для ведения кода — actor, причина явно содержит ID
кода и пользовательскую причину, но сам redeemable code отсутствует.

## Конкурентность и права

Каждая mutation сначала accounts.LockOperatorPair(actor, actor), затем replay,
затем SELECT FOR UPDATE promocode. Роль/restricted/Telegram proof проверяются
до replay; stale expected_revision даёт PROMOCODE_REVISION_CONFLICT.
Неверный body для старого ключа даёт IDEMPOTENCY_CONFLICT.

Активация #49 сначала блокирует аккаунт клиента через публичный accounts port,
затем ту же строку промокода, проверяет tombstone/usage,
сохраняет duration snapshot и увеличивает revision в одной транзакции с grant.
Единый порядок account → promocode исключает обратную FK-блокировку при
активации оператором для себя; тест покрывает обычного клиента и оператора.
DB trigger запрещает physical DELETE, смену code/ID/legacy identities,
сброс использованного marker/связей, редактирование activated duration и
восстановление deleted. Любое UPDATE увеличивает revision ровно на один.
Это не новая активационная операция; local race test моделирует только её
запись в БД под тем же lock. Активация после deletion отказывается.

## HTTP, web и Telegram

Публичные bonuses операции:
`ListPromocodes(ctx, actor, page, perPage) (PromocodeList, error)`,
`GetPromocode(ctx, actor, id) (PromocodeDetail, error)`,
`CreatePromocode(ctx, actor, key, CreatePromocodeInput) (Promocode, error)`,
`EditPromocode(ctx, actor, id, key, EditPromocodeInput) (Promocode, error)`,
`DeletePromocode(ctx, actor, id, key, DeletePromocodeInput) (Promocode, error)`.

POST /api/v1/operator/promocodes/search {page,per_page}; POST .../promocodes
{duration_days,reason}; GET .../promocodes/{id}; POST .../{id}/edit
{duration_days,expected_revision,reason}; POST .../{id}/delete
{expected_revision,reason}. Только operator cookie, Origin/CSRF для POST,
strict bounded DTO; mini-app bearer не получает operator доступ.

Promocode DTO: promocode_id/code/duration_days/revision/state (available,
activated,deleted)/created_at/activated_at/activated_account_id/
activated_by_tg_id/legacy_source/legacy_promocode_id. List: promocodes/page/
per_page/total. Detail: promocode/events/events_has_more, 50 последних событий.
Event: event_id/actor_account_id/action/created_at/reason/before/after;
metadata: promocode_id/duration_days/revision/state. Все nullable поля явны.

Web /admin/promocodes: RU/EN, pagination, пустой список, ошибки с focus/alert,
labelled формы, busy controls, сохранённый ключ для uncertain retry, явное
обновление stale карточки, подтверждение удаления, history. Использованный
или deleted код показывается без mutation формы; ввод не теряется при ошибке.

Telegram private non-forwarded operator commands: /promocodes;
/promo ID; /promo_create DAYS REASON; /promo_edit ID REV DAYS REASON;
/promo_delete ID REV confirm REASON. Stable key зависит от bot/update/chat/message.
Adapters проверяют Telegram proof и текущую operator role и вызывают только
публичные bonuses операции. Safe HTML response, RU/EN и обычная URL-кнопка
web operator кабинета, требующая отдельной operator session. Legacy Python
не меняется и не запускается как второй владелец PostgreSQL mutations.

## Приёмка и границы

Real HTTP + own PG/Redis: CRUD, права/Origin/CSRF/input/role revocation,
same-key/body conflict, stale request, restart, no duplicate history/audit;
row-lock races activation/edit/delete; bigint legacy ID/nullable usage facts;
DB guards и точный DownTo(39) guard 00040; прежний 00039 test сначала DownTo(39).
Own botapi HTTP fixture подтверждает commands/actor/replay, без внешних сообщений.
Chromium: RU/EN, keyboard, empty/error states, stale refresh, uncertain retries,
used/deleted read-only и history. Backup/restore покрывает новые public rows
динамически, без narrowing существующего #45; required CI проверяет весь backend.
Independent review, exact-HEAD Platform + 3 image checks, manual merge в v2,
Closed/Project Done, cleanup и handoff родителю. Production не разрешён.
