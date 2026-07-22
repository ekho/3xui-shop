# План реализации MVP — Telegram-бот для 3x-ui

_На базе форка `snoups/3xui-shop`. Дата: 3 июля 2026._

## Исходные данные

- **База:** форк `snoups/3xui-shop` (MIT, архив upstream — форкаем и владеем).
- **Стек (из репозитория):** Python 3.12, `aiogram 3.15`, `py3xui 0.3.2`, SQLAlchemy 2 (async) + `aiosqlite`, Alembic, Redis, APScheduler, Docker.
- **Панель:** 3x-ui. **Оплата:** Telegram Stars + крипта (Cryptomus/Heleket уже есть; CryptoBot — опционально) + карта→карта вручную.
- **Согласовано:** открытая регистрация + апрув админа; подписка = срок + трафик + устройства; продление = авто-рекуррент Stars (если первая оплата в Stars) иначе напоминания по сроку/трафику; триал — в MVP.

---

## Что уже есть в базе (переиспользуем)

| Возможность | Где в коде | Статус |
|---|---|---|
| Каркас бота (роутеры, middlewares, filters, i18n ru/en) | `app/bot/routers/*`, `app/bot/middlewares/*` | ✅ Готово |
| Абстракция платежей `PaymentGateway` + `GatewayFactory` | `app/bot/payment_gateways/_gateway.py`, `gateway_factory.py` | ✅ Расширяемая |
| Шлюзы: **Stars, Cryptomus, Heleket**, YooKassa, YooMoney | `payment_gateways/*.py` | ✅ (офиц. выключим) |
| Работа с 3x-ui: создать/обновить клиента с **`total_gb` (трафик)** и **`limit_ip` (устройства)**, чтение трафика | `app/bot/services/vpn.py` | ✅ Трафик поддержан на уровне API |
| Мульти-сервер пул + распределение | `app/bot/services/server_pool.py`, `db/models/server.py` | ✅ Готово |
| Триал (`is_trial_used`) | `routers/subscription/trial_handler.py` | ✅ Есть |
| Промокоды, 2-уровневая рефералка, инвайт-кампании | `routers/*`, `db/models/*` | ✅ Есть (в Later) |
| Напоминания об истечении (APScheduler, дедуп через Redis) | `app/bot/tasks/subscription_expiry.py` | ⚠️ Только по сроку, один порог |
| Админка: пользователи, серверы, промо, рассылка, бэкап, maintenance | `routers/admin_tools/*` | ✅ Есть |
| Поддержка, профиль, страница загрузки клиентов | `routers/support`, `profile`, `download` | ✅ Есть |
| Авто-отключение по истечении | нативно в 3x-ui через `expiry_time` | ✅ Панель сама |

---

## Gap-анализ: что дописать / изменить / выключить

| # | Задача | Что делаем | Где | Объём |
|---|---|---|---|---|
| G1 | **Апрув-гейт** | Поле `approval_status` (pending/approved/rejected) в `User`; на `/start` создавать pending + уведомлять админа с кнопками Approve/Reject; блокировать покупку/триал до апрува | `db/models/user.py` (+миграция), стартовый хендлер/`middlewares`, новый `routers/admin_tools/approval_handler.py`, новый filter | **L** |
| G2 | **Трафик в тарифах** | Добавить `traffic_gb` в модель плана и `plans.json`; прокинуть в `create_client(total_gb=…)`/`update_client`; показать остаток в профиле (данные уже есть) | `app/bot/models/plan.py`, `plans.json`, `services/subscription.py`, `routers/profile` | **S–M** |
| G3 | **Ручная карта + модерация** | Новый шлюз `ManualCard(PaymentGateway)`: `create_payment` показывает реквизиты + создаёт PENDING-транзакцию; кнопка «Я оплатил» (+опц. чек); админ Approve/Reject → общий `_on_payment_succeeded` | новый `payment_gateways/manual_card.py`, `gateway_factory.py`, `config.py`, `constants.py`, admin-хендлер | **M** |
| G4 | **Авто-рекуррент Stars** | В `create_invoice_link` добавить `subscription_period=2592000`; обрабатывать повторные `successful_payment` (extend); хранить charge id; отмена/статус в профиле; фолбэк при неудаче | `payment_gateways/telegram_stars.py`, путь `_on_payment_succeeded`, `routers/profile` | **M** |
| G5 | **Напоминания по трафику + пороги** | Расширить задачу: пороги по сроку (3д/1д/день X) и по трафику (80%/100%) через `client_data.traffic_remaining`; починить кнопку «Продлить» (в коде помечена как BUG) | `app/bot/tasks/subscription_expiry.py`, клавиатура | **M** |
| G6 | **CryptoBot (опц.)** | Если нужен именно @CryptoBot — новый шлюз по образцу `cryptomus.py` (Crypto Pay API + вебхук). Иначе используем готовый Cryptomus | новый `payment_gateways/crypto_bot.py` | **M** (опц.) |
| G7 | **Выключить офиц. эквайринг** | `PAYMENT_YOOKASSA_ENABLED=False`, `PAYMENT_YOOMONEY_ENABLED=False`; оставить Stars + Cryptomus/CryptoBot + Manual | `.env` | **XS** |
| G8 | **Сброс трафика при продлении** | Убедиться, что продление сбрасывает счётчик трафика (reset client traffic в 3x-ui) и продлевает срок | `services/vpn.py` `update_client` | **S** |
| G9 | **(Опц.) PostgreSQL** | Для роста — заменить `aiosqlite`→`asyncpg`, обновить URL и Alembic env. Для MVP можно остаться на SQLite | `config.py`, `db/*`, `docker-compose.yml` | **S** (опц.) |

Легенда объёма: XS < S < M < L.

---

## Этапы реализации (порядок)

### Этап 0. Инфраструктура и запуск базы
- Форкнуть репозиторий, поднять по инструкции (Docker Compose), тестовый бот-токен от @BotFather.
- Заполнить `.env`: `BOT_TOKEN`, `BOT_ADMINS`, `XUI_*`, домен/TLS; включить Stars, выключить YooKassa/YooMoney (**G7**).
- Проверить связь с 3x-ui (создание тестового клиента), запуск, логи.
- **DoD:** бот запускается, видит панель, отвечает на `/start`.

### Этап 1. Ядро «покупка → активация» (без гейта)
- Прогнать сквозной путь на готовых рельсах: Stars (разовый) и Cryptomus → `_on_payment_succeeded` → `create_client` → выдача ключа/QR/инструкции.
- Настроить тарифы в `plans.json` (пока срок + устройства).
- Проверить идемпотентность вебхука Cryptomus (дубли не создают вторую подписку).
- **DoD:** оплата Stars и криптой создаёт клиента в 3x-ui и выдаёт подписку.

### Этап 2. Апрув-гейт + триал за гейтом (**G1**)
- Миграция `approval_status`; на `/start` — pending + уведомление админам (inline Approve/Reject).
- Filter/middleware: непрошедшим — «ожидайте подтверждения», блок покупки/триала.
- После апрува — открыть меню и кнопку триала.
- **DoD:** новый юзер проходит апрув, только затем покупает/берёт триал.

### Этап 3. Трёхмерные тарифы + напоминания (**G2, G5, G8**)
- Добавить `traffic_gb` в план/`plans.json`, прокинуть в create/update client; показать остаток трафика в профиле.
- Расширить крон: пороги по сроку и трафику + рабочая кнопка «Продлить».
- Сброс трафика при продлении.
- **DoD:** тариф с лимитом трафика; уведомления и по сроку, и по трафику; продление сбрасывает трафик и продлевает срок.

### Этап 4. Ручная карта с модерацией (**G3**)
- Шлюз `ManualCard`: реквизиты + PENDING; «Я оплатил» (+чек); админ Approve/Reject → активация.
- **DoD:** оплата картой активируется после подтверждения админом.

### Этап 5. Авто-рекуррент Stars (**G4**)
- Рекуррентный инвойс Stars при первой оплате звёздами; обработка автосписаний (extend); управление/отмена в профиле; фолбэк на напоминания при сбое (сценарий L62).
- **DoD:** подписка, оплаченная Stars, продлевается автоматически; сбой корректно уводит в ручное продление.

### Этап 6. Опции
- **G6** CryptoBot (если нужен) · **G9** переезд на PostgreSQL · косметика/локализация текстов.

---

## Инфраструктура и деплой

> Уточнено (актуальная версия — в `telegram-bot-implementation-plan-FULL.md`, §8): бот работает за **внешним** Traefik (TLS не терминируется в боте — убрать встроенный traefik/Let's Encrypt), секреты прокидываются через **docker compose secrets** (`/run/secrets` + чтение `*_FILE` в `config.py`), и **README.md** — обязательный артефакт с пошаговым запуском и полным описанием всех переменных и секретов.

- **Docker Compose:** бот + Redis (+ Postgres, если G9). SQLite-том по умолчанию.
- **Домен + TLS (Let’s Encrypt):** нужен для вебхуков **крипты** (Cryptomus/CryptoBot) и subscription-порта 3x-ui. **Stars и ручная карта вебхук не требуют** — можно стартовать без публичного домена, если крипту подключать позже.
- **3x-ui:** SSL для панели/подписки, настроить inbound (первый используется), subscription-сервис (порт/путь), выключить шифрование конфигурации (по README).
- **Секреты `.env`:** токены платёжек, реквизиты карты для ручного шлюза, `BOT_ADMINS`, `XUI_*`.

## Риски и на что смотреть

- **Stars pre-checkout ≤10 с** — отвечать быстро; в aiogram это нативно, следить за нагрузкой.
- **Идемпотентность** платежей — проверять статус транзакции до провижининга (дубли вебхуков).
- **SQLite** — при росте/нагрузке возможны проблемы; план перехода на Postgres (G9) держать наготове.
- **Upstream архивный** — фиксов не будет; берём как свой код. Полезные доработки есть в форках-наследниках.
- **Рекуррент Stars** — обрабатывать неуспешные списания и отмену пользователем.
- **Юр./налоги (VPN + приём оплаты)** — вопрос решается отдельно, вне этого плана.

## Definition of Done (MVP)

Сквозные сценарии из карты работают: `/start` → апрув админом → триал → выбор трёхмерного тарифа → оплата (Stars / крипта / ручная карта) → создание клиента в 3x-ui с лимитами → выдача ключа/QR/инструкции → «моя подписка» с остатком трафика → напоминания по сроку и трафику → продление (авто-Stars или ручное) → базовая админка (апрув, управление, модерация платежей, рассылка, бэкап, maintenance).

## Быстрая карта «куда править»

- Апрув: `app/db/models/user.py`, стартовый хендлер, `app/bot/routers/admin_tools/` (+ новый filter/middleware).
- Тарифы/трафик: `app/bot/models/plan.py`, `plans.json`, `app/bot/services/subscription.py`, `app/bot/services/vpn.py`, `app/bot/routers/profile/`.
- Платёжные шлюзы: `app/bot/payment_gateways/` (+ `gateway_factory.py`, `app/config.py`, `app/bot/utils/constants.py`).
- Крон/напоминания: `app/bot/tasks/subscription_expiry.py`.
