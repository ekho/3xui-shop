# С37 — Telegram support-прокси

Версия: `2026-10-09-s37-support-proxy-v1`. Владелец: [#40](https://github.com/ekho/3xui-shop/issues/40), модуль `support`.
Спецификация выбрана и проверена агентом в рамках разрешённого автономного ведения документов и реализации. Это не отдельное подтверждение пользователя.

## Цель и исходные факты

Клиент и оператор продолжают один зарегистрированный диалог через кабинет и отдельного support-бота. Сохраняются персональные forum topics, текст/медиа, закрытие/открытие, native topic events, независимый support ban, `/info`, компенсация и reset. До регистрации можно обратиться в поддержку без создания аккаунта или VPN.

База: `origin/v2` `e28ab778c3031f69a520ce5d08c4b572fd3bb08e`. С05/#10, С07/#14, М06/#60, С32/#30 и С29/#39 закрыты. Принятый [модульный монолит](2026-10-05-modular-monolith-design.md): один Go-процесс HTTP/River/Telegram, один `go.mod`, SQL только у владельцев. [С29](2026-10-09-s29-audit-history-design.md) сохраняет исходные actor/target IDs; [С32](2026-10-07-s32-client-bot-design.md) владеет главным ботом. [С05](2026-10-02-s05-support-design.md)/[М06a](2026-10-06-m06a-support-design.md) уже владеют общими обращениями; [С07](2026-10-02-s07-subscription-operations-design.md) — операциями доступа.

Старый `support_tickets` содержит `id`, `tg_id`, nullable `thread_id`, `status`, обязательные `created_at`/`updated_at`. Полной истории старых Telegram-сообщений в БД нет. Старый support middleware создавал пользователя при контакте; теперь регистрация свободна и требует собственного согласия. Старая регистрационная очередь не становится очередью триалов.

## Выбранная архитектура

Переиспользовать `telegram.Runtime` с режимом support и отдельным экземпляром/token внутри `cmd/server`. Его polling, backoff, безопасные ошибки, HTTP transport и shutdown общие. Копия polling и отдельный executable не нужны. `app` только собирает конкретные owners; `Serve` принимает дополнительные runtimes, сохраняя прежние вызовы. Support работает при выключенном главном боте; сбой канала оставляет HTTP, River и остальные задания работающими.

`SUPPORT_TELEGRAM_ENABLED=false` по умолчанию. При включении обязательны file-only `SUPPORT_BOT_TOKEN_FILE`, строго отрицательный `SUPPORT_GROUP_ID` в диапазоне Telegram и `WEB_ORIGIN` для защищённых ссылок. Значения секретов не попадают в ошибки/логи. Два polling handlers одного bot ID запрещены, даже если строки tokens различаются. Audit mirror С29 может использовать тот же support-token: он только отправляет фиксированные метаданные в General.

До polling проверить `getWebhookInfo` (webhook не удалять), `getMe` (bot ID/token prefix), `getChat` (нужная forum supergroup), `getChatMember` (бот — administrator с `can_manage_topics`). Ошибка прав/конфигурации отключает только этот канал с безопасным кодом. Transport сохраняет TLS verification, запрет redirect, ограничение JSON, timeout и redaction текущего `botapi.Client`.

`support` владеет topics, входящими receipts, jobs доставки, guest ban и legacy ledger. Telegram нормализует provider input и вызывает публичные операции `accounts`, `support`, `subscriptions`; чужих private SQL или прямых panel writes нет. Новых зависимостей, общей шины или универсального workflow engine нет.

## Аккаунты, guests и старые topics

Зарегистрированный клиент использует те же `support_conversations`/`support_messages`, пагинацию, bytes attachments, роли, read acknowledgement и лимиты: 4000 Unicode символов, 10 MiB/file, 50 MiB/conversation, 30 сообщений/15 минут. Web-only клиент получает account-owned topic; ответ из topic сохраняется в кабинете и без привязанного Telegram.

Новый private Telegram sender без account/reservation получает отдельный guest topic. `/start` показывает краткую инструкцию; сам контакт не создаёт accounts, credentials, VPN IDs, trial или entitlement. Guest сообщения остаются в Telegram, их bodies не копируются в будущий web-аккаунт. При регистрации/привязке создаётся account-owned topic; guest source/history не переписываются. Known restricted, quarantined, retired или не подтвердившая согласия identity не переходит в guest fallback.

Guest support ban привязан к исходному Telegram ID и проверяется также после его регистрации; новая регистрация не обходит ban. Снять его можно авторизованным `/unban` в исходном guest topic; `/info` нового account topic сообщает о сохраняющемся guest ban без раскрытия гостевой переписки. Account ban относится к диалогу аккаунта. Оба запрещают Telegram relay в обе стороны, независимо от VPN ban. Старое web-правило, разрешающее оператору писать в support-banned web-диалог, сохраняется.

Account topic принадлежит UUID, а не текущему `tg_id`. Привязка того же Telegram к другому аккаунту не передаёт topic/history; ответ старого topic проверяет текущий recipient proof исходного аккаунта. Смена Telegram у того же аккаунта сохраняет account topic, но старые jobs с прежним credential version пропускаются. Guest jobs прекращают доставку, если sender теперь зарегистрирован/retired. Неподтверждённый legacy orphan остаётся read-only и не маршрутизируется по текущей привязке.

Topic mapping хранит исходные bot/group/thread/user/TG IDs и account proof отдельно от текущего состояния. Пересоздание topic оставляет предыдущую строку. У active account/guest destination один topic; unknown create ACK не разрешает автоматическое повторное создание. Известный `MESSAGE_THREAD_NOT_FOUND` позволяет одну явную попытку замены; общий `BAD_REQUEST` не доказывает удаление. После uncertain create оператор явно подтверждает retry или привязывает уже созданный thread; owner проверяет уникальность, bot/group/target и права.

## Авторизация и команды

Команды и операторский relay выполняет реальный human `from.id`, связанный с подтверждённым unrestricted web-аккаунтом с текущей operator role. Membership группы или configured admin ID сами по себе не дают этих прав. Bot, anonymous sender, unknown group/topic, чужой bot callback и inaccessible callback message не запускают операторские действия.

`accounts` создаёт private context snapshot Telegram principal: account UUID, source TG ID, credential version. Это исходное ожидание, не разрешение. `LockOperatorPair` заново проверяет identity под её owner lock **до** UUID-ordered account/role locks; существующие многократные фазы С07 получают тот же context. `RequireOperator` проверяет актуальный context proof для защищённых чтений. `LockNoticeOperatorTx` уже использует этот port. Обычные HTTP contexts/signatures/body hash/principal/replay остаются прежними.

Customer inbound проверяет identity и согласие в caller Tx перед общей внутренней CreateMessage операцией. Guest mutations блокируют actor и guest identity в числовом порядке до account/role/topic rows; `accounts` предоставляет узкий caller-Tx port. Guest delivery блокирует identity и убеждается, что аккаунта/reservation нет. Нельзя оборачивать публичную многотранзакционную операцию С07 внешним Tx: это заняло бы второе соединение. Проверки должны работать при `MaxConns=1` и отзыве роли/привязки между resolution и domain lock.

| Действие | Поведение |
| --- | --- |
| Обычное сообщение private/topic | Store + receipt + post-commit delivery в одном owner Tx; команды не пересылаются клиенту |
| `/close`, `/open`, `/reopen` | Общий account conversation state или guest projection, затем forum operation с честным delivery outcome |
| `/ban reason`, `/unban reason` | Независимый support ban; reason 1–1000, actor и target в audit |
| `/info [topic-uuid]` | Ограниченный профиль/состояние/topic/delivery, ссылка на защищённую карточку; в General без аргумента — до 20 pending/unknown topic IDs; без VPN ключей/credentials/payment bodies |
| `/comp N [reason]` | 1–365 дней через `subscriptions.CreateAccessOperation`; принятие и применённый результат различаются |
| `/reset`, `/reset_traffic [reason]` | С07 reset; VPN ban и funding guards сохраняются, нет скрытого unban |
| `/approve`, `/reject [reason]` | Только существующая current manual TRIAL request; owner operator decision и audit, регистрация не блокируется |
| `/pending` | Ссылка на current trial queue/личные карточки; никаких старых registration reminders |
| `/retry delivery-uuid`, `/bind topic-uuid thread-id` | Двухшаговое подтверждение, текущая role/target/source proof; возможный duplicate показан явно |

Стабильный source command key сохраняется до вызова С07/trial owner. Повтор после crash вызывает тот же key и возвращает ту же domain operation, не добавляет дни/reset/trial. Отсутствующий optional reason заменяется фиксированной меткой команды с параметром, явно как generated reason, а не словами пользователя. Success выдачи показывается только после confirmed owner result. Comp/reset/trial недоступны для guest/orphan без account. Старые `ApprovalCallback`/`comp_reset_*` не переинтерпретируются как новое действие: показать protected legacy reference/инструкцию и не менять историческую registration approval или текущую подписку. Новые callbacks имеют собственный prefix, ≤64 bytes и owner-подтверждённую pending intent; actor заново проверяется на подтверждении. При bind оператор явно подтверждает принадлежность thread выбранному topic: Bot API не предоставляет способ доказать эту принадлежность после lost create ACK. Owner проверяет known source/target и отсутствие другого mapping; непроверенная принадлежность не объявляется фактом.

Native close/reopen service message из configured group — наблюдение реального forum состояния. Оно сохраняет source actor, включая unknown/anonymous/NULL, и синхронизирует состояние известного topic/общего диалога как provider fact. Оно не присваивает web operator role, не меняет ban/VPN/trial и не пересылает body. Unknown topic только безопасно отклоняется.

## Медиа и доставка

Telegram-to-Telegram использует `copyMessage`, сохраняя provider-copyable media, в том числе ещё не распознанный тип. В web обычные photo/document/video/animation/audio/voice/video_note/sticker до 10 MiB скачиваются через `getFile` и сохраняются как существующее защищённое attachment. Самый большой допустимый photo variant выбирается по размеру; MIME не даёт права на inline execution. Имя нормализуется до безопасного существующего support filename, произвольные URL/path/traversal/redirect отклоняются. Bytes читаются с hard cap; file size metadata не заменяет actual cap.

Contact/location/venue/poll/dice получают ограниченное plain-text представление. Для unknown, oversize, исчерпанной file quota диалога или download failure сохраняется один message с исходным текстом/caption и optional отметкой «медиа доступно в Telegram»; Telegram copy продолжается. Внутренний Telegram path допускает пустой text только с доказанным media source/этой отметкой; старый HTTP input по-прежнему требует text или bytes. Message rate limit всё равно отклоняет лишний relay и не обходится media fallback. Текст не обрезается молча, raw JSON/file IDs/token-bearing file URL не выдаются web/API/audit. Неподдерживаемый самим provider copy получает явный failure code. Альбомы обрабатываются отдельными сообщениями, как в старом боте.

Web attachment отправляется как document через stdlib multipart. Текст до 1024 символов можно поместить в caption. Длинный текст с file отправляется двумя частями: сначала полный plain-text, затем document. Job хранит confirmed части и marker каждой wire попытки. Lost ACK второго вызова не переотправляет первый; это по-прежнему один support message/audit/idempotency result.

Receipt identity строится по bot/chat/message/action/callback logical source и SHA-256 digest нормализованного содержимого/actor ID; digest не зависит от `update_id`, display names или порядка JSON keys. Нераспознанные media поля канонизируются с `json.Number`, без float64 округления. `update_id` хранится как факт; polling offset остаётся ephemeral. Forever `MAX(update_id)` не используется: provider может начать случайную последовательность после недели без updates. Exact replay не создаёт новых message/audit/job/op, conflicting digest даёт безопасный conflict. Работа с DB не теряется при post-commit crash.

До wire отдельный committed marker переводит часть job в `sending`; затем recipient binding/version/eligibility/support ban проверяются под owner caller-Tx guard на bounded 10-second send. Confirmed ACK сохраняется, lease-expired `sending` становится `unknown` и автоматически не пересылается. Только явный provider 429, доказывающий отказ, допускает bounded deferral/до 5 попыток. Network error/invalid response/crash/неуспешный completion после wire — `unknown`; definite reject — `failed`; revoked destination — `skipped`.

Оператор может подтвердить новый delivery intent **того же message**, продолжив лишь неподтверждённые части. Это не новый message или бизнес-действие. `/retry`/web action предупреждает о возможном duplicate. Topic creation/close/reopen/command acknowledgement тоже имеют markers; недоступный ответ не делает успешную domain operation ошибочной или повторяемой.

## Данные, API и аудит

Одна additive migration добавляет support-owned topic mappings, receipt ledger, delivery jobs/parts и legacy-support digest ledger; account/guest/orphan различаются constraints. Старые schemas/rows/UUID/sequences/idempotency bytes не переписываются. Down блокируется, если остались новые факты.

Общие CreateMessage paths используют одну внутреннюю caller-Tx реализацию. Когда support включён, новые web messages атомарно ставят jobs; исторического backlog replay при включении нет. Forum operator message сохраняется перед Telegram доставкой, generic main-bot support notice С05 остаётся совместимым. Web-only reply доступен в кабинете независимо от Telegram.

К `SupportMessage` в конец добавляется optional `telegram_delivery` с UUID intent, status (`queued/sending/sent/failed/unknown/skipped`), безопасным code, retry capability и media availability (`stored/telegram_only`). Для старого message поле отсутствует: прежний `delivery=stored/delivered` означает opposite read acknowledgement и не меняется. Optional поле не добавляется задним числом в сохранённые replay blobs.

Owner API добавляет operator-only `POST /api/operator/clients/{id}/support/telegram-deliveries/{deliveryId}/retry` с existing idempotency key, явным `confirmed=true` и reason 1–1000. Владелец проверяет account/message/destination/role, запрещает чужой delivery и повтор успешной/ещё активной попытки. Optional delivery projection доступна только в существующем защищённом support history; guest данные не имеют публичного web endpoint. Topic recovery выполняется в support группе через owner-checked confirmation, без новой открытой админки.

Account actions используют существующий transactional `audit_reports.RecordTx` и сохраняют actual operator UUID/TG source. Для guest/native unknown actor нужен узкий `RecordSupportTelegramTx`: system action `support.telegram` с типизированными optional account/actor/source/topic/action/reason metadata. Не создавать fake account UUID, не ослаблять `audit_events.account_id NOT NULL`, не писать это как legacy import. System journal DTO получает optional support metadata; старые system rows/JSON остаются совместимыми. У account filter guest не приобретает владельца из текущего TG binding. General mirror игнорирует reason/body/name/media и сохраняет прежний фиксированный набор безопасных metadata.

## Перенос и ограничение среды

`import-legacy-support --dry-run|--apply` читает strict UTF-8 JSON stdin до 32 MiB: envelope source bot/group и старые ticket rows. Исходные ID/TG/thread сохраняются в signed int64 без JS округления; nullable thread сохраняется как NULL. Mandatory created/updated timestamps проверяются **до** `time.Time` через С29 `ParseTimestamp`, включая лишнюю ненулевую дробную точность. Unknown fields/duplicate source IDs/несогласованный status/reversed times/conflicts отвергаются до writes. Digest охватывает исходные values; raw source metadata остаётся immutable.

Account resolution разрешена только по immutable original legacy mapping `accounts.LegacyAuditLinksTx` и original `LegacyUserID`, не по current Telegram. Orphans сохраняются и закрыты для relay; С46 может позднее связать их только с доказанным original source, не меняя digest/snapshot. Existing web conversation facts не переписываются импортированным old status: source topic projection сохраняет old status/ban, конфликт active destinations требует явного решения при переносе. Apply атомарен, dry-run read-only, exact replay no-op, changed payload conflict. CLI не запускает HTTP/Telegram/River и не создаёт аккаунты/доступ.

С46/#53 сохраняет full SQLite import/reconciliation; С47/#54 — итоговый cutover/restore и удаление Python main/support bot runtime/build. На переключении один handler на token и разрешённое окно обслуживания. В этом сценарии только owned PostgreSQL/Redis, fake Bot API, реальные локальные 3X-UI **3.7.0** и TLS SMTP. Production, live Telegram, внешний SMTP/кошелёк, Happ/VPN и доверие Mac не трогаются. С13/#18 закрыта по реализации; внешняя YooMoney приёмка остаётся pending независимо.

## Приёмка

1. Ранний thin slice: compiled server + настоящий HTTP support/web history + owned strict Bot API дают account text round-trip и exact replay. Сначала эта граница, затем медиа/commands/import.
2. Общий account диалог, web-only клиент, account switch без старого UI/private history, guest без регистрации, guest ban после signup, known restricted/quarantined/retired fail-closed; бан поддержки не меняет VPN.
3. Реальные bytes/caption, 10/50 MiB и rate, длинный text+file, HTML plain text, неизвестный/oversize тип и failure без молчаливой потери; нельзя скачать чужое вложение или token URL.
4. Revoke role/binding/credential/restriction между resolution и domain lock запрещает reply/comp/reset/guest mutations; actual UUID lock order и `MaxConns=1`, lease/ban race и rollback audit/job проверены.
5. `/comp`, reset, current trial, native known/unknown close/reopen, реальные/unknown actors, stale legacy buttons и duplicate source keys сохраняют доступ, ключи, деньги, registration facts и ровно одну domain operation.
6. ACK loss, 429, restart после committed marker, unknown create/second file part, confirmed manual retry/bind и неподтверждённые части проверены против strict fake Bot API; обычный backend/jobs живёт при отказе support и выключенном main.
7. Atomic/dry/replay/conflict legacy import, NULL thread/orphans/IDs >2^53/raw microseconds, original source proof и current rebind не передают чужую историю; нет обещания импортировать старые message bodies.
8. RU/EN, keyboard/focus/screen reader, loading/empty/error/delivery/confirmed retry и rendered account isolation; generated contract/static/module boundaries, full relevant Go/web/Python, native process restart, Docker images/smoke.
9. Один fresh whole-branch final reviewer `gpt-6-astra/high`; один авторский проход Critical/Important, без второго review. Exact-source CI, manual guarded PR→v2, actual parents/tree, prerelease/tag и три OCI images amd64/arm64. Issue/Project Done означает локальную реализацию и доставку.

Primary provider contract проверен 2026-10-09: [copyMessage](https://core.telegram.org/bots/api#copymessage), [getFile](https://core.telegram.org/bots/api#getfile), [sendDocument](https://core.telegram.org/bots/api#senddocument), [Update](https://core.telegram.org/bots/api#update), [forum methods](https://core.telegram.org/bots/api#createforumtopic). Cloud download 20 MB не повышает внутренний 10 MiB лимит.
