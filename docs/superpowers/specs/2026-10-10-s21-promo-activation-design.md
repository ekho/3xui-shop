# С21 — клиент активирует промокод

Основание: #49, delivered #8/#14/#48, base f64c13ce, public contracts
`2026-10-05-modular-monolith-v1`, `2026-10-10-bonuses-scaffold-v1`,
`2026-10-10-promocodes-lock-order-v1`; activation contract
`2026-10-10-promocode-activation-v1` в #49/6095033564.

## Поведение и границы

Клиент вводит одноразовый код, который выдаёт сохранённое число дней.
Пробелы по краям удаляются, регистр сохраняется как в legacy. Native duration
1..365; legacy duration за пределами compensation contract сохраняется в БД,
но не выдаётся без отдельного решения. Нет скидки, денег или reset traffic.
Unknown/deleted code: PROMOCODE_INVALID; consumed: PROMOCODE_USED.

Accounts restriction, identity/source/policy eligibility и VPN-ban проверяются
до replay. Unlimited, perpetual expiry=0, недоступный назначенный сервер,
пропавший прежний panel client отклоняются. Истёкший regular/euru клиент
продлевается от max(подтверждённый expiry, now), без смены устройств, лимита,
счётчиков, профиля и VPN identities. Truly new account получает configured
bonus devices и дни без traffic cap. Это прежний компенсационный путь #14.

Ответ означает принятие persistent access operation, не синхронную выдачу.
Состояния pending/provisioning/applied/needs_review доступны отдельным GET.
Потерянный ответ повторяется с прежним ключом; новый ключ не выдаёт дни снова.
Needs_review сохраняет следы и требует прежней операторской сверки #14.

## Один владелец и атомарность

Bonuses владеет ActivatePromocode/GetPromocodeActivation, кодом и историей.
New(pool, authority, now) остаётся; ConfigureSubscriptions подключает один
существующий subscriptions owner. GrantBonusDays/GetBonusOperation — узкие
публичные ports subscriptions, повторно использующие #14 preparation/worker.
HTTP/Telegram не используют SQL/private-пакеты модулей.

Предварительно account -> promocode lock сохраняет ID/duration/revision.
После panel read-only preparation финальная транзакция повторно проверяет
аккаунт, baseline и locked promocode. Marker/actor/duration snapshot,
access_operation_id, history/audit, idempotency response, grant и River job
сохраняются вместе. Ошибка любого шага откатывает всё. Ни replay, ни restart
не вычисляет дополнительные дни повторно; worker использует absolute target.
Согласованный account -> promocode lock обязателен и для self-activation
оператором. Edit/delete race не теряет использованный код или историю.

No DDL: используются 00040 и existing access_operations/idempotency_records.
00042 не создаётся; referral-specific/#50/00041 не включаются в исходную ветку.
Код отсутствует в audit/events/operation reason/worker/logs/URL. В events
сохраняется только metadata и ссылка на operation. Legacy Python сохраняется
до #54; он не запускается вторым исполнителем PostgreSQL-операций.

## Интерфейсы

POST /api/v1/promocodes/activate {code}, Idempotency-Key; GET
/api/v1/promocodes/activations/{id}. Account из cookie/signed MiniApp bearer;
cookie POST требует Origin/CSRF, bounded strict DTO без account/body override.
DTO: promocode_id, duration_days, operation_id, status. GET только для владельца.

Cabinet/MiniApp: одна labelled форма RU/EN, disabled busy controls, error focus,
empty/help state, keyboard, status refresh, прежний key при uncertain retry.
Секретные значения не сохраняются в URL/localStorage и не логируются.
Native private /promocode CODE: verified Telegram actor, stable update key,
safe RU/EN response без эха кода, URL-кнопка обычного кабинета. Команда и legacy
promocode callback должны направлять к общему owner, не Python grant.

## Приёмка

Own PG/Redis/panel/botapi fixtures: реальные HTTP/signed MiniApp/native bot/
River worker, no external messages. Одновременная активация разных клиентов,
same-key replay/body conflict, rollback, restart/lost panel response, rights,
no-client и compensation limitations/traffic/devices/stable IDs, edit/delete
× обычный клиент/оператор/self-activation. Audit и история не содержат кода.
Chromium RU/EN/keyboard/error/empty/retry/status. Existing backup retains all
public rows; narrow meaningful acceptance and one exact-head principal full
Platform CI + three images. Независимый ordinary review, manual merge в v2,
Closed/Project Done, own fixture cleanup, handoff родителю. Preview разрешён;
production/live panel/Telegram и ручной screen reader не проверяются.
