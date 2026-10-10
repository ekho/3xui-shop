# С23 — приглашение и фактическая статистика

Issue #50, контракт `2026-10-10-referrals-v1`. Архитектура
`2026-10-05-modular-monolith-v1`, общий каркас
`2026-10-10-bonuses-scaffold-v1` из
[#48](https://github.com/ekho/3xui-shop/issues/48#issuecomment-6094467060).

## Поведение

Клиент открывает «Приглашения» в web/Mini App или `/referrals` в боте.
Один `bonuses` возвращает постоянный случайный код, ссылку на действующий
`CABINET_ORIGIN` и два уровня статистики. Ссылка `/register?invite=r_<32hex>`
передаёт код существующим `source_code`; Telegram `/start <payload>` сохраняет
payload и открывает подписанный Mini App. Старый числовой tg_id продолжает
разрешаться через владельца accounts, включая reservation после unlink.

При создании нового подтверждённого web-аккаунта или нового Telegram-аккаунта
с согласием accounts вызывает capture в той же транзакции. Сохранённые
`registration_source_code`, исходный канал и campaign attribution не меняются.
Неизвестный код остаётся исходным raw source без реферальной связи. Повторная
регистрация, resend, login, link и добавление email не назначают другого inviter.
Связь неизменяема; self-referral и цикл запрещены, включая конкурирующие записи.

`GET /api/v1/referrals` принимает только текущую сессию и не принимает account ID.
Нативный бот проверяет приватный чат, текущую Telegram identity, актуальные
ограничения и согласие до чтения. Ответ содержит агрегаты, без идентификаторов,
email/имён/платежей приглашённых. Restricted/неподтверждённые клиенты не читают
данные. HTTP/Mini App используют существующие guards, no-store и safe errors.

## Данные и границы

Миграция **00041**, зарезервированная координатором, создаёт personal links,
referrals и persisted referrer reward facts. Referrals сохраняют UUID,
отдельный legacy ID, referrer/referred account IDs, created_at и исходные nullable
referred reward timestamp/days. Rewards сохраняют отдельный legacy ID,
account ID, DAYS/MONEY, степень 1/2/nullable legacy, точный numeric(38,18),
payment_id, created_at/rewarded_at. Nullable legacy-факты не придумываются.
Rollback отказывается терять любые links/relations/rewards.

Для каждой степени показаны фактическое число приглашённых, выданные дни,
ожидающие дни и число записей каждого состояния. MONEY — число расчётных записей
и ожидающих записей, без суммы/валюты/кошелька/выплаты. Записи без степени
учитываются отдельно. Наличие `rewarded_at` определяет исторический факт;
отсутствие записи не означает обещанную награду. Флаги будущих наград не скрывают
сохранённую историю. Новые правила/worker выдачи принадлежат #51, льгота — #52,
полный replay-safe импорт — #53. Python сохраняется до #54.

Audit link creation/capture фиксируется атомарно и один раз, без кода/ссылки,
credentials или VPN identifiers. Read после restart возвращает тот же код и
те же факты; ссылка строится из текущего origin, а не сохранённого домена.

## Ограниченный план

1. Содержательные regression tests и 00041; сохранение точных старых rollback
   guards через DownTo соответствующей версии.
2. Referral-specific bonuses SQL/публичные операции, accounts legacy identity
   port, registration composition; идентичный минимальный общий каркас #48.
3. Shared HTTP/OpenAPI и нативный Telegram; ru/en web/Mini App экран и навигация.
4. Собственные PostgreSQL/Redis/TLS HTTP/SMTP/Telegram fixtures: signed consent,
   first source, повторы/link/restart, чужой клиент, audit, graph и exact amounts.
   Chromium: основной/пустой/error пути, язык, клавиатура и accessible names.
5. Независимое ревью; один principal required CI на точном HEAD, три image checks.
   Manual merge в v2 после доставленной #48/00040, затем Closed/Project Done и
   cleanup/handoff координатору. Production не входит в приёмку.
