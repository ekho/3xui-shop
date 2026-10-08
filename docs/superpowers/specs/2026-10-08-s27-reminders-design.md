# С27 — предупреждения о сроке и трафике

Основание: #37, архитектура 2026-10-05-modular-monolith-v1 и решение CTA 2026-10-08-s27-reminders-v2/comment6068747885; автономный мандат #55/2026-10-06-v2-sequential-execution-and-merge. Owner notifications, Native. Ветка feature/s27-reminders от origin/v2/73dd9de3a19f614d9a270e83578d3d0bb7a6ec24. Предпосылки #8/#60/#23/#33 закрыты; повторно используются завершённые #26/monthly reset и #36/bulk panel proof.

Клиент должен вовремя увидеть приближение срока или лимита и перейти к доступному действию: первой покупке, продлению или управлению Stars. Это работает в кабинете без Telegram; бот остаётся адаптером Go-монолита. Существующие пороги, локаль и защита от повторов сохраняются. Новые web-аккаунты не получают согласие на такие письма вместе с регистрацией.

Это архитектурный сценарий: добавляются публичные owner ports, сохранённые предупреждения и три API-операции. Спецификация и последующий план выбираются автономно по прямому поручению пользователя; это не утверждение о состоявшемся ручном review документов.

## Выбранное решение

Использовать notifications.ReminderService, сохранённые предупреждения в кабинете и существующие Telegram/mail outbox. Публичные typed function ports владельцев accounts/VPN/payments собираются только в app; SQL каждого остаётся у него. Новых процессов, библиотек, универсальных интерфейсов, очередей или настроек порогов нет.

Альтернатива — отдельная очередь для каждого канала — дублирует текущие recipient/credential/lease/SMTP guards. Вариант без сохранённых web-предупреждений оставляет пользователя без Telegram зависимым от email-доставки. Выбран общий durable web-результат с независимой доставкой через существующие каналы.

## Правила предупреждений

- Срок: для неотрицательного остатка меньше двух суток выбрать порог 1; иначе меньше четырёх суток — 3. Это прежнее floor-days поведение бота. При expiry=0 предупреждения нет; после истечения срока обычное предупреждение не создаётся. Текст показывает точный срок и время наблюдения, а не обещает ровно 1/3 дня.
- Трафик: quota>0; used>=quota даёт 100%, иначе used>=quota-quota/5 даёт 80%. Формула целочисленная и не переполняет int64. Выбирается только самый срочный текущий порог; уже записанный 100/1 не приводит к менее срочному 80/3 в следующем проходе.
- Дедуп: уникальность account/kind/period/threshold в PostgreSQL, отдельно однократная постановка каждого канала. Срок — actual expiry_ms. Трафик — UUID последнего applied access target с reset=true, либо applied trial как исходный цикл. Метаданные/ban/device-only изменения не создают цикл; renewal и monthly reset создают его, включая expiry=0 с конечной квотой.
- Подтверждённые факты: VPN owner повторно использует строгие baseline/identity/target/membership и два bulk GET из С26. Неизвестные/legacy без proof, unresolved, drift, malformed, outage, ban/restriction не дают нового предупреждения. Истёкший или исчерпанный доступ не путается с отсутствующим клиентом. Само предупреждение описывает датированные параметры, а не гарантирует работоспособность VPN.
- Stars: payments owner проверяет единственную canonical native chain, текущую paid/applied recurring операцию, plan source и отсутствие противоречивых/unknown фактов. Активное рекуррентное продление в current/grace подавляет только предупреждения срока; трафик сохраняется. После paid_until+24h появляется отдельное lapse-предупреждение по текущему cycle order/paid_until. Оно ведёт к существующему Stars-состоянию кабинета и не утверждает, что списание точно не состоялось. Notifications не отменяет billing и не изменяет доступ. Неизвестный Stars-контракт закрывается без ложного обычного expiry-совета.

## Получатели и каналы

Кабинет получает последние 20 собственных актуальных, не закрытых предупреждений, включая отсутствие остальных каналов. Старые события сохраняются для дедупа; после смены периода они не выдаются как актуальные.

Telegram использует существующий client outbox, first-captured binding/credential proof и отдельный reminder ID. Новые предупреждения используют существующий route cabinet с нейтральной кнопкой «Открыть кабинет». Общий кабинет уже даёт trial первую покупку через каталог, допустимому paid тарифу — owner-checked продление, Stars — управление/проверку состояния. DTO enum renew/cabinet сохраняется для совместимости; notifications не дублирует payment eligibility. Прежнее широкое expiry/traffic→renew решение заменено после final review I1 (comment6068747885). Старые ClientOutcome JSON/result_hash, бизнес-события и operator job payloads не меняются. Обычная кнопка Telegram «Закрыть» сохраняет прежнее косметическое поведение; закрытие web-предупреждения — отдельная авторизованная операция.

Email требует отдельного явного согласия, по умолчанию false, и подтверждённой почты с независимым входом. Включение может поставить ещё актуальное предупреждение в очередь один раз на следующем проходе. Выключение запрещает ещё не начатую отправку. Используются существующий encrypted mail outbox, River и SMTP guard; письма входа/безопасности не меняются. Согласие не переносит первый email/credential proof на другой адрес после смены учётных данных. Потерянное подтверждение SMTP/Telegram допускает повтор транспорта; уникальность события и постановки в БД не выдаётся за exactly-once доставку.

Перед отправкой вновь проверяются собственник, источник/согласия, restriction, текущий recipient/credential, закрытие предупреждения, текущий VPN/Stars-период. DB period proof проверяется без нового panel-вызова; текст остаётся датированным фактом первоначального наблюдения. Preference/dismiss используют тот же email session advisory guard, что смена адреса и SMTP. Для Telegram проверка выполняется в существующей recipient-locked transaction. SQL row locks освобождаются перед SMTP; между проверкой периода и сетью нет обещания атомарности с VPN. Новая операция над периодом может обогнать сеть, поэтому текст содержит дату.

## Данные, публичные операции и безопасность

Notifications владеет reminders и reminder_preferences. Reminder хранит UUID, account UUID, kind/period/threshold, observed_at, exact expiry/traffic/Stars факты, first channel proof, флаги enqueue и dismissed_at. Значения quota/used в HTTP — точные decimal strings; период и provider charge/receipt IDs в DTO не выдаются. Additive nullable reminder references расширяют только собственные client_telegram_deliveries/mail_deliveries; новый mail kind=reminder. Старые rows, proof constraints, hashes и challenge-kind поведение остаются совместимыми. Down отказывает при новых фактах, вместо их удаления.

Runtime discriminator — reminders-v1; roadmap/date version живёт только в документах/GitHub.

- GET /api/v1/reminders: version, email_enabled, email_available, reminders[]; только собственный UUID из authenticated session.
- POST /api/v1/reminders/preferences: strict {email_enabled:boolean}, возвращает текущее состояние; при отсутствии допустимой почты включение запрещено. Неизменённое значение idempotent.
- POST /api/v1/reminders/:id/dismiss: empty body, own-only/idempotent, чужое/отсутствующее ID — 404, strict canonical UUID.

Web cookie и явно разрешённый Mini App bearer проходят текущие Origin/CSRF/session/restriction guards. Каждый новый method/path отдельно внесён в allowlist; actor/recipient/route отсутствуют во входных данных. Размеры/лишние поля/параметры запросов проверяются текущими общими guards. Preference/dismiss/create пишут существующий audit через публичный audit_reports.RecordTx; email/Telegram ID, тексты писем, ключи и charge IDs в audit/logs не попадают.

Public ports: accounts выдаёт audience и fresh recipient/credential/source/consent, с same-Tx lookup/lock и существующим WithMailGuard; VPN выдаёт подтверждённые bulk facts и текущие DB period proofs; payments — canonical Stars reminder policy в той же transaction. Ни один public port не приобретает второй connection при переданном Tx. App содержит только связывание.

## Процесс и интерфейс

Один Go-процесс запускает immediate reminder pass, затем stdlib timer каждые 15 минут. Один проход за раз, общий deadline менее интервала; обычная ошибка зависимости записывается безопасным code и не выключает HTTP. Provider read общий для аудитории, DB запись по получателю с повторной проверкой текущих period/identity facts; частичная недоступность не отмечается как успешная доставка.

Один ClientReminders component внутри общего Cabinet: RU/EN, loading/empty/error/retry, явный email checkbox/help, список датированных предупреждений, общая ссылка cabinet к существующим действиям; прежний wire renew принимается для совместимости и кнопка «Закрыть». Keyboard/focus/labels/status/alert, 375px без горизонтального overflow. Abort и account/lang/revision scope не позволяют старому ответу или сохранению согласия обновить новый экран. Auth/restriction loss скрывает данные и использует существующее поведение кабинета.

## Проверки и границы

RED→GREEN: endpoint сначала 404; чистая таблица порогов/overflow, expiry0, traffic reset с expiry0, повтор/restart/новый период, current Stars/grace/lapse/unknown; отсутствие panel/money/access writes. Recipient swap/unlink/restriction, SMTP consent race/MaxConns1, закрытие и stale period, foreign UUID, strict inputs, Mini App method allowlist. Доставки имеют буквальный RU/EN текст и безопасный cabinet link.

Rendered проверки: русский/английский кабинет и Mini App, пустой результат, loading/error/retry, изменение согласия/закрытие/ссылка, переход warning→кабинет→доступная первая покупка/paid renewal/active Stars controls в web и Mini, delayed response+scope+401/403, keyboard/375px. Нативная приёмка использует настоящую owned local3X-UI3.7.0, TLS SMTP, Bot API fixture и compiled process/restart, с readback тех же account/period facts. Затем весь текущий Go/Web/Python/static/generation набор и ровно одно свежее Astra/high whole-branch review; один авторский проход Critical/Important, Minor отложены без второго review. Source CI, ручное guarded PR→v2, фактические parents/tree, prerelease/tag/OCI labels проверяются отдельно.

Production, реальные кошельки/SMTP/Telegram, живой Happ/VPN и изменения доверия Mac исключены. С13 external acceptance остаётся открытой. С28 owns operator campaigns/edit/delete, С29 owns operator history/retention, С39 owns multi-panel expansion, С46 owns full legacy reminder dedup/identity import, С47 owns maintenance-window cutover и удаление старого Python-бота. Эти сценарии не считаются выполненными С27.

Rollback: новый mail kind нельзя отдавать старому бинарнику. После появления reminder facts используется исправление вперёд; возврат к старому исходнику допускается только при остановленном приложении вместе с проверенным согласованным snapshot БД до обновления. Миграция Down разрешается на пустых новых таблицах и отказывает при сохранённых reminder/preference/delivery facts. Реальный backup/restore и окно переноса остаются С43/С47; локальная проверка устанавливает только сохранение данных и запрет опасного downgrade.

## Rulings I made

- Ruling: reuse existing worktree, local containers, outboxes, guards and generated tooling; no new dependency or process — cost if wrong: local checks cannot establish external delivery, retained as an explicit open gate.
- Ruling: Native root implementation and one final Astra/high review — user's execution choice persists — cost if wrong: no independent per-task implementation review.
- Ruling: dated cabinet warning is the durable primary result; explicit default-false email preference is separate from auth mail — cost if wrong: users who never open the cabinet and lack Telegram receive no reminder until they opt in.
- Ruling: period checks are owner-owned and DB-fresh at delivery; no per-message provider read or exactly-once claim — cost if wrong: a concurrent renewal can precede a dated in-flight message, which cannot be recalled.
- Ruling: current single configured panel and O(n) audience reuse С26; bound each pass below 15 minutes — cost if wrong: a larger audience needs batching with durable progress, addressed before that ceiling is reached.
- Ruling: full legacy notification-state import and email retraction are separate С46/С28 work — cost if wrong: imported clients without exact period proof receive no new automatic notice yet, and SMTP cannot retract a sent letter.

Status: one Astra/high whole-branch review found Critical0/Important1/Minor0; one author correction implements the shared cabinet CTA. One author correction and all current local checks pass; unresolved Critical0/Important0. Source CI/manual v2 merge and preview publication pending. Current evidence: docs/superpowers/evidence/2026-10-08-s27-reminders.md.

Ruling: use the existing common cabinet for every new warning and leave purchase/renewal/Stars eligibility with payments — cost if wrong: the client needs one additional click to the relevant action; six rendered next-action paths and current native channel links verify that it is available.
