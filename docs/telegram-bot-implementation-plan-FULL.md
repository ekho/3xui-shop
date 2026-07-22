# Telegram-бот для 3x-ui — единый план реализации MVP

_Мастер-документ. База: форк `snoups/3xui-shop`. Дата: 3 июля 2026._

Сводит воедино: контекст и решения, архитектуру форка, gap-анализ, все этапы (0–6) и детализацию Этапов 2–5 до уровня кода. Полные код-сниппеты по каждому этапу — в отдельных файлах `telegram-bot-stage{2..5}-*.md` (ключевые приведены и здесь).

---

## 0. Контекст и решения

- **Решение:** свой бот на коде (не n8n) на базе форка `snoups/3xui-shop` (MIT; upstream архивный — форкаем и владеем).
- **Стек (из репозитория):** Python 3.12, `aiogram 3.15`, `py3xui ^0.3.2` (резолвится до 0.3.9; линейка 0.3.x–0.6.0 — легаси-эндпоинты, только 3x-ui ≤v3.0 — см. B6), SQLAlchemy 2 (async) + `aiosqlite`, Alembic, Redis, APScheduler, Docker.
- **Панель:** 3x-ui. **Оплата:** Telegram Stars + крипта (Cryptomus готова; CryptoBot опц.) + карта→карта вручную. Официальный эквайринг (YooKassa/YooMoney) выключаем.
- **Продуктовые параметры (согласованы):**
  - Доступ: открытая регистрация + **апрув админа**.
  - Подписка: **срок + трафик + устройства**.
  - Продление: **авто-рекуррент Stars**, если первая оплата в Stars; иначе — **напоминания по сроку/трафику**.
  - **Триал** — в MVP.
- **Антифрод:** в MVP не требуется (доверенные пользователи). Налоги/легальность — вне этого документа.

## 1. Архитектура форка (что переиспользуем)

| Возможность | Где | Статус |
|---|---|---|
| Каркас aiogram: роутеры/middlewares/filters/i18n ru+en | `app/bot/*` | ✅ |
| Абстракция платежей `PaymentGateway` + `GatewayFactory` | `app/bot/payment_gateways/_gateway.py`, `gateway_factory.py` | ✅ расширяемая |
| Шлюзы Stars, Cryptomus, Heleket, YooKassa, YooMoney | `payment_gateways/*.py` | ✅ (офиц. выключим) |
| 3x-ui через py3xui: `create_client/update_client` c `total_gb`+`limit_ip`, чтение трафика | `services/vpn.py` | ✅ трафик поддержан на уровне API |
| Мультисервер-пул | `services/server_pool.py`, `db/models/server.py` | ✅ |
| Триал (`is_trial_used`) | `routers/subscription/trial_handler.py` | ✅ |
| Промокоды, 2-ур. рефералка, инвайты | `routers/*`, `db/models/*` | ✅ (Later) |
| Напоминания об истечении (APScheduler+Redis дедуп) | `tasks/subscription_expiry.py` | ⚠️ только срок, 1 порог, кнопка «Продлить» закомментирована |
| Провижининг после оплаты: `create/extend/change_subscription(user, devices, duration)` | `_gateway._on_payment_succeeded` → `services/vpn.py` | ✅ (без трафика) |
| Stars: `pre_checkout` + `successful_payment` | `routers/subscription/payment_handler.py` | ✅ разовый |
| Админка: юзеры, серверы, промо, рассылка, бэкап, maintenance | `routers/admin_tools/*` | ✅ |
| Авто-отключение по истечении | нативно в 3x-ui (`expiry_time`) | ✅ |

## 2. Gap-анализ

| # | Задача | Суть | Объём |
|---|---|---|---|
| G1 | Апрув-гейт | статус `approval_status`, middleware-гейт, апрув админом | **L** |
| G2 | Трафик в тарифах | `traffic_gb` в план→`SubscriptionData`→`total_gb` (ГБ→байты) | **S–M** |
| G3 | Ручная карта | новый шлюз + ветка «реквизиты/Я оплатил» + модерация | **M** |
| G4 | Авто-рекуррент Stars | `subscription_period`, `is_recurring`→extend, отмена | **M** |
| G5 | Напоминания по трафику + мультипороги | расширить крон, починить кнопку «Продлить» | **M** |
| G6 | CryptoBot (опц.) | новый шлюз по образцу cryptomus | **M** опц. |
| G7 | Выключить офиц. эквайринг | флаги в `.env` | **XS** |
| G8 | Сброс трафика при продлении | reset client traffic в 3x-ui | **S** |
| G9 | (Опц.) PostgreSQL | `aiosqlite`→`asyncpg` | **S** опц. |
| G10 | Docker secrets | чтение секретов из файлов (`*_FILE`) в `config.py`; `secrets:` в compose | **S** |
| G11 | Внешний Traefik | убрать встроенный traefik/letsencrypt, подключить бота к внешней сети reverse-proxy + labels | **XS–S** |
| G12 | README.md | подробная инструкция запуска + полное описание всех переменных и секретов | **S** |

## 2A. Аудит плана: блокеры и обязательные правки

_Внесено по итогам ревью 2026-07-04 (многоагентный разбор + адверсарная верификация по коду форка `snoups/3xui-shop@main` и докам Bot API / aiogram / py3xui / 3x-ui). Все пункты ниже подтверждены с высокой уверенностью; ID (`B*`/`M*`/`m*`) используются как ссылки из детальных этапов ниже._

### Критические блокеры MVP (сделать до/во время профильного этапа — большинство кросс-этапные)

- **B1 — Апрув-гейт глотает платёжные апдейты; reject не отменяет Stars-рекуррент (Этап 2 × 5).** Все middleware форка висят на `dispatcher.update.middleware`, а `DBSessionMiddleware` кладёт `data["user"]` из `event.event.from_user` (есть и у `PreCheckoutQuery`), поэтому гейт срабатывает и на платёжных событиях. Сниппет пропускает только `Message` с `/start` и отвечает только `CallbackQuery` — остальное молчаливый `return None`. Итог: (1) `pre_checkout_query` от не-approved остаётся без ответа → Telegram отменяет платёж через 10 с; (2) `successful_payment` — это `Message` без `text` → **дропается**; с рекуррентом Stars (списывается автоматически, приходит только `successful_payment(is_recurring=True)`) деньги списаны, а хендлер не вызван: ни транзакции, ни продления, ни следа для рефанда; (3) `on_approval` при reject не отменяет активную подписку → Telegram продолжает списывать.
  **Фикс:** в `ApprovalMiddleware` перейти от «чёрного списка» к явной обработке: `PreCheckoutQuery` от не-approved → `await ev.answer(ok=False, error_message=...)` (`error_message` обязателен при `ok=False`); `Message` с `ev.successful_payment` — **всегда** пропускать в хендлер. В хендлере `successful_payment` — ветка для не-approved: записать `Transaction` (аудит), `bot.refund_star_payment(user_id, charge_id)` (для рекуррента рефанд заодно отменяет подписку), сбросить `is_stars_auto_renew`, уведомить. В `on_approval` при reject: если `user.is_stars_auto_renew` — `bot.edit_user_star_subscription(..., is_canceled=True)` в try/except + `User.update(is_stars_auto_renew=False)`. Пункты про `PreCheckoutQuery`/`successful_payment` внести уже на Этапе 2 (разовый Stars существует с Этапа 1), отмену при reject — кросс-этапный пункт Этапа 5.

- **B2 — Нет идемпотентности провижининга → двойное продление за один платёж (Этап 1/5, разд. 9).** В форке `_on_payment_succeeded` статус транзакции **не проверяет** — читает, безусловно ставит COMPLETED, провижинит. Cryptomus переотправляет вебхуки (resend до 10 раз + ретраи на не-200; `webhook_handler` форка отдаёт 400 на любое исключение) → повторный «paid» → второй `extend` → двойной срок. У Stars `Transaction.create` при дубле `payment_id` возвращает `None` без исключения, а сниппет Этапа 5 результат игнорирует.
  **Фикс:** compare-and-set — `UPDATE transactions SET status='completed' WHERE payment_id=:id AND status='pending'`, провижинить только при `rowcount==1`. **Важно:** CAS в `_on_payment_succeeded` сломает Stars-флоу (там транзакция создаётся сразу COMPLETED) → для Stars дедуп делать в `successful_payment` (`txn = await Transaction.create(...); if txn is None: return` — до `User.update` и `handle_payment_succeeded`), а CAS применять только к шлюзам с PENDING-жизненным циклом (Cryptomus/manual). Обработать падение провижининга уже после CAS (флаг `provisioned_at`/откат/алерт — иначе «оплачено, не выдано», сценарий 58). Вебхуку возвращать 200 для любой финализированной транзакции; в `_on_payment_canceled` тоже проверять статус (cancel после completed не должен перетирать).

- **B3 — Модерация ручной оплаты неатомарна и с дырявым гардом (Этап 4).** Сообщение уходит каждому админу, `edit_text` правит только копию кликнувшего. Read-then-act с окном гонки во весь сетевой вызов: Approve/Approve → двойной провижининг; Approve+Reject → доступ выдан И «отказано». Гард ловит только `status == COMPLETED`, поэтому **Reject→Approve проходит** (доступ по отклонённому платежу).
  **Фикс:** явный CAS в хендлере модерации **до** вызова шлюза: `update(Transaction).where(payment_id==pid, status==PENDING).values(status=<целевой>)`; действует только `rowcount==1`, остальным `callback.answer("уже обработано")`. Гард обязан быть `status == PENDING` (через WHERE), а не `!= COMPLETED` — закрывает Approve/Approve, Reject→Approve и двойной Reject. CAS сразу в финальный статус безопасен (повторный COMPLETED в `_on_payment_succeeded` идемпотентен). Сохранять `chat_id+message_id` всех разосланных админ-сообщений (Redis по `payment_id`) и после решения снимать клавиатуры у всех (`edit_message_reply_markup`) — это UX; безопасность обеспечивает CAS.

- **B4 — Перезапись `stars_charge_id` каждым платежом ломает отмену автопродления (Этап 5).** Для `editUserStarSubscription` нужен `charge_id` **первого** платежа (`is_first_recurring=True`). Сниппет сохраняет его при каждом `successful_payment` → после первого автопродления отмена падает `CHARGE_ID_INVALID`. Любой разовый Stars-платёж (`is_extend`/`is_change`/тариф ≠30 дн.) затирает поле и ставит `is_stars_auto_renew=False` — бот «теряет» живую подписку.
  **Фикс:** разделить обновление: (1) `stars_charge_id` — только при `sp.is_first_recurring`; (2) `is_stars_auto_renew=True` — при любом `sp.is_recurring` (самовосстановление после ре-активации в Telegram UI); (3) разовые Stars-платежи не трогают ни то, ни другое. При смене тарифа новая подписка придёт с `is_first_recurring=True` и корректно перезапишет charge id. Отмену из профиля обернуть в `try/except TelegramBadRequest`.

- **B5 — Напоминания по сроку не фильтруют Stars-рекуррент → бот сам провоцирует двойную оплату (Этап 3 × 5).** Крон смотрит только на `expiry_time` в панели; у рекуррент-юзера он продлевается каждый месяц, поэтому на днях 27–29 цикла придёт «истекает через 3 дня» с кнопкой «Продлить». Оплатит вручную — через день Telegram спишет рекуррент за тот же период.
  **Фикс:** (1) в `tasks/subscription_expiry.py` при `user.is_stars_auto_renew` пропускать пороги по сроку (или менять текст на «спишется автоматически ~N числа», без кнопки). (2) Сохранять `SuccessfulPayment.subscription_expiration_date` → `user.stars_expires_at`; в кроне если `now > stars_expires_at + грейс(12–24ч)` и нового рекуррента не было — снять `is_stars_auto_renew`, вернуть в обычные напоминания (это же закрывает отмену юзером в Telegram без вебхука). (3) Трафик-пороги для рекуррент-юзеров оставить, но кнопку вести в смену тарифа/отмену автопродления, не в разовое extend. (4) **Внести `tasks/subscription_expiry.py` в список правок Этапа 5** — сейчас зависимость этапов не зафиксирована.

- **B6 — py3xui 0.3.x несовместим с 3x-ui v3.1+ → весь VPN-слой может получать 404 (стек, разд. 0/8.3).** py3xui 0.3.x–0.6.0 ходит только в легаси `panel/api/inbounds/*`; с 3x-ui v3.1.0 клиентские операции переехали в `panel/api/clients/*` (поддержка — только py3xui 0.7.0, который не работает с панелями ≤v3.0). План не фиксирует версию панели вообще: оплата пройдёт, клиент не создастся.
  **Фикс:** (1) пин форка — caret `^0.3.2` (резолвится до 0.3.9), зафиксировать и lock-файл. (2) Переход на 0.7.0 — **не drop-in** (изменились пути и семантика: `client.update`/`delete`/`get` по email, `reset_stats` игнорирует `inbound_id` в пути): в Этап 0/1 добавить адаптацию и прогон всех вызовов `services/vpn.py` на тестовой панели целевой версии. (3) В разд. 9 — матрица совместимости py3xui↔3x-ui + запрет автообновления панели без синхронного обновления бота. (4) В README — поддерживаемая версия панели и процедура совместного апгрейда.

- **B7 — Telegram-вебхук без `secret_token` → публичный `/webhook` принимает поддельные апдейты (Этап 0/8.4).** Форк вызывает `set_webhook()` без секрета, `SimpleRequestHandler` без проверки. Домен публичен, путь фиксирован: атакующий POST'ит поддельный `successful_payment` (провижининг без оплаты) или `callback_query` с `from.id` админа (`IsAdmin` верит id из апдейта) → approve/рассылка/maintenance.
  **Фикс:** (1) секрет в docker secret `webhook_secret` (файл `./secrets/webhook_secret.txt`, читать по конвенции `*_FILE`; допустимые символы `A-Za-z0-9_-`, длина 1..256). (2) Передать в оба места `app/__main__.py`: `bot.set_webhook(url, secret_token=...)` и `SimpleRequestHandler(..., secret_token=...)` — aiogram сам сверит `X-Telegram-Bot-Api-Secret-Token` и вернёт 401 на подделки. (3) Defense-in-depth: IP-allowlist подсетей Telegram (`149.154.160.0/20`, `91.108.4.0/22`) на Traefik или `ip_filter`. Заодно исправить в `on_startup` некорректное `if await bot.get_webhook_info() != webhook_url` (объект != строка → всегда True; сравнивать `.url` и учитывать смену secret_token).

- **B8 — В compose-фрагменте плана (8.1) потерян volume `/app/data` с SQLite → при пересоздании контейнера теряются юзеры, транзакции, статусы апрува и список серверов.** Хост панели 3x-ui хранится в таблице `server` **внутри БД** (`Server.host`, не в env — поэтому `XUI_HOST` и отсутствует в списках переменных). Форк монтирует `./app/data:/app/data`, `./plans.json`, `./app/locales`, `./app/logs`.
  **Фикс:** см. правку 8.1 ниже — вернуть volumes у `bot` и `redis_data` у `redis`, либо явно подписать, что фрагмент показывает только изменения (networks/secrets/labels), а volumes/command/depends_on/restart берутся из оригинального compose без изменений.

### Инварианты платежей (сквозной принцип для Этапов 1/4/5)

Прошить во все платёжные этапы, чтобы не чинить точечно:
1. **Единый жизненный цикл транзакции:** `PENDING → COMPLETED | CANCELED`, переход — только через compare-and-set по статусу (B2, B3). Провижининг — строго после успешного CAS; повторная финализация идемпотентна.
2. **Идемпотентность по внешнему id:** дедуп по `payment_id`/`telegram_payment_charge_id` до любого изменения состояния и до провижининга.
3. **Аутентификация входящих:** Telegram-вебхук — `secret_token` (B7); Cryptomus — проверка md5-подписи как основной механизм, IP-allowlist вторичен и конфигурируем (m-Cryptomus).
4. **Кросс-гейтвейная согласованность:** при активном Stars-рекурренте не принимать молча ручное/крипто-продление (M-manual-extend); reject/смена способа оплаты обязаны согласованно отменять рекуррент.
5. **Персистентность:** БД (и Redis-дедуп) переживают пересоздание контейнера (B8, M-redis).

### Major (12, подтверждены)

`M1` Redis без volume/`depends_on`, `redis:latest` не запинен → потеря FSM и дедуп-ключей после рестарта (8.1). `M2` Дедуп-ключи трафик-напоминаний не привязаны к циклу подписки → подавление уведомлений нового периода / спам на длинных тарифах (Этап 3). `M3` Роутеры предлагается регистрировать не в том файле — реальная регистрация в `routers/__init__.py::include()` через `dispatcher.include_routers()`, а не в `__init__.py` пакетов (Этапы 2, 4). `M4` Сниппеты Этапа 4 не запустятся: `async with session()` (session — открытый `AsyncSession`, не фабрика), `bot`/`config` не в сигнатурах → `TypeError`/`NameError` на reject-пути. `M5` i18n: сообщения юзеру из админ-хендлеров уйдут на языке админа (Этапы 2, 4). `M6` Уведомление админов по `is_new_user` теряется, если первый апдейт не `/start` → юзер навсегда в pending (Этап 2). `M7` Кнопка «Продлить» из крона без `user_id` в `SubscriptionData` → провижининг упадёт на `User.get(tg_id=0)` (Этап 3). `M8` Крон «чинит» кнопку, не устранив причину (`# BUG:` в форке): `_()` в APScheduler-таске даёт дефолтную локаль (Этап 3). `M9` Ручное/крипто-продление поверх активного Stars-рекуррента → двойной биллинг (Этап 4×5). `M10` Промокоды/бонусные дни (`process_bonus_days`→`update_client` c дефолтным `total_gb=0`) обнуляют платный лимит трафика в безлимит (Этап 3). `M11` SQLite без WAL/`busy_timeout` → `database is locked` при параллельных записях вебхука и APScheduler (G9). `M12` `pending-flood`: неограниченные PENDING-заявки ручной оплаты, спам админам, риск двойного апрува одного перевода (Этап 4).

### Minor (подтверждены)

`m1` Метод сброса трафика — `client.reset_stats(inbound_id, email)` (нужен `inbound_id`), а не `resetClientTraffic/{email}`. `m2` Порог 100% — постфактум (панель уже отрезала), нет предупреждения между 80% и 100%; слать только максимальный сработавший порог. `m3` Pending-юзер не получает ответа на обычные сообщения; `startswith('/start')` пропускает `/startXXX` (парсить первый токен). `m4` Отмена рекуррента ботом необратима для юзера без кнопки «Возобновить». `m5` Хендлер отмены без `try/except` залипнет в `is_stars_auto_renew=True`. `m6` Крон: полный дамп инбаундов на каждого юзера + хрупкое `days_left == d` (использовать `get_by_email`, `<= d`, ранний skip до похода в панель). `m7` `extend-reset-order` (Этап 3, G8): на 3x-ui v3.4.2 гонки нет (`resetTraffic` сам ре-энейблит), но обернуть `reset_traffic` в try/except и не считать extend успешным до обоих вызовов. `m8` Cryptomus IP-allowlist за внешним Traefik: без проброса реального client IP даёт 403 на все callback'и (оплата без активации); сценарий «обход подделкой активирует платёж» ложный (md5-подпись). `m9` Миграции «в command» на каждый старт без бэкапа + расхождение с `create_all` (Этап 0/8.5). `m10` `env_or_file`: `open()` без `with`/`encoding` — nitpick, применять только к строковым секретам (числа/списки — типизированно через environs).

### Опровергнуто верификацией (не чинить)

- `env-or-file-cast` — план явно ограничивает `env_or_file` строковыми секретами, обхода типизации нет (осталось только `encoding='utf-8'` — см. `m10`).
- `no-reentry-into-autorenew` — авто-рекуррент работает через автосписания Telegram (`successful_payment(is_recurring=True)→extend`), а не через кнопку «Продлить»; невозможность вернуться в рекуррент после лапса — задуманное поведение.
- `concurrent-subs-double-billing` — кнопка «Купить» (`state=PROCESS`) в форке показывается только при отсутствии клиента; активный подписчик видит «Продлить»/«Сменить тариф», второй параллельной подписки штатным флоу не создать.

## 3. Этапы (порядок)

- **Этап 0 — Инфраструктура:** форк, Docker; убрать встроенный Traefik и подключить бота к **внешнему Traefik** (G11); перевести секреты на **docker compose secrets** (G10); `.env` только с несекретными значениями (Stars on, YooKassa/YooMoney off — G7); связь с 3x-ui; **черновик README** (G12). **+ Аудит: `secret_token` вебхука (B7); volumes SQLite/redis (B8, M1); SQLite WAL+busy_timeout (M11); зафиксировать версию панели ↔ py3xui (B6); миграции one-shot + бэкап (m9).** DoD: бот стартует за внешним Traefik, видит панель, секреты читаются из `/run/secrets`, вебхук отвергает апдейты без секрета, БД/Redis переживают пересоздание контейнера.
- **Этап 1 — Ядро «оплата→активация»:** сквозной путь Stars(разовый)+Cryptomus → `create_client` → выдача ключа/QR. **+ Аудит: идемпотентность провижининга через CAS-статус транзакции (B2), инварианты платежей (§2A).** DoD: оплата создаёт клиента; повторная доставка вебхука/апдейта не даёт второго провижининга.
- **Этап 2 — Апрув-гейт (G1).** ↓ детально. **Блокеры: B1; major M3, M5, M6; minor m3.**
- **Этап 3 — Трёхмерные тарифы + напоминания (G2,G5,G8).** ↓ детально. **Блокеры: B5, B6; major M2, M7, M8, M10; minor m1, m2, m6, m7.**
- **Этап 4 — Ручная карта (G3).** ↓ детально. **Блокеры: B3; major M3, M4, M9, M12.**
- **Этап 5 — Авто-рекуррент Stars (G4).** ↓ детально. **Блокеры: B4; ретро-правка крона B5; minor m4, m5.**
- **Этап 6 — Опции:** CryptoBot (G6), PostgreSQL (G9), косметика.

---

## 4. Этап 2 — Апрув-гейт (детально)

**Подход:** централизованный `ApprovalMiddleware` (по образцу `MaintenanceMiddleware`), ставится **после** `DBSessionMiddleware` (нужен загруженный `user`).

**Файлы:** `constants.py` (+`ApprovalStatus`), `db/models/user.py` (+колонка, +`update`), миграция (+backfill в `approved`), `config.py`/`.env` (+`SHOP_APPROVAL_REQUIRED`), `middlewares/approval.py` (new) + регистрация, `routers/main_menu/handler.py` (авто-апрув админа, уведомление админов, ветки pending/rejected), `routers/admin_tools/approval_handler.py` (new, approve/reject через `CallbackData`), i18n.

**Ключевое:**
```python
# constants.py
class ApprovalStatus(Enum):
    PENDING = "pending"; APPROVED = "approved"; REJECTED = "rejected"

# middlewares/approval.py
class ApprovalMiddleware(BaseMiddleware):
    def __init__(self, config): self.config = config
    async def __call__(self, handler, event, data):
        user = data.get("user")
        if not self.config.shop.APPROVAL_REQUIRED or user is None: return await handler(event, data)
        if user.approval_status == ApprovalStatus.APPROVED or await IsAdmin()(user_id=user.tg_id):
            return await handler(event, data)
        ev = event.event
        if isinstance(ev, Message) and (ev.text or "").startswith("/start"):
            return await handler(event, data)
        if isinstance(ev, CallbackQuery): await ev.answer(_("approval:notice:pending"), show_alert=True)
        return
```
```python
# routers/admin_tools/approval_handler.py
class ApprovalCallback(CallbackData, prefix="approval"):
    action: str; user_id: int

@router.callback_query(ApprovalCallback.filter(), IsAdmin())
async def on_approval(callback, callback_data, session, bot):
    status = ApprovalStatus.APPROVED if callback_data.action=="approve" else ApprovalStatus.REJECTED
    await User.update(session, tg_id=callback_data.user_id, approval_status=status)
    await bot.send_message(callback_data.user_id,
        _("approval:user:granted") if status==ApprovalStatus.APPROVED else _("approval:user:denied"))
    await callback.answer()
```
**Крайние случаи:** ~~уведомлять админов только при `is_new_user`~~ **(M6: НЕ завязывать на `is_new_user` — см. ниже)**; идемпотентность повторного клика; backfill существующих юзеров в `approved`; `SHOP_APPROVAL_REQUIRED=False` отключает гейт.

**Обязательные правки по аудиту:**
- **B1 — гейт не должен глотать платёжные апдейты.** `ApprovalMiddleware` видит ВСЕ типы апдейтов (`DBSessionMiddleware` кладёт `user` и для `PreCheckoutQuery`). Перейти на явную обработку типов вместо молчаливого `return None`:
  ```python
  ev = event.event
  if isinstance(ev, Message) and ev.successful_payment:
      return await handler(event, data)          # деньги уже списаны — пропускаем ВСЕГДА
  if isinstance(ev, PreCheckoutQuery):
      await ev.answer(ok=False, error_message=_("approval:notice:pending"))  # error_message обязателен
      return
  if isinstance(ev, Message) and (ev.text or "").split(maxsplit=1)[:1] == ["/start"]:
      return await handler(event, data)          # m3: точный разбор, не startswith
  if isinstance(ev, Message):                     # m3: любое другое сообщение — не тишина
      await NotificationService.notify_by_message(message=ev, text=_("approval:notice:pending"), duration=...)  # + redis-троттлинг
      return
  if isinstance(ev, CallbackQuery):
      await ev.answer(_("approval:notice:pending"), show_alert=True); return
  return  # апдейты без from_user (user is None) — осознанно пропускаем/блокируем
  ```
- **B1 (кросс-этап 5) — reject отменяет Stars-рекуррент.** В `on_approval` при REJECTED: если `user.is_stars_auto_renew and user.stars_charge_id` → `await bot.edit_user_star_subscription(user_id=..., telegram_payment_charge_id=user.stars_charge_id, is_canceled=True)` в try/except + `User.update(is_stars_auto_renew=False)`. Плюс ветка для не-approved в хендлере `successful_payment` (рефанд рекуррентного списания) — см. Этап 5.
- **M3 — регистрация роутера.** Реальная регистрация — `dispatcher.include_routers(...)` в `app/bot/routers/__init__.py::include()`, а НЕ в `routers/admin_tools/__init__.py` (там только `from . import ...`). Добавить `admin_tools.approval_handler.router` в этот вызов.
- **M5 — локаль получателя.** `on_approval` шлёт `bot.send_message` другому юзеру, а `SimpleI18nMiddleware` ставит локаль по `from_user` апдейта (админа). Рендерить текст юзеру в его локали по образцу `_gateway._on_payment_succeeded`:
  ```python
  target = await User.get(session, tg_id=callback_data.user_id)
  with i18n.use_locale(target.language_code or DEFAULT_LANGUAGE):
      text = _("approval:user:granted") if status==ApprovalStatus.APPROVED else _("approval:user:denied")
  await bot.send_message(callback_data.user_id, text)   # обернуть в try/except TelegramForbiddenError
  ```
  (`i18n: I18n` добавить в сигнатуру хендлера; `bot.send_message` юзеру — в try/except, чтобы блокировка бота юзером не роняла хендлер до `callback.answer()`).
- **M6 — уведомление админов не по `is_new_user`.** `DBSessionMiddleware` создаёт `User` на ЛЮБОМ первом апдейте (не только `/start`); если первый апдейт — не `/start`, гейт его заблокирует, `is_new_user` сгорит, и уведомление не уйдёт никогда. Слать уведомление при `approval_status==PENDING` (не-админ) с дедупликацией (`User.approval_requested_at` или redis-ключ), сбрасывать метку при approve/reject/повторном запросе.

**DoD:** новый юзер → pending, админам Approve/Reject, покупка/триал заблокированы; approve→доступ; reject→блок **+ отмена активного Stars-рекуррента**; админ/дев авто-approved; **pre_checkout/successful_payment не теряются гейтом; сообщения юзеру — на его языке; уведомление админам не теряется при первом не-`/start` апдейте**.

Полные сниппеты: `telegram-bot-stage2-approval.md`.

## 5. Этап 3 — Трафик в тарифах + напоминания (детально)

**Файлы:** `plans.json` (+`traffic_gb`), `models/plan.py`, `models/subscription_data.py` (+`traffic`), `payment_handler.py` (проставить `traffic`), `_gateway.py` (проброс), `services/vpn.py` (traffic_gb→total_gb, ГБ→байты, reset), `tasks/subscription_expiry.py` (пороги+кнопка), `routers/profile/*`.

**Ключевое:**
```python
# services/vpn.py
def gb_to_bytes(gb): return int(gb) * 1024**3
async def create_subscription(self, user, devices, duration, traffic_gb=0):
    if not await self.is_client_exists(user):
        return await self.create_client(user=user, devices=devices, duration=duration, total_gb=gb_to_bytes(traffic_gb))
    return False
async def extend_subscription(self, user, devices, duration, traffic_gb=0):
    ok = await self.update_client(user=user, devices=devices, duration=duration, replace_devices=True, total_gb=gb_to_bytes(traffic_gb))
    if ok: await self.reset_traffic(user)   # обнулить использованный
    return ok
```
Крон — добавить пороги по сроку `[3,1]` дн. и по трафику `[0.8,1.0]` (из `ClientData._traffic_used/_traffic_total`), каждый со своим redis-ключом, + рабочая кнопка «Продлить» (`SubscriptionData(state=EXTEND, user_id=user.tg_id, ...)`).

> ⚠️ `totalGB` в 3x-ui — **байты**: ГБ×1024³. Сброс трафика — отдельный вызов py3xui.

**Обязательные правки по аудиту:**
- **m1 — точная сигнатура сброса.** В py3xui 0.3.2 метод — `client.reset_stats(inbound_id: int, email: str)` (`inbound_id` в URL), не `resetClientTraffic/{email}`. Реализация:
  ```python
  async def reset_traffic(self, user):
      connection = await self.server_pool_service.get_connection(user)
      if not connection: return False
      inbound_id = await self.server_pool_service.get_inbound_id(connection.api)
      try:
          await connection.api.client.reset_stats(inbound_id=inbound_id, email=str(user.tg_id))
          return True
      except Exception as e:
          logger.error(f"reset_traffic {user.tg_id}: {e}"); return False
  ```
- **B6 — совместимость py3xui↔панель.** Всё выше верно для py3xui 0.3.x + 3x-ui ≤v3.0. На v3.1+ эндпоинты переехали в `panel/api/clients/*` → нужна py3xui 0.7.0 и адаптация. Зафиксировать версию панели (см. Этап 0, разд. 9).
- **m7 — порядок update→reset.** На 3x-ui v3.4.2 гонки с `XrayTrafficJob` нет (`resetTraffic` сам ре-энейблит depleted-клиента), НО: обернуть `reset_traffic` в try/except и **не считать extend успешным, пока не прошли оба вызова** (`update_client` И `reset_traffic`); при неуспехе reset — алерт админу, а не тихий крэш из `_on_payment_succeeded`.
- **M10 — промокоды не должны стирать лимит трафика.** `VPNService.update_client` безусловно делает `client.total_gb = total_gb` (дефолт `0` = безлимит), а `process_bonus_days`/`activate_promocode` зовут его без `total_gb` → снимут платный лимит. Сменить сигнатуру на `total_gb: int | None = None`; при `None` — сохранять текущий лимит (`client.total` в байтах уже получен из `get_by_email`), симметрично тому, как сохраняются устройства при `replace_devices=False`. Проверить ВСЕ вызовы `update_client`.
- **M7 — кнопка «Продлить» из крона.** В `extend_kb(user)` явно проставлять `user_id=user.tg_id` (иначе payload уйдёт с `user_id=0`, оплата пройдёт, а `_on_payment_succeeded` упадёт на `User.get(tg_id=0)`). Дополнительно — guard `if user is None` в `_on_payment_succeeded` (деньги списаны → алерт админу, не тихий крэш).
- **M8 — локаль в кроне.** `_()` в APScheduler-таске даёт дефолтную локаль (именно из-за этого кнопка в форке закомментирована с `# BUG:`). Оборачивать формирование текста И клавиатуры в `with i18n.use_locale(user.language_code or DEFAULT_LANGUAGE):`.
- **M2 — дедуп-ключи привязать к циклу.** Ключ `notify:traf:{tg_id}:{pct}` с фиксированным TTL 30 дн. не сбрасывается при продлении → в новом периоде уведомление подавляется, а на тарифах 60/180/365 дн. — спамит. При успешном `extend_subscription` (после `reset_traffic`) и при ручном сбросе трафика админом делать `redis.delete(f"notify:traf:{tg}:80", ...:100, f"notify:exp:{tg}:3", ...:1)` (пороги константны, SCAN/KEYS не нужны).
- **B5 — фильтр Stars-рекуррента (ретро-правка из Этапа 5).** См. Этап 5: крон при `user.is_stars_auto_renew` пропускает пороги по сроку / не показывает кнопку «Продлить».
- **m6 — нагрузка крона.** Не звать `get_client_data` целиком (тянет `inbound.get_list()` на каждого юзера); для порогов достаточно `client.get_by_email` (total/up/down/expiry одним ответом). Redis-ключи проверять ДО похода в панель; фильтровать юзеров без активной подписки; в `add_job` — `coalesce=True`, `misfire_grace_time`.
- **m2 — семантика порогов.** `days_left <= d` (не `== d`, иначе пропуск рана теряет порог); порог `1.0` — отдельный текст «трафик исчерпан, доступ приостановлен» (панель уже отрезала), не «заканчивается»; при пересечении нескольких порогов за ран слать только максимальный (сортировка по убыванию + break). Пункт «на 100% вручную `enable=False`» убрать — панель блокирует depleted сама.

**DoD:** тариф с лимитом трафика; профиль показывает остаток; напоминания 3д/1д и 80%/100% **(рекуррент-юзерам — без ложных «истекает»)**; продление сбрасывает трафик и продлевает срок; **промокод не снимает лимит трафика; напоминания повторяются в каждом новом цикле подписки**.

Полные сниппеты: `telegram-bot-stage3-traffic.md`.

## 6. Этап 4 — Ручная карта + модерация (детально)

**Подход:** отдельная ветка (у ручной оплаты нет URL): показать реквизиты + «Я оплатил» → уведомить админов → Approve/Reject → общий `_on_payment_succeeded`.

**Файлы:** `navigation.py` (+`PAY_MANUAL`, колбэки), `config.py`/`.env` (+`PAYMENT_MANUAL_ENABLED`, реквизиты), `payment_gateways/manual_card.py` (new) + фабрика, `routers/subscription/manual_handler.py` (new) + include **до** универсального payment-хендлера, i18n.

**Ключевое:**
```python
# manual_card.py — create_payment создаёт PENDING и возвращает payment_id (не URL)
async def create_payment(self, data):
    payment_id = str(uuid.uuid4())
    async with self.session() as s:
        await Transaction.create(s, tg_id=data.user_id, subscription=data.pack(),
                                 payment_id=payment_id, status=TransactionStatus.PENDING)
    return payment_id

# модерация
@router.callback_query(ManualModerationCallback.filter(), IsAdmin())
async def manual_moderation(callback, callback_data, session, gateway_factory):
    txn = await Transaction.get_by_id(session, payment_id=callback_data.payment_id)
    if not txn or txn.status == TransactionStatus.COMPLETED:   # идемпотентность
        await callback.answer(_("payment:manual:already_processed")); return
    gw = gateway_factory.get_gateway(NavSubscription.PAY_MANUAL)
    await (gw.handle_payment_succeeded if callback_data.action=="approve" else gw.handle_payment_canceled)(callback_data.payment_id)
```
> Универсальный хендлер ловит `F.state.startswith(PAY)` — ручной роутер подключить раньше, либо исключить `PAY_MANUAL` из универсального.

**Обязательные правки по аудиту:**
- **B3 — атомарная модерация (CAS вместо read-then-act).** Гард `if txn.status == COMPLETED` дырявый: не закрывает Approve/Approve (гонка), Approve+Reject и Reject→Approve. Делать compare-and-set по статусу ДО вызова шлюза:
  ```python
  stmt = (update(Transaction)
          .where(Transaction.payment_id == pid, Transaction.status == TransactionStatus.PENDING)
          .values(status=target_status))
  res = await session.execute(stmt); await session.commit()
  if res.rowcount != 1:
      await callback.answer(_("payment:manual:already_processed")); return
  # только теперь — gw.handle_payment_succeeded / handle_payment_canceled
  ```
  Гард обязан быть `status == PENDING` (через WHERE), не `!= COMPLETED`. Одиночный UPDATE атомарен и на SQLite, и на Postgres (G9).
- **M4 — сниппеты §5 не запустятся как есть.** `session` инжектится как открытый `AsyncSession`, не фабрика → убрать `async with session() as s`, использовать `session` напрямую. Добавить в DI-сигнатуры недостающее: `config: Config` (для `config.shop.MANUAL_CARD_DETAILS` в `manual_payment`), `bot: Bot` (для уведомления юзера в ветке reject `manual_moderation`). Уведомление юзера об отказе — через `NotificationService.notify_by_id` (глотает `TelegramForbiddenError`) или try/except; `callback.answer()` — в `finally`.
- **M3 — регистрация роутера.** `manual_handler.router` добавить в `dispatcher.include_routers(...)` в `routers/__init__.py` **строго перед** `subscription.payment_handler.router`; надёжнее — исключить `PAY_MANUAL` из универсального фильтра: `SubscriptionData.filter(F.state.startswith(PAY) & (F.state != PAY_MANUAL))`.
- **M12 — «Я оплатил»: лимит заявок и спам.** Каждый заход в «карта» создаёт новую PENDING (uuid), каждое «Я оплатил» шлёт всем админам. Юзер может наплодить N неотличимых заявок → админы заапрувят две за один перевод (дедуп по `payment_id` тут не спасает, id разные). В `create_payment` переиспользовать активную PENDING manual-транзакцию юзера (макс. одна); троттлинг «Я оплатил» (redis-ключ); TTL-крон отмены брошенных PENDING — включить в объём Этапа 4, не «Later».
- **M-no-amount — подтверждение вслепую.** В заявке админу только `user_id`+`payment_id`. Обогатить из транзакции: `data = SubscriptionData.unpack(txn.subscription)` → `price`+`Currency.RUB`, `devices`, `duration`, `traffic` (`plan.get_plan(...).traffic_gb`, в `SubscriptionData` поля traffic нет), `@username`/имя. Иначе юзер выберет дорогой тариф, переведёт меньше — админ не поймает.
- **M9 — кросс-гейтвейный двойной биллинг.** Если у юзера активен Stars-рекуррент (`user.is_stars_auto_renew`), а он продлевает картой/криптой — не принимать молча: предупредить и предложить сначала отменить автопродление (`edit_user_star_subscription(is_canceled=True)`), либо как минимум показать админу в заявке флаг «активен Stars-рекуррент».
- **M5 — локаль.** Уведомление об отказе юзеру рендерить в его локали (`i18n.use_locale`), не админа — как в Этапе 2.

**DoD:** «карта» → реквизиты + «Я оплатил» → админ Approve/Reject **(с суммой и тарифом в заявке)** → активация/уведомление об отказе; повторное подтверждение безопасно **(Approve/Approve, Approve+Reject, Reject→Approve, двойной Reject — все закрыты CAS)**; **макс. одна активная заявка на юзера; при активном Stars-рекурренте ручное продление не создаёт двойной биллинг**.

Полные сниппеты: `telegram-bot-stage4-manual-card.md`.

## 7. Этап 5 — Авто-рекуррент Stars (детально)

**Ограничение Telegram:** период подписки Stars фиксированный — 30 дней (`subscription_period=2592000`). Рекуррент — только для 30-дн. тарифа; остальные Stars-платежи разовые.

**Файлы:** `telegram_stars.py` (+`subscription_period`), `payment_handler.py` (ветка `is_recurring`→extend, сохранить charge id, не рефандить рекуррент), `db/models/user.py` (+`stars_charge_id`, `is_stars_auto_renew`, миграция), `routers/profile/*` (статус + «Отменить автопродление»), i18n.

**Ключевое:**
```python
# telegram_stars.create_payment
if data.duration == 30 and not data.is_extend and not data.is_change:
    kwargs["subscription_period"] = 2592000
return await self.bot.create_invoice_link(**kwargs)

# payment_handler.successful_payment
sp = message.successful_payment
data = SubscriptionData.unpack(sp.invoice_payload)
if sp.is_recurring and not sp.is_first_recurring:
    data.is_extend = True                       # авто-продление
await User.update(session, tg_id=user.tg_id, stars_charge_id=sp.telegram_payment_charge_id,
                  is_stars_auto_renew=bool(sp.is_recurring))
# ... Transaction.create(COMPLETED) + gateway.handle_payment_succeeded(charge_id)

# отмена из профиля
await bot.edit_user_star_subscription(user_id=user.tg_id,
    telegram_payment_charge_id=user.stars_charge_id, is_canceled=True)
```
> `_on_payment_succeeded` роутит по `data.is_extend` → `extend_subscription` (срок + сброс трафика). Свериться с сигнатурами `create_invoice_link(subscription_period=...)` и `edit_user_star_subscription(...)` в aiogram 3.15+.

**Обязательные правки по аудиту:**
- **B4 — не перезаписывать `stars_charge_id` каждым платежом.** Для `editUserStarSubscription` нужен charge id ПЕРВОГО платежа (`is_first_recurring=True`). Разделить обновление в `successful_payment`:
  ```python
  sp = message.successful_payment
  if sp.is_first_recurring:                       # только первый платёж подписки
      await User.update(session, tg_id=user.tg_id, stars_charge_id=sp.telegram_payment_charge_id)
  if sp.is_recurring:                             # первый И рекурренты — подписка жива
      await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=True)
  if sp.subscription_expiration_date:             # B5: точный сигнал для крона
      await User.update(session, tg_id=user.tg_id, stars_expires_at=sp.subscription_expiration_date)
  # разовые Stars-платежи (is_extend/is_change/тариф≠30дн, sp.is_recurring отсутствует) НЕ трогают charge_id/is_stars_auto_renew
  ```
- **B2 — идемпотентность (Stars-специфика).** Транзакция Stars создаётся сразу COMPLETED → CAS в `_on_payment_succeeded` её сломает. Дедуп делать здесь: `txn = await Transaction.create(...); if txn is None: logger.warning(...); return` — ДО `User.update` и `handle_payment_succeeded`. CAS оставить только для Cryptomus/manual.
- **B1 — рефанд списания у не-approved.** Если юзер не approved (reject после покупки) — рекуррентное `successful_payment` всё равно придёт: записать `Transaction` (аудит), `bot.refund_star_payment(user_id, charge_id)` (для рекуррента заодно отменяет подписку), сбросить `is_stars_auto_renew`, уведомить.
- **B5 — ретро-правка крона Этапа 3.** Внести `tasks/subscription_expiry.py` в список файлов Этапа 5: пропускать пороги по сроку при `is_stars_auto_renew`; по `stars_expires_at + грейс` детектить лапс/отмену без вебхука и снимать флаг.
- **m5 — отмена без залипания флага.** `edit_user_star_subscription` требует активную подписку и валидный charge id; если подписка лапснула/зарефанжена — бросит `TelegramBadRequest`. Обернуть в try/except и в ЛЮБОМ случае делать `User.update(is_stars_auto_renew=False)` локально:
  ```python
  try:
      await bot.edit_user_star_subscription(user_id=user.tg_id, telegram_payment_charge_id=user.stars_charge_id, is_canceled=True)
  except TelegramBadRequest as e:
      logger.warning(f"cancel stars {user.tg_id}: {e}")
  await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=False)
  ```
- **m4 — кнопка «Возобновить».** Отмена ботом (`is_canceled=True`) ставит `bot_canceled` — юзер НЕ реактивирует подписку сам из настроек Telegram. Добавить кнопку «Возобновить автопродление» (`edit_user_star_subscription(..., is_canceled=False)`, работает пока текущий период активен) и предупредить в тексте отмены, что доступ сохранится до конца оплаченного периода.
- **Модель/миграция:** к плановым полям добавить `stars_expires_at` (для B5). DoD/тесты: покупка → автопродление → отмена из профиля (не только до первого рекуррента); разовое Stars-продление при активной подписке → статус автопродления в профиле не меняется; reject при активном рекурренте → отмена + обработка следующего списания.

**DoD:** 30-дн. Stars-покупка → автообновляемая подписка; рекуррент продлевает; отмена из профиля **(и «Возобновить» пока период активен; без залипания флага при лапснувшей подписке)**; тарифы ≠30 дн. разовые; сбой/отмена → напоминания; **charge id первого платежа не затирается рекуррентами; повторная доставка апдейта не даёт второго продления**.

Полные сниппеты: `telegram-bot-stage5-stars-recurring.md`.

---

## 8. Инфраструктура и деплой

Бот работает **за существующим (внешним) Traefik**: TLS терминируется на Traefik, сам бот слушает **plain HTTP на `BOT_PORT` (8080)** во внутренней docker-сети и порты наружу не публикует.

### 8.1 Traefik (внешний reverse-proxy) — G11
В форке `docker-compose.yml` **уже есть встроенный Traefik + Let's Encrypt** и бот уже ходит plain HTTP:8080 за ним. Раз используем свой Traefik:
- **Убрать** из compose сервис `traefik` и volume `letsencrypt_data`; переменная `LETSENCRYPT_EMAIL` в проекте бота больше не нужна (сертификаты — на вашем Traefik).
- Бот подключить к **внешней сети** вашего Traefik; на сервисе `bot` оставить Traefik-labels (Host-rule, entrypoint `websecure`, `certresolver` = имя вашего резолвера, порт сервиса 8080).
- `BOT_DOMAIN` остаётся (Host-rule + построение URL вебхуков).

Пример (фрагмент `docker-compose.yml`):

> ⚠️ **B8/M1 — фрагмент показывает ТОЛЬКО изменения относительно оригинального compose форка (networks/secrets/labels). `volumes`, `command` (миграции), `depends_on`, `restart` берутся из оригинала без изменений и приведены ниже явно — их пропуск ведёт к потере SQLite-базы (юзеры/транзакции/апрувы/список серверов) и redis-дедупа при пересоздании контейнера.** Хост панели 3x-ui хранится в таблице `server` внутри БД (`Server.host`), а не в env — поэтому `XUI_HOST` отсутствует в списках переменных.

```yaml
services:
  bot:
    build: .
    networks: [traefik-proxy, default]     # порты наружу НЕ публикуем
    env_file: [.env]                        # только НЕсекретные значения
    depends_on:                             # M1: bot не стартует раньше готового redis
      redis: { condition: service_healthy }
    restart: unless-stopped
    volumes:                                # B8: ОБЯЗАТЕЛЬНО — иначе БД живёт в слое контейнера
      - ./app/data:/app/data                # bot_database.sqlite3 лежит здесь
      - ./plans.json:/app/data/plans.json
      - ./app/locales:/app/locales
      - ./app/logs:/app/logs
    environment:
      BOT_TOKEN_FILE: /run/secrets/bot_token
      XUI_PASSWORD_FILE: /run/secrets/xui_password
      CRYPTOMUS_API_KEY_FILE: /run/secrets/cryptomus_api_key
      MANUAL_CARD_DETAILS_FILE: /run/secrets/manual_card_details
      WEBHOOK_SECRET_FILE: /run/secrets/webhook_secret   # B7
    secrets: [bot_token, xui_password, cryptomus_api_key, manual_card_details, webhook_secret]
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.bot.rule=Host(`${BOT_DOMAIN}`)"
      - "traefik.http.routers.bot.entrypoints=websecure"
      - "traefik.http.routers.bot.tls.certresolver=<ВАШ_РЕЗОЛВЕР>"
      - "traefik.http.services.bot.loadbalancer.server.port=8080"
      - "traefik.docker.network=traefik-proxy"
      # B7 (defense-in-depth): allowlist подсетей Telegram на роут /webhook
      # - "traefik.http.routers.bot-webhook.rule=Host(`${BOT_DOMAIN}`) && Path(`/webhook`)"
      # - "traefik.http.routers.bot-webhook.middlewares=tg-ipallowlist"
      # - "traefik.http.middlewares.tg-ipallowlist.ipwhitelist.sourcerange=149.154.160.0/20,91.108.4.0/22"
  redis:
    image: redis:7.4-alpine              # M1: пин версии вместо latest
    networks: [default]
    restart: unless-stopped
    volumes: [redis_data:/data]          # M1: FSM + дедуп-ключи напоминаний переживают рестарт
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 3s
      retries: 5

networks:
  traefik-proxy: { external: true }          # сеть вашего Traefik

volumes:
  redis_data:                                # M1

secrets:
  bot_token:           { file: ./secrets/bot_token.txt }
  xui_password:        { file: ./secrets/xui_password.txt }
  cryptomus_api_key:   { file: ./secrets/cryptomus_api_key.txt }
  manual_card_details: { file: ./secrets/manual_card_details.txt }
  webhook_secret:      { file: ./secrets/webhook_secret.txt }   # B7
```

### 8.2 Секреты через docker compose secrets — G10
Секреты монтируются в `/run/secrets/<name>` (read-only). В `.env` — только несекретные значения; секреты — в файлах. В `config.py` добавить чтение из файла по конвенции `<VAR>_FILE`:
```python
def env_or_file(env, name, cast=str, default=None):
    path = env.str(f"{name}_FILE", None)
    if path and os.path.isfile(path):
        with open(path, encoding="utf-8") as f:      # m10: with + encoding
            return cast(f.read().strip())
    val = env.str(name, default)
    return cast(val) if val is not None else None
# применять ТОЛЬКО к строковым секретам: BOT_TOKEN, XUI_PASSWORD, XUI_TOKEN, *_API_KEY,
#   *_MERCHANT_ID, MANUAL_CARD_DETAILS, WEBHOOK_SECRET (B7).
# m10: числовые/списковые переменные (DB_PORT и т.п.) оставить на типизированном чтении
#      environs (env.int/env.list(subcast=int)/env.bool + валидаторы) — не гонять через generic cast.
```
- **Секреты** (файлы `/run/secrets/*`): `bot_token`, `xui_password`, `xui_token`(опц.), `cryptomus_api_key`, `cryptomus_merchant_id`, `manual_card_details`, **`webhook_secret` (B7)**, (+ `crypto_bot_token` при G6, `db_password`/`redis_password` при необходимости).
- **Несекретное** (в `.env`): `BOT_DOMAIN`, `BOT_PORT`, `BOT_ADMINS`, `BOT_DEV_ID`, `BOT_SUPPORT_ID`, `SHOP_*` флаги, `XUI_SUBSCRIPTION_*`, `LOG_*`, `REDIS_HOST/PORT`.

### 8.3 3x-ui
SSL для панели/подписки — на стороне панели (отдельно от бота); inbound; subscription-сервис (порт/путь); выключить шифрование конфигурации.

### 8.4 Webhooks
Крипта (Cryptomus/CryptoBot) требует публичного HTTPS (даёт Traefik). **Stars и ручная карта вебхук не требуют** → ядро можно поднять и без внешних вебхуков.

**B7 — аутентификация Telegram-вебхука (обязательно, Этап 0).** Форк вызывает `bot.set_webhook()` без секрета и регистрирует `SimpleRequestHandler` без проверки → публичный `/webhook` принимает поддельные апдейты (подделка `successful_payment` → провижининг без оплаты; подделка `callback_query` с `from.id` админа → approve/рассылка/maintenance). Правки в `app/__main__.py`:
- `await bot.set_webhook(webhook_url, secret_token=config.bot.WEBHOOK_SECRET)`;
- `SimpleRequestHandler(dispatcher=dispatcher, bot=bot, secret_token=config.bot.WEBHOOK_SECRET)` — aiogram сам сверит `X-Telegram-Bot-Api-Secret-Token` (`secrets.compare_digest`) и вернёт 401 на подделки;
- секрет — docker secret `webhook_secret` (символы `A-Za-z0-9_-`, длина 1..256);
- defense-in-depth — IP-allowlist подсетей Telegram (см. закомментированные labels в 8.1);
- заодно исправить в `on_startup` `if await bot.get_webhook_info() != webhook_url` (объект `WebhookInfo` != строка → всегда True; сравнивать `.url` и учитывать смену `secret_token`).

**m8 — Cryptomus-вебхук за внешним Traefik.** `verify_webhook` форка сверяет client IP с единственным хардкодным `91.227.144.54`, а за Traefik `request.remote` = IP прокси. Если Traefik не пробрасывает реальный client IP (`forwardedHeaders.trustedIPs`), allowlist даст 403 на ВСЕ настоящие callback'и (крипта оплачена, подписка молча не активируется). Основной механизм доверия — md5-подпись; IP-allowlist сделать конфигурируемым (env) и вторичным. Приёмочный тест Этапа 1: реальный/тестовый крипто-callback за Traefik доходит до 200 и активирует подписку.

### 8.4a SQLite под нагрузкой (M11) — до перехода на Postgres (G9)
Движок форка создаётся без PRAGMA. Параллельные записи вебхука (Transaction/User) и трёх APScheduler-задач в том же процессе → `database is locked` на пути активации оплаты (оплачено, не выдано). Для MVP на SQLite включить WAL и `busy_timeout` через connect-listener:
```python
from sqlalchemy import event
engine = create_async_engine(url, pool_pre_ping=True)
@event.listens_for(engine.sync_engine, "connect")
def _sqlite_pragmas(dbapi_conn, _):          # применять только для sqlite-URL
    cur = dbapi_conn.cursor()
    cur.execute("PRAGMA journal_mode=WAL")
    cur.execute("PRAGMA busy_timeout=5000")
    cur.execute("PRAGMA synchronous=NORMAL")
    cur.close()
```
Плюс ретрай на `database is locked` вокруг финализации транзакции в `_on_payment_succeeded`. Бот — строго **single-instance** (webhook + in-process APScheduler не рассчитаны на несколько реплик: дублируются планировщик и провижининг).

### 8.5 README.md — обязательный артефакт — G12
При сборке бота README.md должен содержать:
- Обзор и требования (VPS, Docker/Compose, **внешний Traefik и имя его сети**).
- Подготовка Traefik: внешняя сеть, `certresolver`, entrypoints.
- Создание секретов: список файлов `./secrets/*.txt`, команды создания и права доступа.
- **Полная таблица переменных окружения:** имя · обязательность · значение по умолчанию · секрет (да/нет) · описание.
- **Полный список docker-секретов** с описанием.
- **Пошаговый запуск с примерами:** пример `.env`, пример `docker-compose.yml` (Traefik-labels + secrets), создание секретов, `docker compose build`, миграции (в `command`), `docker compose up -d`.
- Настройка: `BOT_ADMINS`/`BOT_DEV_ID`, домен, платёжки (вкл/выкл флагами), реквизиты ручной оплаты, 3x-ui (inbound/subscription).
- Обновление/миграции, бэкап БД, troubleshooting (частые ошибки, где смотреть логи).

README держать в актуальном состоянии по мере добавления фич (апрув, трафик, ручная карта, Stars-рекуррент).

**Правки по аудиту для README:**
- **B8 — персистентность:** где лежит SQLite (`/app/data/bot_database.sqlite3` внутри `./app/data`-volume), что бэкапить (БД + `plans.json` + `secrets/`), что при переходе на Postgres (G9) нужен именованный volume для pgdata.
- **m9 — миграции.** Форк гоняет `alembic upgrade head` в постоянном `command` на каждый старт без бэкапа. Прописать: перед `docker compose up`/апгрейдом делать копию `bot_database.sqlite3` (`cp bot_database.sqlite3 bot_database.$(date +%F).bak` при остановленном боте) — единственная точка отката для файловой SQLite; рекомендовать выносить миграции в разовый шаг (`docker compose run --rm bot ... alembic upgrade head`), а не на каждый рестарт; определиться между `create_all` в `initialize()` и alembic, чтобы схема не расходилась; зафиксировать single-instance (M11).
- **B6/B7:** поддерживаемая версия панели 3x-ui ↔ py3xui и процедура совместного апгрейда; создание секрета `webhook_secret`.

## 9. Риски

- Stars pre-checkout ≤10 c (в aiogram нативно; следить за нагрузкой). **Гейт не должен глотать `pre_checkout_query` — иначе платёж отменяется через 10 с (B1).**
- **Идемпотентность платежей — не «проверять статус», а compare-and-set по статусу транзакции (B2): в форке `_on_payment_succeeded` НЕ проверяет статус; Cryptomus переотправляет вебхуки; Stars `Transaction.create` при дубле возвращает `None` без исключения. Дедуп Stars — в `successful_payment`, CAS — для Cryptomus/manual. Обработать падение провижининга после CAS (флаг `provisioned_at`/алерт — иначе «оплачено, не выдано»).**
- **Матрица совместимости py3xui ↔ 3x-ui (B6): 0.3.x–0.6.0 — только легаси `panel/api/inbounds/*`, работают с панелью ≤v3.0; 0.7.0+ — только `panel/api/clients/*`, с v3.1+. Зафиксировать версию образа панели, запретить её автообновление без синхронного обновления бота; переход на 0.7.0 — не drop-in (изменилась семантика адресации по email).**
- SQLite при росте/нагрузке — держать наготове переход на Postgres (G9). **До миграции — WAL + busy_timeout + single-instance (M11, §8.4a).**
- Upstream архивный — фиксов нет; берём как свой код.
- Единицы трафика (`totalGB` в байтах) и сброс трафика при продлении — проверить на реальной панели. **Метод — `client.reset_stats(inbound_id, email)` (m1); промокоды не должны стирать лимит (M10).**
- Рекуррент Stars — обрабатывать неуспешные списания и отмену; период жёстко 30 дн. **`stars_charge_id` — только первого платежа (B4); напоминания фильтровать по `is_stars_auto_renew` (B5); ручное/крипто-продление при активном рекурренте → двойной биллинг (M9).**
- **Безопасность входящих: Telegram-вебхук без `secret_token` принимает подделки (B7); Cryptomus — доверять md5-подписи, не одному хардкод-IP (m8).**
- **Конкурентная модерация ручной оплаты — гонка без CAS: Approve/Approve, Reject→Approve выдают доступ по отклонённому/двойному платежу (B3, M12).**

## 10. Definition of Done (MVP)

Сквозной путь: `/start` → апрув админом → триал → выбор трёхмерного тарифа → оплата (Stars/крипта/ручная карта) → создание клиента в 3x-ui с лимитами → выдача ключа/QR/инструкции → «моя подписка» с остатком трафика → напоминания по сроку и трафику → продление (авто-Stars или ручное) → базовая админка (апрув, управление, модерация платежей, рассылка, бэкап, maintenance).

**Инфраструктурная готовность:** бот поднимается за внешним Traefik (без встроенного TLS), все секреты — через docker compose secrets (`/run/secrets`), и есть актуальный README с пошаговым запуском и полным описанием всех переменных и секретов. **+ БД и Redis переживают пересоздание контейнера (volumes, B8/M1); Telegram-вебхук отвергает апдейты без `secret_token` (B7); SQLite в режиме WAL, бот single-instance (M11).**

**Готовность платежей (инварианты §2A):** повторная доставка вебхука/апдейта не даёт второго провижининга (B2); конкурентная модерация ручной оплаты безопасна во всех комбинациях Approve/Reject (B3); отмена Stars-рекуррента из профиля работает и после нескольких автопродлений (B4); гейт не теряет платёжные апдейты, reject отменяет рекуррент (B1); напоминания не провоцируют двойную оплату у рекуррент-юзеров (B5).

## 11. Карта «куда править»

- **Апрув:** `db/models/user.py`, `middlewares/approval.py`(+`__init__`), `routers/main_menu/handler.py`, `routers/admin_tools/approval_handler.py`, **`routers/__init__.py` (M3 — реальная регистрация роутеров здесь)**.
- **Тарифы/трафик:** `models/plan.py`, `data/plans.json`, `models/subscription_data.py`, `services/vpn.py` **(M10 — `total_gb: int|None`; m1 — `reset_stats`)**, `services/server_pool.py` **(`get_inbound_id` для m1)**, `payment_gateways/_gateway.py` **(B2 — CAS/guard `user is None`)**, `routers/profile/*`.
- **Крон/напоминания:** `tasks/subscription_expiry.py` **(M2, M7, M8, B5, m2, m6 — правится и на Этапе 3, и ретро на Этапе 5)**.
- **Платёжные шлюзы:** `payment_gateways/*` (+`gateway_factory.py`, `config.py`, `utils/navigation.py`, `utils/constants.py`) **+ `payment_gateways/cryptomus.py` (m8 — IP-allowlist)**.
- **Stars-рекуррент:** `payment_gateways/telegram_stars.py`, `routers/subscription/payment_handler.py` **(B1, B2, B4)**, `routers/profile/*` **(m4, m5)**, `db/models/user.py` **(+`stars_expires_at`, B5)**.
- **Инфраструктура/безопасность:** `app/__main__.py` **(B7 — `secret_token`; исправить сравнение `get_webhook_info`)**, `app/config.py` **(m10 — `env_or_file`; `WEBHOOK_SECRET`)**, `app/db/database.py` **(M11 — WAL/busy_timeout)**, `docker-compose.yml` **(B8/M1 — volumes, redis-пин/healthcheck, `webhook_secret`)**.

## Приложение — документы-спутники

- `telegram-bot-scenarios.md` — карта сценариев и стейт-машины.
- `telegram-bot-flows.html` — диаграммы потоков.
- `telegram-bot-stage2-approval.md` … `stage5-stars-recurring.md` — полные код-сниппеты по этапам.
- `telegram-bot-3xui-research.md` — исходный ресёрч (панель/боты/платежи).
