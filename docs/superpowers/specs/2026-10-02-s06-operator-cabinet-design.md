# С06: операторский кабинет

Дата: 2026-10-02. Основание: [роадмап](../../roadmaps/2026-10-01-platform-roadmap.md)
и автономное поручение С03–С06. [С05](2026-10-02-s05-support-design.md) предоставляет
support API и общую роль. Статус: API зафиксирован, реализация в работе;
[приёмка](../../evidence/s06-acceptance.md) открыта.

## Результат

В React-admin оператор ищет клиента по email/имени/настоящему Telegram ID/UUID,
открывает карточку с источником и ограничениями, подпиской, сервером, ключом,
заявками/историей решений и обращением. Решает web-заявку на триал, пересматривает
отказ с причиной, выполняет предусмотренную сверку выдачи. Создаёт новый триальный
Telegram-only аккаунт по настоящему ID и имени, даже если клиент не запускал бот.
Отвечает с вложением/закрывает/открывает/support-ban в той же карточке.

Тарифы/компенсация/сброс и VPN-ban изменяются в С07/С08, аккаунтное ограничение —
С48; здесь их состояния читаются. Платежи/промокоды дополняются с С18/С21/С22,
как прямо задано роадмапом. До них UI показывает отсутствие источника данных,
а не выдуманные нули или платежи. Старый бот имел только последние3 транзакции;
backend не объявляет их перенесёнными до импорта С46.

## Вход и право оператора

Используется существующий email/password login и Secure HttpOnly cookie, CSRF/Origin,
отзыв сессий С02. Авторизация проверяется backend по `operator_accounts` из С05;
client-side permissions только скрывают элементы. Никакой роли из body/cookie
или `ADMIN_TG_ID` для web-сессии. Саморегистрация даёт только клиентские права.
Не добавляются новый JWT, второй пароль/вход или собственная реализация MFA.

Локальная команда `server operator grant|revoke --account-file <private-path>`
читает UUID из файла; grant требует существующий подтверждённый, unrestricted
web-account. Revoke может удалить имеющуюся роль и у restricted аккаунта.
Обе команды идемпотентны, меняют entitlement с audit и не принимают
email/password/Telegram ID.
Права отзываются независимо от session, следующий admin запрос отказал. Записи
чтения проверяют роль до выдачи; изменения повторно проверяют её в транзакции
под блокировкой. Restricted operator не использует права; logout остаётся доступным.
Записи сохраняют порядок account rows actor/target по UUID → entitlement →
request/conversation; CLI grant/revoke сначала блокирует account роли.
Первый оператор создаётся через обычную регистрацию/письмо и эту CLI.

## Единая идентичность клиента

Расширение `accounts`: `kind` web/telegram (default web), `display_name`, настоящее
nullable уникальное `telegram_id`; существующие UUID/vpn_id/sub_id/panel_key
и FK заявок/выдач/Grant/очереди сохраняются. Для kind=telegram email/password/
verified_at/consent legitimately NULL. CHECK требует положительный telegram_id
и непустое имя; web требует полный подтверждённый credential/consent набор.
Частичный web credential не допускается; Telegram-only не получает login/session
или фиктивный email. Публичный `/me` остаётся web-only с прежним контрактом.

Trial eligibility учитывает verified web либо настоящий Telegram-origin;
проверка повторяется исполнителем provision. Создание клиента, approved request,
reservation, Operation и River job — одна транзакция через общий движок выдачи.
Configured trial/target должны быть доступны; существующий TG ID/used trial
запрещают новую выдачу. Idempotency actor+key/body hash покрывает lost response
и concurrent requests. Прежние trial-used, had_subscription, assignment и
строгие target/one-grant гарантии С01 сохраняются; panel failure не создаёт вторую
идентичность. Имя не используется как panel key.

Старые Telegram ID/legacy_user_id при будущем импорте С46 сохраняются явно.
В С06 проверка уникальности относится к новой БД: параллельный старый SQLite
writer не получает выдуманной общей защиты. До внешнего включения Telegram-only
создания нужны cutover/сопоставление существующих Telegram-клиентов и прекращение
старого create handler в согласованное окно обслуживания. Локальная приёмка
использует новые тестовые идентичности и не пишет в старую SQLite/production.

## Решения и аудит

Web actor — session UUID, `trial_requests.operator_account_id` с FK. Решённая
заявка содержит ровно один тип оператора: положительный Telegram ID либо web UUID.
Pending не содержит ни одного. `decision_callbacks` остаются Telegram-only;
web использует текущие idempotency_records с principal `operator-account:<uuid>`.
Общий transaction core принимает проверенный тип actor, блокирует account/request,
резервирует Grant/Operation/job и записывает решение/audit. Нельзя получить web
решение вызовом с фиктивным TG ID. Конкурирующие bot/web решения создают одну выдачу.

Web result возвращает request/operation, без обязательной Telegram-card/delivery.
Telegram notifications допустимы как отдельная доставка; их отсутствие не блокирует
web-only операторов. canRequestTrial/notify/provision не требуют запущенного бота
или непустого cfg.Operators, когда существует разрешённый web-оператор.
Telegram-card nullable email/name/real ID корректно представляют Telegram-only
клиента; web email/старые internal callbacks сохраняют своё поведение.

Дата создания нового account сохраняется отдельным `created_at`. Для уже
существующих accounts она неизвестна: migration оставляет NULL, UI не выдаёт
дату migration/подтверждения email за дату регистрации. Поиск сортирует
`created_at DESC NULLS LAST, id DESC`.

## API и React-admin

`/admin` монтирует lazy React-admin5.15.4 (MIT, React18/19 peer), npm lock.
Custom authProvider использует existing login/logout + `GET /api/v1/operator/session`.
Custom dataProvider соответствует собственным typed схемам; UI library не задаёт
безопасность API. [AuthProvider](https://marmelab.com/react-admin/AuthProviderWriting.html),
[DataProvider](https://marmelab.com/react-admin/DataProviders.html),
[SecurityGuide](https://marmelab.com/react-admin/SecurityGuide.html) — официальные источники.

- POST `/api/v1/operator/clients/search`: q≤256 code points, page≥1, per_page1..50,
  фиксированная сортировка created_at/id; параметры SQL, не строковая конкатенация.
- GET `/api/v1/operator/clients/{id}`: identity/source, ограничения, subscription,
  server reference без credential, trial requests/operations, audit metadata,
  support summary; nullable email и настоящий Telegram ID. Пагинация истории при
  количестве>50 через отдельный typed POST, не бесконечный response.
- GET `/api/v1/operator/clients/{id}/key`: свежий защищённый own target key с
  operator authorization, private response; не копировать key в list/card.
- POST `/api/v1/operator/trial-requests/{id}/decision`, `/reconsider`,
  `/api/v1/operator/trial-operations/{id}/reconcile`: actor из session,
  Idempotency-Key, reason при отказе/пересмотре/сверке до1000 символов.
- POST `/api/v1/operator/clients/trial`: real positive Telegram ID, имя1..128,
  locale ru/en и Idempotency-Key; web-аккаунт получает trial через existing request.
- Support endpoints С05: ответ/вложения/history/receipt/state/ban; карточка
  заменяет `/info`, новая административная чат-реализация не нужна.

Точные DTO зафиксированы в [OpenAPI](../../api/openapi.yaml): OperatorSession,
OperatorClient/SearchInput/SearchResult/ClientCard, OperatorTrialRequest/Operation,
OperatorAuditEvent/HistoryInput/HistoryResult и typed inputs/results действий.
Карточка содержит первые50 заявок и50 audit records с отдельными `has_more`;
history POST принимает `kind` trials/audit и пару cursor `before_created_at` +
`before_id` (обе либо ни одной), возвращает до50 в том же порядке.
Сервер раскрывает только configured `panel_id` и `enabled`.

Новые operator DTO и Telegram-only create передают Telegram ID десятичной
строкой, чтобы JavaScript не округлял int64. Backend требует положительный int64;
старые internal request actor fields остаются прежними числами. Единственное
расширение прежнего response: TelegramPayload.email допускает NULL и получает
optional display_name/telegram_id. Потребитель бот и backend payload builder
адаптируются вместе в С06; web-email карточки сохраняют прежнее содержимое.
Остальные прежние paths/schemas остаются без изменений; `/me` остаётся web-only.

Ключ показывается только по явному запросу и очищается при уходе/скрытии/logout/
потере роли; нет key/body/email в логах, query strings, browser storage/analytics.
ru/en, keyboard focus, semantic labels/errors, mobile375px обязательны.

## Приёмка

1. Клиент без роли не открывает list/card/key/action/attachments, прямой HTTP
   обход и forged actor отказали; revoke/restricted закрывают дальнейшие действия.
2. Поиск/страницы/карточка проверены на web, Telegram-only и разных состояниях;
   не выдуманы отсутствующие identity/платежи/история, server secrets не раскрыты.
3. Approve/reject/reconsider/reconcile используют настоящего web actor в audit;
   отказ immutable, пересмотр с причиной, повторное действие идемпотентно.
4. Concurrent bot/web approve и lost response оставляют один Grant/Operation/job;
   ни повторной выдачи, ни изменения VPN identity или target.
5. Real TG-only creation с NULL web credentials выдаёт configured trial через
   тот же worker; duplicate/used/disabled config/panel uncertainty защищены.
6. При остановленном Telegram adapter и cfg.Operators=[] web request/решение/
   очередь/provision работают, новый клиент получает собственный ключ.
7. React-admin reply/attachment/close/reopen/support-ban/receipt и client С05
   проходят actual browser/API; subscription/VPN не меняются от support ban.
8. Role/actors/new identity/history/attachment переживают PG restore, старые
   sessions/proofs отозваны; ru/en/mobile/keyboard, existing С01/С02 и общий
   regression плюс один свежий whole-branch review пройдены. Live Happ исключён.
