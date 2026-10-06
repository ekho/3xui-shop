# Первая покупка

## YooMoney

Оплата включается при запуске backend. Сборка кабинета не требует кошелька
или секрета. Без настроек метод выключен и каталог продолжает показывать цены.

Для Compose добавьте `-f deploy/purchase/compose.yoomoney.yml` к файлам вашего
стенда. В закрытом env-файле задайте `SHOP_PAYMENT_YOOMONEY_ENABLED=true`,
`YOOMONEY_WALLET_ID` и абсолютный путь `YOOMONEY_NOTIFICATION_SECRET_FILE`.
Сам секрет храните в файле с доступом только пользователю сервиса. Overlay
монтирует его в backend, migrate и reconcile; пароль/секрет не передаются
в frontend. Выключение новых покупок не удаляет секрет для завершения старых.

Публичный HTTPS proxy должен отправлять точный `POST /webhooks/yoomoney`
в backend. Cookie, Origin и CSRF для уведомления не требуются; HMAC обязателен.
Query и повтор поля отклоняются. Не открывайте `/internal/*` в публичной сети.
Кабинет использует POST-форму `https://yoomoney.ru/quickpay/confirm`; разрешите
этот домен в CSP `form-action`. Пример — `deploy/acceptance/Caddyfile`.

У кошелька один URL уведомлений. Перед переключением с бота проверьте старые
pending платежи и согласуйте их завершение или маршрутизацию старых меток.
С10 не переключает production URL, не выполняет перенос старых платежей и
не запускает два обработчика одного денежного факта.

Локальная проверка использует отдельный случайный секрет, синтетический
получатель и подписанный callback на localhost. Браузерная отправка во внешнюю
форму перехватывается. Это проверяет приложение и 3X-UI 3.7.0, но не доказывает
настоящий денежный перевод YooMoney.

На уже созданном собственном Docker-стенде задайте `LOCAL_STATE_DIR` его закрытым
каталогом, выполните `python3 deploy/purchase/local.py prepare`, затем соберите
и запустите backend/gateway с файлами своего стенда и платёжным overlay.
Режим native сохраняет `compose.native.yml` при восстановлении и перезапуске.
Перед миграцией закройте
ingress, остановите writers и сделайте backup, как в основной инструкции стенда.
`python3 deploy/purchase/local.py check` регистрирует два синтетических аккаунта,
проверяет новый доступ и переход с триала, неверное/тестовое уведомление и повтор.
`restore` сохраняет новый paid-заказ при временно остановленной локальной панели,
останавливает backend, восстанавливает dump в отдельную базу, сверяет деньги,
операции и очистку авторизации; исполнители восстановленной базы не запускаются.
Исходный backend возобновляет выдачу после возвращения панели. Все credentials,
dump и диагностические артефакты остаются в закрытом `.superpowers/acceptance/purchase`.


## Ручной перевод

Добавьте `-f deploy/purchase/compose.manual.yml` и задайте при деплое
`SHOP_PAYMENT_MANUAL_ENABLED=true`, `MANUAL_CARD_DETAILS_FILE` — абсолютный
путь к защищённому UTF-8 файлу с реквизитами (1–2000 символов, без NUL).
По умолчанию метод выключен. Файл монтируется в backend/migrate/reconcile,
кабинет получает только snapshot своего заказа. Ввод inline запрещён.
При отключении сохраните файл: новые заказы прекращаются, прежние переводы
можно заявить, а оператор — подтвердить или отклонить.

Клиент переводит точную сумму и нажимает «Я оплатил». Заявление не подтверждает
деньги; после него нельзя отменить заказ или создать второй. Оператор открывает
«Заявки на ручную оплату» в React-admin, проверяет реальное поступление в банке,
вводит полученную сумму и причину, затем подтверждает или отказывает. Решение
после исходного окна оплаты допустимо для своевременно заявленного перевода.
Причина и состояние сохраняются в кабинете; внешние уведомления — С27/С28/С32.

На собственном localhost-стенде `prepare-manual` создаёт синтетические реквизиты.
Перезапустите backend/gateway через те же Compose файлы плюс manual overlay.
`check-manual` проверяет HTTP-заявление/права/решения и реальную выдачу в3X-UI3.7.0
новому клиенту и клиенту с триалом. `restore-manual` останавливает только панель
этого Docker-проекта, сохраняет paid с оператором/заявлением/receipt в backup,
восстанавливает копию без writers, проверяет неизменность и перезапускает исходный
backend. Синтетическое подтверждение не доказывает банковский перевод.

## YooKassa

Добавьте `-f deploy/purchase/compose.yookassa.yml` и задайте при деплое
`SHOP_PAYMENT_YOOKASSA_ENABLED=true`, `YOOKASSA_SHOP_ID`, `SHOP_EMAIL` и абсолютный
путь `YOOKASSA_TOKEN_FILE`. Без настройки метод выключен. Token хранится только
в защищённом файле; inline `YOOKASSA_TOKEN` отклоняется. `YOOKASSA_TEST_MODE=false`
по умолчанию, для тестового магазина задайте true. Режим сверяется с ответом API.
При отключении новых продаж сохраните credentials для старых платежей.

HTTPS proxy передаёт точный `POST /webhooks/yookassa` в backend и сохраняет
проверяемый адрес отправителя. Используйте существующую настройку доверенных
proxy; произвольный X-Forwarded-For не должен определять этот адрес. Backend
проверяет официальный IP и заново запрашивает payment через авторизованный
HTTPS API. В test mode допускается собственный private/loopback отправитель,
но авторизованная проверка API обязательна. Не открывайте `/internal/*`.

Покупка сначала сохраняет заказ и задание, затем worker готовит ссылку оплаты.
Возврат в кабинет читает статус. Самостоятельное завершение не зависит от
уведомления: worker проверяет известный payment ID. Сомнительные/поздние платежи
сохраняются для разбора без новой выдачи. Неизвестный результат создания после
24 часов не повторяет POST. Смена shop/test требует завершить или разобрать
старые заказы; они не отправляются в другой магазин.

Сохранены текущие receipt-параметры: одна позиция, quantity=1, vat_code=1,
SHOP_EMAIL. Для настоящего магазина отдельно проверьте фискальные настройки
и публичную доставку уведомлений. Локальная заглушка подтверждает путь приложения,
но не подтверждает готовность настоящего магазина.

Для собственной локальной приёмки создайте новый закрытый каталог и задайте его
абсолютный путь в `LOCAL_STATE_DIR`. В нём нужен `runtime.json` с отдельной
идентичностью стенда:

```json
{"project":"cabinet-c12","postgres_user":"cabinet_c12","base_database":"cabinet_c12","fixture_prefixes":{"purchase":"c12-purchase-"}}
```

`python3 deploy/purchase/yookassa-local.py prepare` создаёт синтетический token,
сертификат и Compose overlay. `up` собирает текущие backend/web и запускает
3X-UI3.7.0, TLS Mailpit и stdlib API stub. Требуются свободные localhost-порты
58443, 59444–59447, 58449 и подсеть Docker10.253.12.0/28. Файл сертификата
используется только клиентами стенда; системное доверие менять не нужно.

`check` проверяет новый доступ и переход с триала, одинаковые bytes/key после
ошибки500 и перезапуска, авторизованный GET вместо доверия телу callback и один
receipt/job/access. `restore` использует существующую процедуру остановки writers,
backup, восстановления без writers и очистки авторизации; сравнивает также
provider checkout/proof, затем возобновляет исходный backend. `down` удаляет
только этот Docker-проект и его volume. Private reports/dump/credentials остаются
в `LOCAL_STATE_DIR`; в лог выводится только результат проверок.

`python3 deploy/purchase/yookassa-stub.py --self-check` проверяет саму заглушку
без Docker и внешней сети. Заглушка не запускает legacy Telegram-бота.

## Cryptomus

Добавьте `-f deploy/purchase/compose.cryptomus.yml`. При запуске задайте
`SHOP_PAYMENT_CRYPTOMUS_ENABLED=true`, `CRYPTOMUS_MERCHANT_ID` (UUID магазина)
и абсолютный путь `CRYPTOMUS_API_KEY_FILE`. По умолчанию метод выключен;
inline `CRYPTOMUS_API_KEY` запрещён. Сохраните парные credentials после
выключения новых продаж для сверки старых invoice. Смена merchant требует
сначала завершить или разобрать его заказы.

Публичный proxy передаёт точный `POST /webhooks/cryptomus` и достоверный адрес
отправителя. Backend принимает только официальный IP91.227.144.54; произвольный
X-Forwarded-For не считается адресом отправителя. Локального обхода проверки IP
нет. Проверяется подпись, затем авторизованный `/v1/payment/info`; webhook и
возврат браузера сами не подтверждают деньги. Worker сверяет invoice и без
уведомления. Не открывайте `/internal/*` и не настраивайте другой API hostname.

Cryptomus выбирает цену в USD. Invoice principal хранится в USD, суммы фактической
криптооплаты — отдельными фактами в payer currency. USD-net неизвестен/NULL.
`paid_over` даёт обычный срок доступа без дополнительного кредита. Недостаточная,
поздняя, AML/refund или противоречивая оплата требует разбора. Повтор создания
сохраняет order_id и bytes; после expiry неизвестного результата новый POST
не отправляется. Реальные реквизиты, публичная доставка и кошелёк проверяются
отдельно до переноса; локальная приёмка их не подтверждает.

Для собственного стенда создайте закрытый `LOCAL_STATE_DIR` с `runtime.json`:

```json
{"project":"cabinet-c14","postgres_user":"cabinet_c14","base_database":"cabinet_c14","fixture_prefixes":{"purchase":"c14-purchase-"}}
```

`python3 deploy/purchase/cryptomus-local.py prepare` готовит FILE secrets и TLS/DNS
только для этого Docker-проекта; `up` собирает текущий Go/web и запускает
3X-UI3.7.0 и TLS Mailpit. Нужны свободные порты58443,59444–59447,58450 и
подсеть10.253.14.0/28. Системное доверие сертификатам менять не требуется.
`check` проверяет new/paid, trial/paid_over, неопределённый500 и перезапуск,
одинаковые bytes/invoice, один receipt/job/access. Повтор информации проверяется
повторным запуском существующего River job в собственном fixture; vendor-IP
webhook через Docker не имитируется. `restore` сохраняет paid-pending с checkout/
proof, останавливает writers, сверяет восстановленную копию без writers и
возобновляет только исходный backend. `stop` оставляет volume и private reports.

`python3 deploy/purchase/cryptomus-stub.py --self-check` проверяет signed API,
unique order_id,500,info и paid/paid_over без Docker и внешней сети. Legacy бот
и живое VPN-соединение не используются.

Результаты и границы проверки: [локальная приёмка С14](../../docs/evidence/s14-acceptance.md).

## Heleket

Добавьте `-f deploy/purchase/compose.heleket.yml`.
Настройки задаются при деплое: `SHOP_PAYMENT_HELEKET_ENABLED=false` по умолчанию,
`HELEKET_MERCHANT_ID` — canonical UUID, `HELEKET_API_KEY_FILE` — абсолютный путь к
закрытому файлу. Inline `HELEKET_API_KEY` запрещён; парные retained credentials
проверяются и после отключения новых продаж. Секретов в frontend/image нет.

Публичный proxy передаёт точный `POST /webhooks/heleket` в backend. Разрешён
только effective IP31.133.220.8 и собственная подпись; forwarded header не
заменяет источник. Fixed API api.heleket.com; HTTPS checkout допускает только
new-pay.heleket.com или pay.heleket.com. Authenticated info подтверждает деньги;
return/webhook сами их не подтверждают. Не открывайте `/internal/*` публично.

Цена invoice в USD, crypto payer/payment/merchant facts отдельно, USD-net
неизвестен/NULL. Paid_over не добавляет доступ сверх тарифа. Underpayment,
late/canceled/refund/AML или противоречивые facts требуют review. Frozen
order/merchant/bytes и invoice сохраняются; неизвестный результат после expiry
проверяется только через info, без refresh. Cryptomus settings/host/IP/history
остаются отдельными. Реальные merchant/payment/public delivery не проверены.

Для собственного стенда создайте закрытый `LOCAL_STATE_DIR` с `runtime.json`:

```json
{"project":"cabinet-c15","postgres_user":"cabinet_c15","base_database":"cabinet_c15","fixture_prefixes":{"purchase":"c15-purchase-"}}
```

Запускайте `python3 deploy/purchase/heleket-local.py prepare`, затем `up`,
`check`, `restore` и `stop` с `LOCAL_PROFILE=native` и абсолютным
`LOCAL_STATE_DIR`. Этот путь использует общий проверенный драйвер двух
провайдеров, собственные Heleket FILE secrets/TLS/DNS/таблицы/River kind и
3X-UI3.7.0. Нужны свободные порты58443,59444–59447,58450 и подсеть10.253.15.0/28;
Cryptomus стенд перед этим остановите. Доверие сертификатам Mac и живое VPN
не меняются. `check` проверяет новые аккаунты, переход с триала,500/restart,
frozen bytes и replay; `restore` сохраняет финансовые факты в резервной копии
без запуска её writers и разрешает выдачу только исходному backend.

`python3 deploy/purchase/cryptomus-stub.py --self-check` проверяет оба
конкретных протокола локально. Документированная доставка webhook с vendor IP,
реальные checkout и деньги требуют отдельного стенда с доступом провайдера.

Результаты и границы: [локальная приёмка С15](../../docs/evidence/s15-acceptance.md).
