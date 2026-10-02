# С05: обращение в поддержку без Telegram

Дата: 2026-10-02. Мандат: автономная реализация С03–С06 из [роадмапа](../../roadmaps/2026-10-01-platform-roadmap.md).
Статус: реализация и технические проверки готовы; [приёмка С05](../../evidence/s05-acceptance.md) ожидает общего свежего review С03–С06.

## Результат и граница

Клиент ведёт одно обращение из кабинета: сообщения, вложения, история, закрытие
и повторное открытие. Оператор отвечает в React-admin С06, видит клиента,
подписку и сервер в эквиваленте `/info`, отдельно ограничивает поддержку.
Закрытое обращение автоматически открывается при новом допустимом сообщении,
как в текущем support-боте. История сохраняется после закрытия/бана.

Текущий `SupportTicket` хранит Telegram ID/topic/status без истории сообщений;
достоверно импортировать старую переписку из этой БД нельзя. Новый backend
хранит web-переписку. Telegram topics/text/media relay относится к С37;
результат записи web-сообщения не считается доставкой в Telegram или email.

## Данные и простая доставка

PostgreSQL: `support_conversations` — UUID, уникальный account_id, status
open/closed, support_banned, created_at/updated_at и подтверждённые sequence
просмотра клиентом/оператором. `support_messages` — UUID, conversation_id,
глобальный монотонный sequence, sender_account_id, sender_kind customer/operator,
text, created_at, nullable имя/bytes вложения. Все FK принадлежат этой БД.
История упорядочена sequence; последние50 сообщений плюс загрузка предыдущих50.

Одна транзакция проверяет право, блокирует обращение, сохраняет сообщение,
повторно открывает closed и пишет audit/idempotency. Потерянный ответ повторяется
с тем же UUID ключом и тем же payload; изменённый payload даёт409.
Один успешный201 означает «сохранено в поддержке». Получатель подтверждает
максимальный отображённый sequence отдельным POST; это состояние «доставлено
в кабинет получателя», а не доказательство прочтения человеком. До подтверждения
сообщение имеет `stored`, после — `delivered`. GET не подменяет acknowledgment.
Недоступность API сохраняет draft и ключ повтора, не показывает успех.

Polling каждые5 секунд после завершения предыдущего запроса, только при видимой
странице; AbortController отменяет запросы при уходе/logout. WebSocket/новая
очередь не требуются: delivery здесь подтверждается получателем после отображения.

## Вложения

Один файл до10MiB на сообщение; суммарно до50MiB вложений на обращение.
Файл отправляется вместе с текстом в одном multipart POST и хранится bytea в
строке сообщения: нет отдельных upload/orphan lifecycle или нового object store.
Текст до4000 Unicode code points, валидный UTF-8; нужен текст или непустой файл.
Filename до128 code points, без control/path characters; тело запроса максимум
10MiB+16KiB, за пределом —413. Для текстового JSON сохраняется предел16KiB.
Новых сообщений максимум30 за15 минут на актора; Redis outage fail closed.

Тип файла не определяет доверие. Любые bytes скачиваются только после проверки
владельца/роли через endpoint сообщения, как `application/octet-stream`,
`Content-Disposition: attachment`, `nosniff` и `no-store`; файл не рендерится
inline, не исполняется, не распаковывается и не передаётся внешнему preview.
Text показывается обычным React text, HTML/markdown не исполняются.
Обход quota или чужой message UUID не открывает вложение.

## Права и API

Клиентский actor — только session account. `restricted` сохраняет запрет
кабинета; `vpn_banned` не запрещает поддержку. `support_banned` блокирует новые
сообщения/reopen клиента, сохраняет просмотр истории и не меняет VPN, grant,
key, пароль или session. Оператор может объяснить ограничение ответом; unban
не снимает остальные ограничения и не создаёт подписку.

Общая с С06 роль — `operator_accounts(account_id PK FK accounts, granted_at)`.
Это entitlement подтверждённого web-аккаунта; клиент сам его не получает.
Миграция С05 также добавляет nullable `audit_events.operator_account_id` с FK
и запретом одновременного Telegram/web actor; системный audit сохраняет оба NULL.
`audit_events.support_message_id` с FK связывает событие сообщения без копирования
его текста/bytes в audit.
Операторский actor тоже session UUID, не Telegram ID из body. Проверка роли
выполняется на каждом запросе и повторно под блокировкой в транзакции записи.
Общий порядок записи: account rows actor/target в порядке UUID → entitlement
оператора → conversation → message/idempotency. CLI С06 берёт тот же account
lock перед grant/revoke; проверка права не переносится за пределы транзакции.
Изменения права доступны только CLI С06; тесты создают собственную роль напрямую.
Применяются существующие Secure cookie, CSRF/Origin, timeout/no-store и audit.

Авторский контракт — `docs/api/openapi.yaml`, generated Go/TS штатные:

- `GET /api/v1/support` — своё обращение/последние50; null до первого сообщения.
- `POST /api/v1/support/history` — previous page по before_sequence; limit50.
- `POST /api/v1/support/messages` — JSON text или multipart text/file, Idempotency-Key.
- `POST /api/v1/support/read` — собственный displayed sequence.
- `POST /api/v1/support/state` — open/closed; клиентский запрет при support_banned.
- `GET /api/v1/support/messages/{id}/attachment` — owner/operator protected bytes.
- `GET /api/v1/operator/clients/{id}/support`, POST child history/messages/read/state/ban —
  те же операции, с ролью; ban body содержит banned bool и причину до1000 символов.

Нет account_id в клиентском body/query. Неверные UUID/body/query/CSRF/Origin
отвергаются до side effects. Audit содержит action/target/actor/reason/message ID,
но не message text или файл. Нужные SQL prepared/typed, без SQL из поиска.

## Приёмка

1. Два собственных аккаунта переписываются с оператором: text/attachment,
   sequence/page50, close/reopen/auto-reopen; содержимое сохранено после reload.
2. Lost response/concurrent duplicate создают одну запись; payload mismatch409;
   ошибки/429/503 не теряют draft и не подделывают delivery.
3. Stored → delivered только после recipient ack; чужой/out-of-range ack отказал.
4. Support ban независим от VPN/restricted и истории; operator revocation
   закрывает последующие запросы и concurrent write без права.
5. Чужие conversations/attachments, неподтверждённая роль/actor body, CSRF/Origin,
   oversize/invalid filenames/quota/rate limits отвергаются без side effects.
6. Сохранённые текст/bytes/history/роль переживают настоящий PG dump/restore;
   session/proof revoke С02 сохраняется. Не заявлять восстановление отсутствующей legacy истории.
7. Клиентская и операторская ru/en/mobile/keyboard поверхность пройдена с actual
   API; `/info` card/оператор UI закрываются в общей приёмке с С06.
