# С25: рекламные приглашения и источник регистрации

Владелец [#35](https://github.com/ekho/3xui-shop/issues/35), контракт [2026-10-08-s25-campaigns-v1](https://github.com/ekho/3xui-shop/issues/35#issuecomment-6058801580). Предпосылки #6/#17/#56 закрыты; база 826f0b8c9f8697514ab8c89e644acb6026ddc7f8. Native и последовательная доставка PR в v2 уже разрешены; отдельное подтверждение документов не требуется.

## Результат

Оператор создаёт именованную рекламную ссылку, смотрит список/карточку и показатели, приостанавливает, включает или удаляет кампанию. Клиент регистрируется через кабинет или signed Telegram Mini App; первый источник остаётся у исходного UUID. Рекламная кампания не является рефералом и не выдаёт льгот.

Новые ссылки: текущий кабинет `/register?invite=<code>`, имя продукта/домен берутся из текущего окружения. Новый code = `c_` + 32 hex UUID, ненумерический, без секретов. Старые hash и маршрут `/start hash → startapp → signed start_param` сохраняются. Новая ссылка не требует отдельной настройки bot username.

## Владение и данные

Один Go-процесс; новый owner `campaigns`. Он хранит:
- campaign UUID, неизменяемые name/code/created_at и legacy snapshot/ID/clicks, current state active/paused/deleted, revision, атомарный web_visits;
- immutable campaign_acquisitions: account UUID, campaign UUID, channel web/telegram/legacy_name, created_at, source snapshot/legacy trial flag;
- immutable campaign_events: event/campaign UUID, actor UUID или null для CLI import, action/reason/time и снимки до/после. Кампания не подменяет target клиентского audit_events аккаунтом оператора. Общий просмотр этих событий принадлежит С29.

Имя — 1–100 Unicode characters после trim, NUL запрещён; name/code не переиспользуются после delete. Delete — конечное состояние без физического удаления, нажатие требует явного подтверждения. Изменение требует expected_revision ≥1 и reason 1–1000 Unicode characters после trim. Совпадающий Idempotency-Key/body возвращает прежний результат без новой версии/события; другой body →409 IDEMPOTENCY_CONFLICT. Несовпадающая текущая версия/изменение deleted →409 CAMPAIGN_REVISION_CONFLICT; занятое имя →409 CAMPAIGN_NAME_CONFLICT. Неизвестный UUID →404 INVALID_INPUT.

Accounts хранит immutable nullable registration_source_code у нового аккаунта. Существующим аккаунтам источник не придумывается. Optional RegisterInput.source_code — ASCII [A-Za-z0-9_-],1–64; signed Telegram raw source сохраняется до512 как в текущем контракте. Поле не раскрывается обычным Account DTO. Старый TelegramStartParam остаётся отдельным историческим фактом, включая прежнее позднее заполнение.

Challenge сохраняет optional source_code, resend переносит его. При повторной регистрации до создания аккаунта действует source того challenge, которым подтверждён email. При реальном создании UUID accounts вызывает публичный CaptureRegistrationTx в той же Tx. Кампания должна быть active в этот момент, её row lock сериализует pause/delete с регистрацией. Unknown/numeric/inactive source сохраняется в accounts, но не назначает кампанию и не блокирует регистрацию. Поздний /start, login, verify replay, first email, email change и Telegram linking не вызывают повторную атрибуцию.

## Счётчики и деньги

web_visits — число принятых запросов открытия web-ссылки, включая повторные открытия; не уникальные люди и не рекламный antifraud. Public POST ограничен30 запросами/IP/минуту, IP хранится только HMAC/hash-key в Redis с TTL, не в БД. Invalid code shape →400; неизвестный/inactive code →204 без информации о кампании. Ошибка счётчика не блокирует UI регистрации. legacy_clicks отдельно: старый бот увеличивал его только для нового пользователя. telegram_registrations — новые UUID с доказанным signed code, без выдуманной оценки просмотров.

Card statistics читаются в одной read-only repeatable-read Tx. campaigns читает только собственную когорту/флаги, subscriptions/payments — собственные данные через публичные StatisticsTx(ctx,tx,ids) ports; нет чужого SQL/N+1.

- users: число аккаунтов в когорте; web/telegram/legacy_name counts отдельно.
- trial_users: native grant.status=granted, без reserved/ошибок; legacy_trial_used — отдельное архивное число.
- paid_users/repeat_users: native accounts с одним/более одним funded paid order; recurring child orders считаются отдельными покупками. Pending/review/extra unfunded receipt не считается оплатой.
- Native currency totals: сумма funding gross_minor, сумма только известных net_minor, unknown_net_receipts. Все суммы decimal integer strings, SUM numeric/BigInt, RUB/USD/XTR отдельно.
- refunds: подтверждённые возвраты funding receipt, returned_amount отдельно по фактической currency, exact decimal strings. Нет автоматического вычитания USDT из USD или неизвестной комиссии.
- legacy: COMPLETED archive transactions, paid/repeat users, quoted revenue известных nine-part payload по валютам, unknown_quote_count. Архивные price/flag/status не доказывают native money/access. Используется decoder С18, без float и конвертации валют.

С26 повторно использует эти определения/ports. Ни импорт, ни просмотр статистики не меняет orders/receipts/refunds, trial grants, VPN IDs, source policy, recurring owner или выдачу.

## Контролируемый перенос

Read-only SQLite exporter получает явную IANA timezone и stopped owned snapshot. Сохраняет исходные invite ID/name/hash/clicks/is_active/created_at и users legacy ID/TG/source_invite_name/is_trial_used. Raw snapshot сохраняется у campaign/member. Name-reference атрибуция из старого users.source_invite_name не доказывает hash при прежнем удалении/переиспользовании имени; это явно обозначено в карточке. Orphan name получает архивную deleted карточку без code/ссылки, без выдуманного старого ID.

Stdin CLI `import-legacy-campaigns --dry-run|--apply` переиспользует bounded32MiB strict JSON decoder. Package version1, до10000campaigns/100000users, уникальные исходные ID/имена/code/users, положительные signed-int64 identity IDs, неотрицательные clicks. Уже сопоставленные accounts legacy/TG IDs обязательны. Весь пакет атомарен; конфликты mappings/source/raw data/name/code останавливают весь импорт. Exact replay не пишет события/строки. Dry-run действительно read-only. Новые деньги, доступ, задания и внешние запросы не создаются. Полная репетиция/production import остаётся С46/С47; legacy trial flag не открывает automatic trial.

## HTTP и интерфейс

Additive OpenAPI:
- POST /api/v1/campaign-visits {code}: public same-origin,204;400/429/503.
- POST /api/v1/operator/campaigns/search {page,per_page}: web operator, Origin/CSRF; максимум50.
- POST /api/v1/operator/campaigns {name,reason}: operator write/Idempotency-Key,201.
- GET /api/v1/operator/campaigns/{id}: operator card/basic+statistics+последние20audit events с признаком продолжения.
- POST /api/v1/operator/campaigns/{id}/state {state,expected_revision,reason}: operator write/Idempotency-Key,200.

Operator write повторно проверяет и блокирует account/role через существующий LockOperatorPair(actor,actor). Mini bearer/client/restricted/unverified actor не допущены. Public source/count не даёт административного чтения. Old endpoints/DTO/security/hash сохраняются; никаких новых GET-query исключений.

React-admin получает один custom campaigns resource на существующих стилях. Keyboard/labels/error focus, ru/en, loading/empty/error/retry,375px, безопасные тексты, причины/версии и deleted confirmation обязательны. Link строится от location.origin/URL, без токенов/password/VPN keys; code копируется только явной кнопкой. Auth читает ровно один валидный invite только в mode=register, хранит в memory до submit, отправляет optional source_code; не сохраняет в localStorage и не переносит в login/reset/link.

## Приёмка и границы

Meaningful RED→GREEN: роли/CSRF/replay/concurrency/soft delete; first source/resend/inactive/late start/link; currency/unknown-net/refund/recurring/legacy separation и rollback/replay/orphans; rendered CRUD/register ru/en/a11y/narrow/errors. Owned native Go HTTP/jobs/Telegram stub + TLS SMTP/3X-UI3.7.0 доказывает кампанию → новый UUID → реальный триал и сохранение состояния после restart. Используются только разрешённые собственные локальные fixtures; живой Happ/VPN/Mac trust, реальные деньги, Telegram/SMTP/production исключены.

После полного текущего Go race/web/Python/generation/static набора — ONE свежий Astra/high whole-branch reviewer; root re-grade/ONE Critical/Important RED→GREEN author pass, Minor в журнал. Source CI, manual guarded v2 merge и actual prerelease/tag/3OCI images проверяются отдельно. #35 Done означает реализацию, локальную приёмку и v2 delivery, не production readiness; далее С26.

