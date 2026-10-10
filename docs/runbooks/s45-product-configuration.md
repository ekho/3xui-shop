# Deployment inventory v2 — С45

Владелец [#47](https://github.com/ekho/3xui-shop/issues/47), контракт
`2026-10-10-s45-product-config-v1`. Это перечень **используемых** настроек,
не новая платформа настроек. Target — один Go backend HTTP/River/Telegram,
отдельный frontend/Caddy. Python остаётся legacy до #54; не запускайте двух
исполнителей одной операции. Production этим документом не разрешается.

## Сохранить и применить

Эксплуатационный оператор хранит env и secret files вне Git, с ограниченным
доступом ОС. В env — public values и абсолютные пути, в файлах — секреты.
Не выводите `docker compose config` целиком: используйте `config --quiet`,
`config --images`, `ps` и безопасные states. Зафиксируйте project, revision,
исполнителя и причину без секретов в журнале host-оператора.

За основу используйте `deploy/acceptance/.env.example` и существующий
`compose.acceptance.yml` с проверенными overlays окружения. Это пример для
выделенного стенда, не разрешение использовать чужой fixture/production.
Оплаты подключаются соответствующим `deploy/purchase/compose.*.yml`.
`compose.local.yml` / `compose.native.yml` — только synthetic acceptance:
локальные panel/SMTP/оплата, fixed test ports, не production-настройки.

Изменение env требует recreate: по [С44](s44-process-operations.md)
`up -d --no-deps backend` или `gateway` с **тем же точным project/env/overlays**.
`restart` старого контейнера не перечитывает env. Одна web-сборка использует
новые public values при новом запуске Caddy; клиент получает их при reload.
Версии TERMS/PRIVACY меняйте согласованно у frontend и backend. При смене
домена отдельно согласуйте DNS/TLS и `CABINET_HOST`/`CABINET_ORIGIN`/document
URLs; старые origin cookies/ссылки не становятся автоматически переносимыми.

Проверка: backend `/readyz` и healthcheck, безопасные module states, публичный
`/config.json` с no-store, ru/en интерфейс, документы и обычная разрешённая
операция. Не печатайте private configuration в evidence. Не перевыпускайте
MAIL/CODE keys, DB credentials или panel identities ради branding/URL.
Повтор recreate не изменяет persisted UUID/jobs/orders/audit/maintenance.
Rollback возвращает прежние deployment files и digest, затем recreate/probe;
DB/schema rollback не входит в смену конфигурации.

## Backend: обязательная основа

Источник: `backend/internal/app/config.go`, `backend/cmd/server/main.go`.
Секретные env без `_FILE` не являются альтернативой в Go. Если одновременно
заданы file и непустой plaintext — отказ; плохой file не допускает fallback.
Ошибка сообщает имя настройки/безопасный code, без значения или пути.
File contents читаются при запуске и trim; доступ файла должен иметь только
нужный runtime UID. Docker secrets/CA files доступны этому UID read-only.

| Настройки | Назначение и условия |
| --- | --- |
| `DATABASE_URL_FILE`, `REDIS_URL_FILE` | Обязательные private connection URI. PostgreSQL/River и Redis throttling; URL не в argv/logs. |
| `MAIL_KEY_FILE`, `CODE_KEY_FILE` | Обязательные base64 32-byte keys. Сохранять при restart/restore, не менять вместе с public settings. |
| `CABINET_ORIGIN` | Обязательный HTTPS origin без credentials/path/query/fragment; public links, exact Origin/CSRF. |
| `TERMS_VERSION`, `PRIVACY_VERSION` | Обязательные непустые версии согласия, одинаковые у backend/web. |
| `LISTEN_ADDRESS` | Host bind; default `127.0.0.1:8080`, Compose — `0.0.0.0:8080`, наружу только проверенный gateway. |
| `TRUSTED_PROXY_CIDRS` | Optional comma-separated CIDRs доверенного proxy; Compose использует точный gateway `/32`. |
| `BOT_OPERATOR_IDS` | Optional comma-separated positive Telegram actor IDs для operator boundaries; не заменяет web operator grants. |
| `LEGACY_BOT_API_ENABLED`, `BOT_ADAPTER_TOKEN_FILE` | Transitional API default true. При операторах требует token длиной ≥32. Native deployment задаёт false; не исполнять native и adapter одновременно. |

`migrate`/`reconcile` используют существующие Config. Standalone catalogue,
operator/infrastructure grants и legacy imports читают `DATABASE_URL_FILE` по
своим защищённым CLI contracts. Их input files/permissions не заменяются env.

## Telegram, audit и эксплуатационный канал

| Настройки | Default/условие |
| --- | --- |
| `TELEGRAM_ENABLED`, `BOT_TOKEN_FILE` | false. Token требуется **только** для main Telegram-on; off не читает token и обслуживает HTTP/River. On требует допустимый token/operator IDs; bad main config останавливает serve. |
| `SHOP_PAYMENT_STARS_ENABLED` | false. Native Stars/Mini App требуют настроенный main bot; новые деньги/recurring/refund сохраняют свои source/funding guards. Нет флага для обхода денежных проверок. |
| `SUPPORT_TELEGRAM_ENABLED`, `SUPPORT_BOT_TOKEN_FILE`, `SUPPORT_GROUP_ID` | false. On требует отдельный bot token и отрицательный group ID; main/support polling identities различаются. Ошибка support даёт degraded channel, web/core продолжают работу. |
| `AUDIT_MIRROR_ENABLED` | false. Optional mirror использует тот же SUPPORT token/group, только metadata; недоступность не заменяет persisted audit. |
| `AUDIT_RETENTION_DAYS` | 365; допустимо 1–3650, daily prune оставляет audit события. |
| `AUDIT_RETENTION_TIMEZONE`, `BOT_TIMEZONE` | IANA timezone: explicit audit zone → BOT_TIMEZONE → UTC; `Local` запрещён. Audit prune schedule не меняет expiry/исторические timestamps. |
| `ACCESS_RESET_TIMEZONE` | UTC; отдельная IANA zone monthly unlimited reset, не автоматический alias BOT_TIMEZONE. |
| `OPERATIONS_EMAIL_FILE` | Optional один private operator email без display name/newlines. Plaintext `OPERATIONS_EMAIL` запрещён даже пустой; адрес не логируется. |
| `SMTP_ADDRESS`, `SMTP_USER`, `SMTP_FROM`, `SMTP_PASSWORD_FILE`, `SMTP_CA_FILE` | Existing TLS SMTP settings для outbox/operations. Password нужен при SMTP_USER, CA optional для своего trust root. Timeout/недоставка не разрешают менять права/данные. |

В Compose disabled optional bot/adapter token/email files используют `/dev/null` и не
читаются отключённым каналом. При включении задайте реальный file; пустой
mount не считается credentials. Secrets **не** выдаются в `/config.json`.
Readiness PG+Redis+River, graceful stop 75s и logs/email best-effort —
[С44](s44-process-operations.md). Для живого Telegram нужна отдельная приёмка.

## Trial, bonus, панели и серверный пул

| Настройки | Default/назначение |
| --- | --- |
| `TRIAL_ENABLED` | false. Admission/источник/legacy trial-used guards принадлежат subscriptions. Не включает referral льготу. |
| `TRIAL_PERIOD`, `TRIAL_TRAFFIC_GB`, `BONUS_DEVICES_COUNT` | 3 days, 15 GiB, 1 device. Traffic 0 — unlimited; диапазоны/overflow/one-trial проверяет owning операция. BONUS здесь используется текущими trial/access operations, не новая promo/referral функция. |
| `PANEL_ID` | Stable legacy/default panel identity; обязателен для включённого trial. Base acceptance Compose сохраняет `dedicated-acceptance`, не переименовывайте identity существующих данных. |
| `PANEL_URL`, `SUBSCRIPTION_BASE_URL` | HTTPS base URLs без credentials/query/fragment; subscription base включает нужный path. Не строить domain/port из имени продукта. |
| `PANEL_USERNAME`, `PANEL_PASSWORD_FILE`, `PANEL_TOKEN_FILE`, `PANEL_CA_FILE` | Выбрать token **либо** username/password; смешение запрещено. CA для private TLS, validation не отключается. Acceptance base использует token, local overlay — password. |
| `PANEL_DUPLICATE_GUARD_VERIFIED` | false. Включать только после проверки duplicate/idempotent panel contract на выбранном target; не разрешает live panel или обход funding/trial guards. |

Server registry, per-server API/subscription base, auth mode/credential и CA
принадлежат `vpn` и защищённым server-management операциям ([С38](s38-server-management.md)).
Существующие server/account/VPN/subscription identities, assignment и encrypted
credentials сохраняются в DB; глобальный env не remap существующих клиентов.
Планы/версии/цены/длительности/traffic profiles хранятся у `catalogue` через
его операции, не дублируются в новом settings store.

## Оплаты и валюты

Все sales flags в Go default false. Включение требует соответствующий overlay
и credentials; выключение новых продаж **сохраняет** credentials для pending
callbacks/reconcile/refunds. Не удалять старую конфигурацию до сверки обязательств.
Browser return URL не является подтверждением денег.

| Provider / overlay | Используемые настройки | Quote currency |
| --- | --- | --- |
| YooMoney / `compose.yoomoney.yml` | `SHOP_PAYMENT_YOOMONEY_ENABLED`, `YOOMONEY_WALLET_ID`, `YOOMONEY_NOTIFICATION_SECRET_FILE` | RUB |
| Manual / `compose.manual.yml` | `SHOP_PAYMENT_MANUAL_ENABLED`, `MANUAL_CARD_DETAILS_FILE` (private plain text) | RUB |
| YooKassa / `compose.yookassa.yml` | `SHOP_PAYMENT_YOOKASSA_ENABLED`, `YOOKASSA_SHOP_ID`, `YOOKASSA_TOKEN_FILE`, `SHOP_EMAIL`, `YOOKASSA_TEST_MODE` | RUB |
| Cryptomus / `compose.cryptomus.yml` | `SHOP_PAYMENT_CRYPTOMUS_ENABLED`, `CRYPTOMUS_MERCHANT_ID`, `CRYPTOMUS_API_KEY_FILE` | USD |
| Heleket / `compose.heleket.yml` | `SHOP_PAYMENT_HELEKET_ENABLED`, `HELEKET_MERCHANT_ID`, `HELEKET_API_KEY_FILE` | USD |
| Native Stars | `SHOP_PAYMENT_STARS_ENABLED`, main Telegram settings выше | XTR |

YooKassa test mode default false; это явно выбранный режим provider, не
универсальный dev bypass. Локальные provider transports/Stars fixtures
инъецируются тестами, не production env. `LOCAL_YOOMONEY_FIXTURE_ENABLED`
принадлежит только native overlay и использует synthetic wallet/secret.
Нет v2 `SHOP_CURRENCY`: provider выбирает currency, catalogue имеет точную
RUB/USD/XTR цену. Отсутствующая цена не конвертируется и не подставляется.

## Frontend/Caddy: public runtime, не build args

| Настройки | Условия |
| --- | --- |
| `PRODUCT_NAME` | Optional plain text ≤128 UTF-8 bytes, без control chars/blank. React text/title безопасно отображают кавычки/HTML как текст. Отсутствие сохраняет ru/en generic labels. |
| `TERMS_VERSION`, `PRIVACY_VERSION` | Required, ≤128 bytes в runtime generator; согласованы с backend. |
| `TERMS_URL`, `PRIVACY_URL`, `SUPPORT_URL` | Required ≤2048 bytes, HTTPS без user/password; support допускает mailto, Telegram links не заменяют browser support. |
| `WEB_PUBLIC_CONFIG_DIR` | Generator output, default `/run/web-public`; writable private tmpfs при read-only image. |
| `CABINET_HOST` | Caddy host, фактический TLS certificate соответствует ему. Domain не build-time константа. |
| `CABINET_MAINTENANCE` | Transitional gateway stop-traffic flag; это не persisted business maintenance С42, не drain/backup. |
| `PUBLIC_CERT_FILE`, `PUBLIC_KEY_FILE`, `ADAPTER_CERT_FILE`, `ADAPTER_KEY_FILE` | TLS mounts gateway. Private keys вне env/Git; adapter listener остаётся закрытым от public routes. |

`/config.json` содержит только allowlist public fields: versions/URLs и optional
productName. Нет DB/SMTP/panel/token/recipient. Caddy и browser не кешируют config;
build не включает deployment env. Missing/invalid config закрывает registration
с ru/en alert. Local Vite fixture `web/scripts/local-public-config.json` — только
dev/preview; поставляемый Caddy использует runtime generator.

## Compose, backup и локальные проверки

`APP_RUNTIME_UID`, `APP_RUNTIME_GID` (10001), `APP_NETWORK_SUBNET`,
`APP_GATEWAY_IP`, `PG_USER`, `BASE_DATABASE`, `PG_PASSWORD_FILE` задают существующую
Compose инфраструктуру. Заранее проверьте UID/file access и свободную сеть.
`LOCAL_STATE_DIR`, `SMTP_AUTH_FILE`, `WEB_TRIAL_API_CA_FILE` нужны соответствующим
local/legacy overlays; не подключайте их к чужим ресурсам.
`BACKUP` сохраняет PostgreSQL/attachments, **не env/secrets/certs/Redis/panel**:
сохраняйте приватные deployment files отдельно и сверяйте с manifest revision.
`RESTORE_DATABASE_URL_FILE` и CLI operator-file остаются с absolute/canonical/
0600/owner правилами [С43](s43-backup-restore.md); проверять restore отдельной DB.

Connected tests: `TEST_DATABASE_URL_FILE`, `TEST_REDIS_URL_FILE`,
`TEST_POSTGRES_COMPOSE_FILE`, `TEST_POSTGRES_FIXTURE_FILE`. Обе заданные fixture
формы обязаны выбрать **один** безопасно проверенный PostgreSQL container;
ошибка explicit input запрещает fallback. Используйте `backup_restore.py up`
в своём worktree и его private metadata/dynamic ports, затем `down` только
этого project. Test/UI environment и faults не являются product settings.
`E2E_PORT`, `E2E_MODE`, `TEST_ORIGIN`, `RUN_BROWSER_TESTS` — проверочные inputs.

## Legacy Python: перечень для переноса, не включение Р7

Источник `app/config.py`, `.env.example`/`docker-compose.yml` до #54. Python —
Telegram-only программа: без bot token она не запускается. Web-only v2 **не**
запускает её. Если задан `_FILE`, он имеет приоритет и обязан читаться/быть
непустым UTF-8; bad file не заменяется plaintext. Без `_FILE` остаётся старый
env fallback только для совместимости. В deployment секреты задавайте файлами.

| Используемые legacy настройки | Перенос / состояние |
| --- | --- |
| `BOT_ADMINS`, `BOT_DEV_ID`, `BOT_SUPPORT_ID`, `BOT_DOMAIN`, `BOT_PORT`, `BOT_USE_WEBHOOK`, `TELEGRAM_API_URL`, `TELEGRAM_API_IS_LOCAL`, `BOT_TOKEN_FILE`, `WEBHOOK_SECRET_FILE` | Legacy actors/HTTP/webhook/self-hosted API. Native main — polling в общем Go lifecycle; old modes не включаются автоматически. Domain задаёт оператор. |
| `SUPPORT_BOT_TOKEN_FILE`, `SUPPORT_GROUP_ID`, `BOT_TIMEZONE`, `AUDIT_RETENTION_DAYS` | Legacy support/schedules; native switches/timezone выше, не два исполнителя. |
| `SHOP_APPROVAL_REQUIRED`, `SHOP_EMAIL`, `SHOP_CURRENCY`, `SHOP_TRIAL_ENABLED`, `SHOP_TRIAL_PERIOD`, `SHOP_TRIAL_TRAFFIC_GB`, `SHOP_BONUS_DEVICES_COUNT` | Legacy currency/buttons/approval/trial defaults отличаются от v2. SHOP_EMAIL deployment-owned без product-domain default, обязателен и проверяется при YooKassa-on. Сверять фактические значения и grants до переноса. |
| `SHOP_REFERRED_TRIAL_ENABLED`, `SHOP_REFERRED_TRIAL_PERIOD`, `SHOP_REFERRER_REWARD_ENABLED`, `SHOP_REFERRED_REWARD_TYPE`, `SHOP_REFERRER_LEVEL_ONE_PERIOD`, `SHOP_REFERRER_LEVEL_TWO_PERIOD`, `SHOP_REFERRER_LEVEL_ONE_RATE`, `SHOP_REFERRER_LEVEL_TWO_RATE` | Пока только legacy. Фактически reader называется **SHOP_REFERRED_REWARD_TYPE**; иначе написанный REFERRER alias не читается. Р7 #50–#52 уточняет данные/операции/параметры, здесь не вводится Go referral/promo flags. |
| `SHOP_PAYMENT_STARS_ENABLED`, `SHOP_PAYMENT_CRYPTOMUS_ENABLED`, `SHOP_PAYMENT_HELEKET_ENABLED`, `SHOP_PAYMENT_YOOKASSA_ENABLED`, `SHOP_PAYMENT_YOOMONEY_ENABLED`, `SHOP_PAYMENT_MANUAL_ENABLED` | Legacy Stars default true и fallback на Stars при всех выключенных, в Go такого fallback нет. Переносит owning payment сценарий/С46. |
| `XUI_USERNAME`, `XUI_PASSWORD_FILE`, `XUI_TOKEN_FILE` | Legacy panel auth; base/subscription URLs уже в server DB, прежние XUI_SUBSCRIPTION_PORT/PATH больше не читаются. |
| `CRYPTOMUS_API_KEY_FILE`, `CRYPTOMUS_MERCHANT_ID_FILE`, `HELEKET_API_KEY_FILE`, `HELEKET_MERCHANT_ID_FILE`, `YOOKASSA_TOKEN_FILE`, `YOOKASSA_SHOP_ID`, `YOOMONEY_NOTIFICATION_SECRET_FILE`, `YOOMONEY_WALLET_ID`, `MANUAL_CARD_DETAILS_FILE` | Legacy providers, string credentials через file reader; numeric shop ID typed. Go merchant IDs public env, не переносить secret bytes в Git. |
| `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USERNAME`, `DB_PASSWORD_FILE`, `REDIS_HOST`, `REDIS_PORT`, `REDIS_DB_NAME`, `REDIS_USERNAME`, `REDIS_PASSWORD_FILE` | Legacy SQLite/default Redis либо явно заданные DSNs. Target v2 — DATABASE/REDIS URL files, не reuse default legacy DB. |
| `LOG_LEVEL`, `LOG_FORMAT`, `LOG_ARCHIVE_FORMAT` | Legacy logging/archive; Go безопасные structured lifecycle states не принимают произвольные format/env dumps. |
| `WEB_TRIAL_API_URL`, `WEB_TRIAL_API_TOKEN_FILE`, `WEB_TRIAL_API_CA_FILE`, `BOT_OPERATOR_IDS` | Transitional adapter only; HTTPS/file-only token и один active executor. Удаление у #54. |

Реферальные данные/бонусы/промо и неизвестные legacy trial/payment facts не
объявляются перенесёнными этой сверкой. С46/import и С47/cutover подтверждают
их отдельно. С42 [maintenance](s42-maintenance.md) сохраняет права/audit/replay;
С43 [backup](s43-backup-restore.md) сохраняет restore guards;
С44 [process operations](s44-process-operations.md) сохраняет readiness/drain.
