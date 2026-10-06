# С13 — локальная приёмка YooMoney

Владелец #18. Контракт `2026-10-06-c13-local-stubs-v2`:
https://github.com/ekho/3xui-shop/issues/18#issuecomment-6014628265.
Документы и Native выполняются автономно по принятому поручению.

## Цель и границы

Подтвердить существующий путь YooMoney: заказ с серверной ценой, форма,
подписанное уведомление, сохранение денег и одна выдача в 3X-UI3.7.0.
Используются собственные тестовые данные и заглушки, настоящий кошелёк,
перевод, доставка провайдером, production и Happ не затрагиваются.
Прежний план ожидания HTTPS/реального перевода заменён решением владельца.

payments остаётся единственным владельцем заказа, HMAC, receipt/funding и
purchase worker; HTTP/web используют его публичные операции. Адаптер уже
доставлен в С10/М05. Новый SDK, платёжный framework или обработчик не нужен.
Изменения product/API/schema/dependencies не предполагаются. Обнаруженный
дефект исправляется в владельце по RED→GREEN, в том же ограниченном сценарии.

## Сохранённый контракт

С10 остаётся источником quote, прав, 30min, идемпотентности и состояний;
С07/С08 — операции доступа. [С10](2026-10-03-s10-first-purchase-design.md).
Форма POST quickpay/confirm использует button, PC/AC, точную цену RUB и UUID
заказа; successURL только читает состояние. Настройки включения и FILE-секрет
задаются при деплое, метод default false; выключение новых заказов не
отменяет обработку существующих уведомлений при сохранённом секрете.

Уведомление проходит HTTP UTF-8/form/16KiB/query/duplicate boundary, затем
HMAC-SHA256 sign от всех полей кроме sign. SHA1-only и неподписанный
тестовый запрос не принимаются. Подписанный test_notification не создаёт
денег. amount — фактически зачисленный net, withdraw_amount — gross;
цена сравнивается с gross, комиссия не зашивается в код. Несовпадение суммы,
валюты, типа, защищённый/непринятый факт сохраняется для разбора без выдачи.
Повтор ID не создаёт receipt/job/access, конфликт не переписывает первый факт.
Поздняя оплата и отмена не теряют подписанный факт, но требуют разбора;
поздняя доставка совершённого в срок перевода использует signed timestamp.

Подписанная неизвестная метка получает200 без привязки/выдачи. Это не импорт
legacy платежей. Настоящие legacy pending, wallet URL и включённые методы
сверяются в С45–С47; финансовые споры/возвраты — С19/С20.

## Проверки и критерии

| Критерий | Проверка |
| --- | --- |
| AC01 | Серверный quote/PC/AC/точная POST-форма, владелец сессии/CSRF/Origin, повтор создания; прежние Go/16 browser purchase cases. Browser provider POST перехвачен. |
| AC02 | Official HMAC vector и HTTP malformed boundary; дополнить TestYooMoneyHTTPReceiptBoundary: SHA1-only, unsigned test, signed test, неизвестные non-UUID/UUID labels, missing gross — нет receipt/job/access, ожидаемый400/403/200. |
| AC03 | Тот же новый HTTP test: signed wrong currency/type, net0/net>gross, unaccepted=true — сохранён спорный факт/needs_review без funding/job/access. Защищённый codepro=true покрыт прежним regression; True cases defensive/synthetic. |
| AC04 | TestYooMoneyHTTPReceiptConflict: валидный факт+repeat дают одну запись/job; изменённый net с тем же ID сохраняет первый факт, помечает dispute и блокирует подготовку доступа. Старые second-payment/late/cancel/race/manual-cross-method regressions сохраняются. |
| AC05 | Native signed-local HTTP→River→3X-UI3.7.0 new/trial/replay и paid-pending backup/restore reuse из финальной26-stage С11 только после byte-equivalence production source/API/migrations/deps; каждый исходный лог доступен и проверен. UI mock и actual HTTP/native уровни указаны отдельно. |
| AC06 | Fresh v2, один Native task и fresh Astra/high whole-branch review, exact-source CI, ручной PR→v2, actual parents/source-equal tree, preview/tag/3multiarch images, собственная приёмка #18 и Project Done. Production-ready не утверждается. |

Первичные источники сверены 2026-10-06: [форма](https://yoomoney.ru/docs/payment-buttons/using-api/forms),
[уведомления](https://yoomoney.ru/docs/payment-buttons/using-api/notifications).
Актуальный provider описывает codepro/unaccepted=false; True-тесты не обещают
такую функцию провайдера. Пример98/100 — тестовое значение, не текущий тариф
комиссии или подтверждённый минимум перевода.

Self-review: один существующий сценарий payments, без нового API/архитектуры;
предпосылки #17/#59 закрыты, С11 доставлена PR72/dev.33 перед этой веткой. Все AC имеют
конкретное доказательство, источник и границу; прошлые logs не выданы за новый
запуск. Реальная внешняя оплата исключена последним решением владельца.
