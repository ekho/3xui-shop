# С36 — Другие способы оплаты из Mini App

Версия: `2026-10-08-s36-browser-payments-v1`. Владелец: [#34](https://github.com/ekho/3xui-shop/issues/34), [решение](https://github.com/ekho/3xui-shop/issues/34#issuecomment-6056519052).
Предпосылки #7/#55/#17/#59/#18/#28/#29 закрыты. С35 доставлена PR87 в `0d19b02669a90c335a7ac62d5062de4715687706`.
Автономные документы, Native-реализация и необходимые PR/manual merge в v2 уже разрешены пользователем. Production и реальная оплата не входят в эту локальную приёмку.

## Результат

Клиент из Mini App открывает отдельный браузер, самостоятельно входит по email и паролю того же аккаунта и выбирает фактически включённый внешний способ оплаты. Telegram остаётся дополнительным интерфейсом общего Go-приложения. UUID, VPN-идентичность, действующий оплаченный/пробный период и права не меняются от перехода или настройки входа.

Это ограниченная доработка существующих экранов. Документы сценария сохраняются по требованию роадмапа; новые backend API, схема, модуль, процесс, зависимость и отдельный механизм передачи сессии не нужны.

## Выбранное решение

Использовать готовый `openMiniBrowser(url:string):void` непосредственно в обработчике пользовательского нажатия. Он сначала вызывает Telegram `openLink`, а при отсутствующем/ошибающемся методе пытается `window.open(url,'_blank','noopener,noreferrer')`. Новый асинхронный запрос до вызова не добавляется.

Другие рассмотренные варианты:
- Передать одноразовый login/checkout token в URL: создаёт новый секрет и дополнительный auth-контракт; противоречит самостоятельному входу.
- Создать новый identity/status reader для кнопки: уже есть AccountIdentity и самостоятельный login; лишние запросы не помогают публичной навигации.

Общий блок в `main.tsx` виден только внутри готового Mini App на cabinet, catalogue, renew, change-plan и корректном order route. Он содержит обычную кнопку, короткое объяснение и существующую setup-ссылку. Бизнес-компоненты Catalogue/PurchaseOrder и их guards не меняются.

## Основной путь

1. В подписанном готовом Mini App клиент нажимает «Другие способы оплаты» / «Other payment methods».
2. Открывается `location.origin + '/login'`; для en добавляется только `?lang=en`, для ru query отсутствует.
3. Браузер всегда показывает email/password форму. Наличие прежней cookie другого аккаунта не подменяет этот шаг.
4. Клиент входит тем email/password, который настроен в его Mini App аккаунте. Обычный auth owner выдаёт cookie; `/me` возвращает тот же UUID.
5. Клиент открывает обычный web-каталог. Только реально включённые методы из существующего payment-methods owner доступны; основной внешний метод — YooMoney.
6. Существующий quote/order/checkout поток заново проверяет доступность, цену, метод, текущую подписку и billing guards. Доказательство оплаты остаётся за Payments owner.

Переход не переносит выбранный план, цену, сумму, заказ или intent. В браузере клиент выбирает актуальное предложение заново.

## Если email и пароль ещё не настроены

Постоянная помощь у кнопки объясняет: сначала настроить вход по email, затем использовать эти credentials в браузере. Ссылка «Настроить вход по email» / «Set up email sign-in» ведёт через существующий `link('/cabinet/identity',lang)` в Mini App.

Действует настоящий Accounts flow: email → код из письма → пароль и повтор → оба согласия → confirm. Уникальность email, challenge lifetime/rate/idempotency и conflict refusal сохраняются. Этот flow добавляет credentials существующему UUID и отзывает старые сессии; повторная регистрация и объединение аккаунтов отсутствуют.

После успеха Mini App показывает существующее сообщение о завершении сеанса. Общая header-ссылка «Открыть кабинет в браузере» также направляется на публичный `/login`, чтобы клиент мог продолжить без повторного запуска Mini App. Для локального HTTP существующий click interceptor допускает ровно собственные `/cabinet` и `/login`; внутренние `/mini-app/*` по-прежнему идут через router.

Выбранные клиентом другие credentials входят в другой аккаунт по обычным правилам auth. Доступ и подписка не переносятся; публичный URL не связывается с приватной identity. Помощь явно указывает использовать аккаунт Mini App.

## Права, повторы и ошибки

- Кнопка не выполняет финансовых/identity writes. Повторное нажатие лишь повторяет публичное открытие браузера.
- URL строится из текущего origin и выбранной ru/en локали; все входные query/hash, initData, bearer/CSRF, email, UUID, order IDs и VPN keys исключены.
- WebView cookie, storage, headers или Mini bearer не копируются в браузер. Cookies другого owned аккаунта остаются отдельными до явного login.
- Нет logout/revoke/cancel старой Stars подписки от навигации. Active/unknown recurrence, unresolved reservation, pending checkout, restriction и current access/source guards С35 остаются у владельцев.
- Неуспешные credentials, недоступный email/identity conflict и expiry показываются существующими формами; UI не пытается создать запасной аккаунт.
- При ended/expired/rejected Mini session приватные children и эта payment-кнопка исчезают. Публичная header-ссылка остаётся.
- Из SDK/fallback возвращается только попытка открытия; блокировка окна браузером не считается успешным login. Проверка OS-default browser/живого Telegram здесь не заявляется.
- Возврат по provider successURL не является оплатой и не выдаёт/продлевает доступ.

## Данные и совместимость

Backend contracts/SQL/migrations/OpenAPI остаются прежними. Accounts владеет email/password/session/UUID; Catalogue и Payments — методами, quote, деньгами и fulfillment; Subscriptions/VPN — доступом; Telegram — интерфейсом и signed transport. Операции адаптеров используют существующий public API.

Действующее С30 literal ожидание header URL `/cabinet` заменяется `/login`; его private/session assertions сохраняются. С31 initial-email revocation/UUID semantics не меняется. One-shot/recurring Stars UI и backend guards С34/С35 сохраняются. Normal web/admin не получает Mini payment-блок или Telegram SDK.

Название/домен продукта задаются при деплое. Никаких новых публичных build variables или фиксированного product domain.

## Интерфейс и доступность

Точный текст кнопки: ru «Другие способы оплаты», en «Other payment methods».
Текст setup-ссылки: ru «Настроить вход по email», en «Set up email sign-in».
Помощь: ru «В браузере войдите с email и паролем этого аккаунта. Если вход по email ещё не настроен, сначала добавьте email и пароль.»; en «In the browser, sign in with this account's email and password. If email sign-in is not set up yet, add an email and password first.»

Использовать существующие card/account-links/primary button styles. Section имеет доступное имя, помощь связана с кнопкой через aria-describedby. Native button поддерживает Tab/Enter/Space и видимый focus; ru/en и ширина375px не дают горизонтальной прокрутки. Дополнительная modal/confirmation не нужна для публичной навигации.

## Локальная приёмка

1. Mock rendered Mini checks: пять payment entry routes, точный ru/en текст, keyboard/375px/theme, чистый публичный URL при dirty search/hash, повтор без writes, SDK openLink и существующий fallback.
2. First-email setup link открывает готовый AccountIdentity. Logout/expiry/rejected SDK state не оставляет payment-кнопку; normal web не получает её.
3. Настоящий current Go graph: signed Mini trial → actual TLS SMTP initial-email code/password/consents → session revoked → header public login → независимый browser context с чужой owned cookie → manual login в исходный UUID.
4. Этот browser видит YooMoney и создаёт настоящий RUB quote/order. Provider form перехватывается до внешней сети. Без provider proof заказ остаётся pending, receipts/purchase access отсутствуют, повтор/успешный URL не меняют trial.
5. UUID/vpn_id/sub_id/panel_key, исходный trial grant/operation и реальный panel target/readback TLS3X-UI3.7.0 сохраняются. Чужой owned аккаунт не изменяется.
6. Current full web/Go race/Python/generated/static consumers и exact-source CI проходят; один fresh Astra/high whole-branch review, один author Important/Critical pass, no re-review; ручной guarded PR merge и actual v2 prerelease/OCI proof завершают Done.

Fixtures принадлежат задаче, credential/signature files0600, безопасный real reporter не выводит их. Подключаются только owned PostgreSQL/Redis, TLS3X-UI3.7.0 и SMTP с TLS. Live Happ/VPN, реальный кошелёк/уведомления/деньги, внешний SMTP и production остаются вне этого результата.
