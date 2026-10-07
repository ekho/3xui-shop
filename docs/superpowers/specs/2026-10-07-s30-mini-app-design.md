# С30 — общий кабинет в Telegram Mini App

Владелец: #28, модуль `telegram`. Контракт: `2026-10-07-s30-mini-app-v1`.
База: `58a620d70473dd9a424b55858c730913421ce7f4` (`v2`).
Архитектура: `2026-10-05-modular-monolith-v1`, #55; один Go-процесс и один frontend build.

## Результат

Клиент открывает `/mini-app` через main/menu/inline-button Mini App Telegram,
подтверждает условия при первом входе и получает доступ к существующим экранам
подписки, подключения устройства, каталога, заказов, истории и поддержки.
Аккаунт находится по подписанному Telegram ID, а не по имени или email.
Повторный вход сохраняет ID, VPN ID, ключи, источник аккаунта и историю.
Браузерный кабинет продолжает работать через email/password независимо от Telegram.

Спецификация и план исполняются автономно в рамках принятого мандата. Локальная
приёмка использует заглушки внешних ресурсов. Production и живой Happ исключены.

## Вход и границы доверия

`POST /api/v1/telegram/mini-app/session` принимает raw `init_data` (не более 12 KiB)
и необязательные версии согласия. Модуль Telegram проверяет Ed25519-подпись,
фиксированный bot ID из конфигурации встроенного бота, `auth_date` не старше
5 минут и не более чем на 60 секунд в будущем. Production использует официальный
ключ Telegram; смена ключа через переменную окружения не поддерживается.
Конструктор допускает явную зависимость ключа для локальных подписанных fixtures.
Дубликаты полей, плохое URL-кодирование/UTF-8, отсутствующий user, неверный ID,
подпись и время отклоняются. `initDataUnsafe`, произвольные ID и browser cookies
не подтверждают Telegram-вход. Redis ограничивает попытки до проверки подписи,
используя существующий лимитер accounts (30/IP за 15 минут); сбой лимитера закрывает вход.

Accounts владеет поиском/созданием аккаунта, согласием, транзакцией и сессией.
Один advisory lock по Telegram ID исключает два аккаунта при повторе/гонке.
Новый Telegram-only аккаунт не получает пароль, фиктивный email, операторскую роль
или подписку. Новый/существующий Telegram-only аккаунт без согласия требует
обоих текущих версий документов; без согласия новый аккаунт вообще не создаётся.
Версии и время фиксируются транзакционно и в audit. Первый подписанный
`start_param`, если есть, сохраняется неизменяемо для С32/С46; здесь он не
активирует промокод, реферала или выдачу доступа. При последующих входах payload
не заменяется. Подписанное имя служит только подписью в интерфейсе.

Сессия — случайный opaque bearer с префиксом `mini_`, только в памяти frontend;
БД хранит только hash, источник `telegram` и Telegram ID на момент выдачи.
Сроки совпадают с принятым клиентским контрактом: 7 дней бездействия, 30 дней
абсолютно. Проверка актуальной привязки отзывает доступ при смене Telegram ID.
Префикс исключает использование Mini App bearer как старой web cookie даже
после будущей привязки email. Web-сессии и старые DTO остаются совместимыми.

Mini App использует `Authorization: Bearer …`, `credentials: omit`, тот же
Origin и CSRF для записей. Неверный bearer не переключает запрос на cookie.
Разрешён явный список клиентских операций; operator/credentials/web login и
создание внешнего checkout через Mini App запрещены, включая будущий связанный
web-аккаунт оператора. `GET /api/v1/telegram/mini-app/account` возвращает отдельный
DTO с nullable email, display name, Telegram ID, locale, CSRF и возможностями.
`POST /api/v1/telegram/mini-app/logout` отзывает только эту сессию; доступен и
ограниченному аккаунту. Сессия/context для безопасного выхода не раскрывают данные.

## Общие экраны

Используются существующие компоненты и публичные операции владельцев доменов.
Mini App navigation сохраняет память приложения и показывает Telegram BackButton.
Ключ/QR, поддержка и история подчиняются тем же ownership/restriction правилам.
Вложения загружаются авторизованным fetch, без bearer в URL; временные blob URL
отзываются. При выходе/истечении сессии экраны и запросы уничтожаются, автоматического
повторного входа нет. Подписанные launch data и SDK cache очищаются из URL/storage.

Внешний checkout не показывается внутри Mini App. Каталог и детали заказов доступны
для чтения; внешний кабинет открывается отдельным пользовательским действием по
HTTPS URL текущего origin без токенов/ключей/идентификаторов. Этот переход не обещает
вход в тот же аккаунт: привязка и полный сценарий других оплат принадлежат С31/С36.
Ручная заявка на триал сохраняет существующие eligibility/апрув; автоматический
Telegram-триал принадлежит С33 и в С30 не появляется.

## SDK, HTML и конфигурация

Один Vite build имеет web и Mini App HTML entries. Mini entry загружает официальный
SDK асинхронно только в Mini App, с ограниченным ожиданием и retry/error/empty-launch
состояниями. Падение CDN не мешает обычному web. Отсутствующая интеграция возвращает
503 без остановки web и фоновых заданий. SDK ready/expand, theme, stable viewport,
safe area и BackButton применяются с проверкой доступности методов.
RU/EN, клавиатура и screen reader остаются обязательными.

Разрешение встраивания и загрузки SDK относится только к `/mini-app` и его HTML
подмаршрутам: `frame-ancestors https://web.telegram.org`, SDK script origin
`https://telegram.org`. Остальной web/admin сохраняет запрет framing; wildcard,
`unsafe-inline`, отдельная сборка Mini App и секреты в runtime config не нужны.
Настройка menu/main Mini App URL в Telegram — внешняя конфигурация С45, а не
автоматическая запись в BotFather или проверка реального бота в этой приёмке.

## Приёмка и совместимость

1. Реальная Ed25519-проверка локально подписанных данных: tamper, другой bot/key,
   stale/future, дубликаты, плохое кодирование, большие ID и replay в пределах окна.
2. HTTP → accounts → Postgres/Redis: consent, один аккаунт при повторе/гонке,
   сохранение старой Telegram identity/ключей/заказов, hash-only source-bound session,
   TTL, ограничения, revocation/logout, CSRF/Origin и отсутствие cookie fallback.
3. Web cookie не получает Mini App права и Mini bearer не получает web/admin/
   credentials/checkout права; ошибки не возвращают initData, bearer или VPN-ключ.
4. Browser fixtures: SDK success/timeout/empty/error, RU/EN, consent, common screens,
   navigation/theme/back/safe area, attachments, logout/expiry, независимый web.
5. Реальная сборка и контейнерная маршрутизация/CSP: Mini HTML не очищает Telegram
   launch до SDK; web email bootstrap сохраняется, SDK не запрашивается вне Mini App.
6. Миграция вперёд сохраняет старые IDs/hashes/данные; unsafe downgrade с новыми
   account consent/attribution данными блокируется. Полная Go race/connected и web
   регрессия, Python legacy checks, генерация, vet/typecheck/build, свежий Native review.

Слияние в `v2`, успешные CI и dev prerelease/images закрывают локальную задачу;
реальные клиенты Telegram, production SMTP/конфигурация и перенос остаются С45–С47.

Источники: [Telegram Mini Apps](https://core.telegram.org/bots/webapps),
[официальный SDK](https://telegram.org/js/telegram-web-app.js), проверены 2026-10-07.
Существующий проектный контракт Ed25519/bot ID/5 min/60 s и 7/30-day sessions
сохраняется из `2026-10-01-cabinet-backend-miniapp-design.md`.
