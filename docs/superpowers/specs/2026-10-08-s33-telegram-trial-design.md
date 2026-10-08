# С33.Р4 — обычный Telegram-триал

Владелец: #31 / subscriptions. Контракт `2026-10-08-s33-telegram-trial-v1`.
База: v2 / 71bc325bf1153f1a1d40b2ff9d8f6749038db29b.
Поручение: автономная подготовка документов, Native-реализация и последовательное
слияние PR в v2. Локальные заглушки разрешены; production и живой Happ исключены.

## Поведение

Новый клиент принимает условия в подписанном Mini App, открывает кабинет и
нажимает «Активировать триал». Backend в одной транзакции создаёт одобренную
заявку с явно записанным автоматическим решением, резервирует единственный
trial_grant и существующее River-задание. Оператор для этого пути не нужен.
После выдачи кабинет показывает существующее подключение. Сам VPN-ключ не
попадает в Telegram, аудит или ответ активации.

Выбор политики зависит от неизменяемого SourceKind. Первоначальный web-аккаунт
сохраняет заявку оператору даже после привязки Telegram. Первоначальный
Telegram-аккаунт после добавления email/пароля сохраняет автоматическую политику.
Смена текущего канала входа не меняет политику и не создаёт второй аккаунт.

Обычные параметры уже принадлежат subscriptions: TRIAL_ENABLED, TRIAL_PERIOD,
TRIAL_TRAFFIC_GB, BONUS_DEVICES_COUNT, PANEL_ID. Параметры захватываются в
существующую trial_operation перед сетевым вызовом. Нового worker нет.

## Eligibility и повторы

- SourceKind=telegram, TelegramID присутствует, исходная Telegram-identity
  доступна, аккаунт не restricted/VPN-banned; действующие правила consent/auth.
- LegacyUserID отсутствует: нынешний частичный импорт не содержит факта старого
  использования триала. Такие аккаунты остаются на существующем ручном пути;
  С46 обязан перенести trial-used до расширения автоматической eligibility.
- Нет trial_grant, прошлого/назначенного подключения, незавершённой access
  operation или текущей заявки. При отказе остаётся пересмотр через поддержку.
- TRIAL_ENABLED=true, валидны текущие лимиты и panel. Сервер проверяет политику
  заново под account lock; поле capability в браузере не предоставляет права.
- Idempotency-Key обязателен. Повтор того же ключа возвращает ту же заявку;
  новый ключ после резервирования даёт TRIAL_ALREADY_USED. Account lock и
  уникальный grant не допускают двух выдач при конкуренции.
- Ошибка вставки River/audit/outbox откатывает всю транзакцию. Неопределённый
  результат 3X-UI оставляет резерв и needs_review; повторная активация не выдаёт
  новый ключ. Reconcile использует уже сохранённый operation/target.

## API и данные

Источник HTTP-контракта: docs/api/openapi.yaml; существующие генераторы
backend/Makefile и web/package.json. Добавляется POST /api/v1/trials/activate,
operationId activateTelegramTrial, JSON {}, существующие cookie/Mini App bearer,
Origin/CSRF/Idempotency-Key, 201 при создании и 200 при replay; TrialRequest
остаётся прежним. Ошибки 400/401/403/404/409/429/503 и no-store как прежде.

Capabilities получает необязательное trial_mode=activate|request. Для ручного
пути поле опускается, сохраняя прежний web JSON; отсутствие означает request.
trial_available учитывает наличие оператора только для ручного пути.
POST /trial-requests остаётся запросом оператору, не становится активацией.

subscriptions предоставляет ActivateTelegramTrial(ctx,accountID,key) и
TrialMode(snapshot). HTTP адаптер сериализует публичные DTO; Telegram продолжает
открывать общий Mini App, без отдельного /trial обработчика и чужого SQL.

Миграция 00027 добавляет decision_source=operator|telegram_auto. Существующие
pending/rejected/manual-approved факты сохраняют настоящего оператора. Только
telegram_auto/approved допускает отсутствие operator IDs, обязательны decided_at
и operation_id. Нулевой или фиктивный оператор не используется. Аудит действия
trial_activated_telegram с account/request/operation, без имитации оператора.
Down отказывает при существующих автоматических решениях, не удаляя факты.

## Интерфейс

Один Cabinet для браузера/Mini App. Автоматический путь показывает объяснение
и кнопку активации без комментария; ручной путь сохраняет форму. Попытка хранит
ключ и выбранный mode до результата, включая потерянный ответ. Polling,
provisioning/needs_review, ошибки, no-store и явное раскрытие ключа сохраняются.
ru/en, нативная клавиатура, доступные имена, role=status/alert и disabled busy.

## Границы и совместимость

Реферальная льгота принадлежит #52/С33.Р7 вместе с #50–#51. До неё обычные
параметры одинаковы: signed start_param сохраняется, но не считается доказанной
реферальной связью и не выдаёт бонус. Это соответствует выключенному по
умолчанию legacy REFERRED_TRIAL_ENABLED; включённая legacy льгота не объявляется
перенесённой до Р7/С45–С46. Промокоды остаются #48–#49.

С31 reservations защищают от unlink/recreate; новую identity-таблицу не вводим.
UUID, subscription identity, старые operator callback/outbox hashes, manual
approve/reject/reconsider, email-политика, billing guards сохраняются.
С34–С36 Stars/payment, уведомления о сроках и пул серверов имеют своих владельцев.

## Приёмка и rollback

Проверить signed Mini App → capability → активация → River → 3X-UI 3.7.0 →
подключение; отсутствие operator approval, одинаковый request/op при replay,
конкурентность и полный rollback. Проверить disabled/used/restricted/legacy,
web+Telegram link, TG+email, неверные auth/CSRF/body/keys, отсутствие утечки VPN.
Проверить known/uncertain panel failures и restart/reconcile без второго grant.
Отдельно проверить ограничения миграции и старый ручной сценарий, ru/en в
Playwright. Полные Go/race, web, сохранённые Python и генерация перед delivery.

Локальная приёмка использует owned PostgreSQL/Redis/SMTP/TG fixtures и TLS
Docker 3X-UI 3.7.0; реальных денег и публичного webhook нет. Done означает
локальную приёмку, review и merge в v2 с проверенными preview image/release.
Внешний Telegram/production и финальный импорт остаются своим этапам.
Rollback приложения выключает TRIAL_ENABLED или возвращает предыдущий образ,
сохраняя schema/grants; Down блокируется после автоматической выдачи.
