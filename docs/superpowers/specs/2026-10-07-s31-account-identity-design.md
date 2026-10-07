# С31 — способы входа и восстановление Telegram-аккаунта

Owner: #29, модуль `accounts`. Контракт `2026-10-07-s31-account-identity-v1`.
Основание: строка С31 роадмапа, С02/#7, С30/#28, M02/#56 и архитектура
`2026-10-05-modular-monolith-v1`. Пользователь разрешил автономную подготовку,
Native-реализацию и ручное слияние PR в v2 с предварительным выпуском.

## Результат

Клиент добавляет второй способ входа к существующему аккаунту, подтверждая
владение обоими каналами. Его UUID, VPN-клиент, подписка, история и ограничения
сохраняются. При утрате Telegram оператор начинает восстановление этого же
аккаунта через подтверждённый email. Два уже существующих аккаунта автоматически
не объединяются; конфликт направляет клиента в поддержку.

Один Go-процесс HTTP/River/Telegram и одна web-сборка. Telegram проверяет подпись
`initData`, accounts владеет привязками и доказательствами владения, notifications
доставляет письма, HTTP/frontend вызывают публичные операции. Новых библиотек,
общего identity framework, foreign SQL/private imports нет.

## Добавление независимого входа из Mini App

1. Авторизованный Telegram-only клиент открывает «Способы входа», вводит email.
   Backend выдаёт одинаковый 202 с challenge ID для свободного и занятого адреса.
   Занятый адрес не переносится; worker подавляет письмо с недоступной целью.
2. На свободный адрес приходит одноразовый код из восьми цифр. Завершение
   допускается только в текущей Mini App с её живым source-bound bearer, CSRF
   и тем же Telegram ID. Письмо само не выдаёт вход.
3. Клиент вводит код и новый пароль, явно принимает текущие версии условий и
   privacy. Пароль использует существующие Argon2/политику 15..128 символов и
   common-password запрет. Код живёт 10 минут, proof token 30 минут, пять попыток.
4. В одной транзакции accounts подтверждает email, сохраняет пароль на том же
   аккаунте, фиксирует consent, увеличивает credential version, отзывает все
   сеансы и другие proofs, записывает audit. Клиент входит заново в Mini App или
   обычном браузере. Нового аккаунта или VPN-клиента нет.

Повторная заявка заменяет прежнюю по существующим resend/rate-limit правилам.
Просроченный, перебранный, отозванный или принадлежащий другому аккаунту proof
ничего не меняет. Проверка свободного email повторяется под блокировкой перед
выдачей credentials; гонка с регистрацией не переносит владельца адреса.

## Привязка Telegram к существующему кабинету

1. В обычном браузере клиент с независимым входом вводит текущий пароль.
   Backend выдаёт один непрозрачный 43-символьный код привязки на 10 минут.
   В БД сохраняется только хеш; код показывается/копируется из памяти страницы.
   Он не попадает в URL, storage, audit или ошибки.
2. До создания нового аккаунта первый экран Mini App предлагает «У меня уже
   есть кабинет». Клиент вставляет код и принимает текущие legal versions.
   Telegram-модуль проверяет официальный signed `initData` по контракту С30;
   accounts атомарно проверяет второй proof и владельцев обоих каналов.
3. Telegram ID может быть свободным, уже принадлежать этому аккаунту либо
   находиться в reservation этого же аккаунта. При другом владельце — конфликт
   без переноса данных, выдачи второго аккаунта или автоматического merge.
4. Успех связывает Telegram ID с целевым аккаунтом, сохраняет первый подписанный
   start payload только если его ещё нет, обновляет consent, отзывает старые
   sessions/proofs и создаёт один audit. Затем Mini App выполняет обычный login.

Повтор использованного link proof допустим только для того же Telegram ID,
неизменившегося email и текущей credential version, ровно на единицу больше
версии proof. Audit и изменение версии повторно не выполняются. Последующая
смена credentials/binding делает такой повтор недействительным.
Новый link proof заменяет прежний. Выход web, завершение других sessions и любая
смена credentials отзывают незавершённые link proofs. Неудавшийся web logout
сохраняет CSRF для безопасного повторения; успешный logout очищает его.

## Отвязка

Отвязка доступна только из независимого web-сеанса после проверки текущего
пароля. Telegram-only клиент сначала добавляет email/пароль. Неизвестные legacy
и Stars billing facts не разрешают self-unlink: наличие `legacy_user_id` сохраняет
закрытый guard до авторитетного расширения С35. Financial guards не ослабляются.

Транзакция переносит Telegram ID в accounts-owned reservation, очищает активную
привязку, увеличивает credential version, отзывает sessions/proofs и пишет audit.
Mini login с отозванным ID не создаёт новый аккаунт; предлагает привязать кабинет
или поддержку. Тот же владелец может снова привязать ID с обоими proofs. Это
сохраняет историю использования триала. После отвязки требуется новый web login.
Свежий web-запрос уже отвязанного аккаунта безопасно возвращает unchanged.

## Восстановление при утрате Telegram

Оператор использует существующую React-admin карточку клиента. Нужны действующая
роль, собственный текущий пароль, явное подтверждение, причина 1..1000 символов
после trim и существующий `Idempotency-Key`. Разрешена только выдача первого
независимого входа Telegram-only клиенту. Перезапись подтверждённого email,
самовосстановление оператора, protected operator target и снятие ограничений
запрещены. Занятый адрес отклоняется до изменения аккаунта.

В одной транзакции accounts включает постоянный `telegram_login_disabled`,
сохраняя текущий Telegram ID, отзывает sessions/proofs, увеличивает credential
version и создаёт email recovery proof с actor ID. Старый Telegram больше не
входит. Истечение proof, ошибка SMTP или повторная доставка не снимают quarantine;
оператор может повторно выдать proof. Причина и actor сохраняются в audit.

Клиент в независимом браузере подтверждает код либо fragment-token, задаёт пароль
и явно принимает текущие legal versions. Перед commit повторно проверяются
proof, версия, quarantine, свободный адрес и действующая нерестриктированная
роль инициировавшего оператора. Утративший роль оператор не может оставить
действующее разрешение. Успех выдаёт credentials на том же UUID, резервирует
старый Telegram ID, очищает binding/quarantine и отзывает все sessions/proofs.
Ограничения аккаунта, legacy ID, VPN и деньги не меняются.

Idempotency fingerprint содержит target/email/reason/confirmation, исключает
пароль; действующий пароль проверяется и перед повтором. Результат содержит
challenge ID/expiry/resend delay, не proof token. Для ограниченного клиента
credentials не обходят существующий запрет на вход/действия.

## Данные и совместимость

Новая additive миграция accounts:

- nullable `original_kind`, сохраняемый только при первом переводе Telegram-only
  в `kind=web`; `Snapshot.SourceKind = original_kind ?? kind`. Kind продолжает
  обозначать доступность независимых credentials, источник аккаунта не теряется;
- `telegram_login_disabled boolean default false`;
- `telegram_identity_reservations(telegram_id primary key, account_id FK,
  retired_at)`; reservation никогда не передаёт канал другому аккаунту;
- existing credential_challenges purposes `initial_email`, `telegram_link`,
  `identity_recovery`, nullable recovery `requested_by` FK.

Структуры создаются внутри accounts, запросы генерируются существующим sqlc.
Используются существующие encrypted mail payload/guards, hash-only proofs,
email/IP/password limiters, session version, audit/idempotency и lock helpers.
Lock order: Telegram advisory lock перед account row, затем sorted email locks
и proof row; operator paths используют существующий упорядоченный actor/target
lock. Проверки после получения locks повторяют ранее прочитанные факты. SMTP
выполняется после освобождения SQL locks. Registration/retired-ID guards
участвуют в том же Telegram lock, поэтому unlink не создаёт второй аккаунт.

Down отказывается удалять новые identity facts, reservations/quarantine или
непредставимые в старой схеме credentials. Старые session hashes/consents/IDs,
web cookies, шесть прежних web DTO и DTO С30 сохраняются. Новые схемы additive.
Переход в kind=web сам по себе не делает legacy/Telegram billing доступным для
внешней оплаты: существующие eligibility guards сохраняются до С35/С36.
С46 импортирует исходные ID/billing/history с учётом сохранённого источника и
reservations; полная миграция реальных данных не является предпосылкой С31.

## Публичные операции и HTTP

Accounts: `GetIdentity`, `RequestInitialEmail`, `CompleteInitialEmail`,
`StartTelegramLink`, `ConfirmTelegramLink`, `UnlinkTelegram`,
`RequestOperatorRecovery`, `CompleteIdentityRecovery`. Telegram
`MiniApp.ConfirmLink` передаёт accounts только проверенную signed identity.

- `GET /api/v1/me/identity`: web либо явно разрешённый Mini bearer;
- `POST /api/v1/telegram/initial-email` и `/confirm`: Mini bearer + CSRF;
- `POST /api/v1/me/telegram/link`, `/unlink`: web + CSRF + current password;
- `POST /api/v1/telegram/link`: signed initData, link proof и Origin; bearer
  запрещён, cookie не используется как доказательство владения;
- `POST /api/v1/operator/clients/{account_id}/identity-recovery`: web operator,
  CSRF, пароль, explicit confirmation, reason и Idempotency-Key;
- `POST /api/v1/auth/identity-recovery`: anonymous proof + Origin.

Source-bound allowlist расширяется только названными Mini operations. Везде
сохраняются exact Origin, request deadlines, DTO limits и safe errors; raw query
не используется для proof. На входе валидируются все строки/ID/confirmation.
Фрагмент email-ссылки немедленно удаляется из адреса; секреты не передаются
браузером/ботом друг другу через URL. Recovery/login mail guard повторно проверяет
версии, owner, availability, quarantine и роль перед SMTP; чужая цель не получает
сообщение. Telegram-link challenge вообще не отправляется email.

## Интерфейсы

Один `AccountIdentity` экран `/cabinet/identity` и
`/mini-app/cabinet/identity`: текущие способы входа, Mini email enrollment,
web link code/unlink, причины недоступного действия, expiry/retry/выход.
Первый Mini consent экран имеет явный выбор существующего кабинета. Независимая
`/recover-account` форма подтверждает recovery; React-admin даёт операторское
действие на выбранном клиенте.

RU/EN, labels, keyboard/focus, live status/errors, busy/empty/expired states.
Abort/unmount/pagehide, смена выбранного клиента и запоздалые ответы не показывают
секрет и не применяют результат к другому аккаунту. Пароль и link code живут
только в памяти до завершения/ухода. Существующий failed web logout CSRF исправить
в этом сценарии: безопасный выход необходим для отзыва link proofs.

## Приёмка

Локальные собственные SMTP/Telegram/HTTP fixtures, TestKit Postgres/Redis и native
3X-UI 3.7.0. Ни реального перевода, ни внешнего callback, SMTP/TG, production,
переключения живого Happ/VPN или изменения доверия Mac.

Проверить SAME UUID/VPN/payment/legacy/consent/source/history после каждого пути;
гонки registration/enrollment/link/unlink/recovery и роль/ограничения; занятую
почту/истечение/пять попыток/повторы/version/session revocation; старый Telegram
при retirement/quarantine; no pre-consent account creation; отсутствие credentials
в URL/storage/audit; RU/EN/keyboard/late responses; safe failed-logout retry;
миграцию/Down/restart/старые consumer DTO и полный regression.

Native: автор выполняет задачи, один свежий обзор всей ветки Astra/high; один
авторский RED→GREEN проход по Critical/Important, Minor фиксируются и откладываются,
повторного ревью нет. Conventional Commits + Co-Authored-By. После local acceptance
и exact-source CI — manual SHA-guarded PR merge в v2, проверка actual dev release,
tag и multiarch images. Только затем #29 closed/Project Done. Реальные provider
resources, полный перенос и production readiness остаются С45–С47.
