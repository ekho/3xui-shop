# С24 — реферальные дни после подтверждённой оплаты

Issue #51, контракт `2026-10-10-referral-rewards-v1`. База `f75dbb7`,
архитектура `2026-10-05-modular-monolith-v1`; referrals/source identity из #50,
shared compensation port `2026-10-10-promocode-activation-v1` из #49.

## Поведение и права

Один существующий payments executor подтверждает точный funding receipt заказа.
В той же транзакции bonuses записывает DAYS двум предкам неизменяемого графа:
по умолчанию 10 дней непосредственному inviter, 3 дня его inviter. Нулевое
число дней отключает соответствующую степень. Отключение новых наград не
удаляет сохранённые pending факты. MONEY остаётся историей без начислений.
Действующий legacy параметр `SHOP_REFERRED_REWARD_TYPE=money` отключает новые
награды; `days` сохраняет обычные дни. Периоды0..365 соблюдают предел публичной
compensation операции. Настройки передаются существующим Compose backend.

Stable key заказа и получателя не зависит от webhook, Telegram update,
email/link/unlink или очереди. Первый source channel/inviter не меняется.
Self/cycle исключаются действующим графом; деньги без точной native funding
связи, refunded/unaccepted/review receipts и дополнительные receipts нового
начисления не создают. Старые pending записи сохраняются; полный перенос
доказательств их оплаты относится к #53.

Worker проверяет текущую source identity, consent/restriction/VPN-ban получателя
и подтверждённую оплату. Недоступный provider, занятый access owner и ограничения
откладывают выдачу. Один shared subscriptions GrantBonusDays атомарно сохраняет
access operation и reward link. Он сохраняет абсолютный срок, devices, traffic,
profile и VPN identity; один прежний AccessWorker выполняет target. Права и
funding проверяются перед внешним эффектом и в финальной транзакции. Restart
или потеря ответа не прибавляет дни заново. Readback success отмечает rewarded_at
и audit; review остаётся pending до действующего operator reconciliation.

Льгота приглашённому остаётся по исходному каналу: web требует поддержки,
Telegram имеет свой automatic trial. Увеличенный Telegram trial завершает #52;
эта задача не создаёт ещё один trial executor или web automatic trial.

## Данные, интерфейсы и совместимость

Миграция **00042** добавляет nullable source_order_id/access_operation_id
в referrer_rewards с native immutability и guard rollback. Legacy UUID/IDs,
точные суммы и timestamps не переписываются. Public payments port
ConfirmedPurchaseTx + paid callback используют прежний funding check.
Bonuses владеет своими SQL и RewardWorker; subscriptions/vpn сохраняют свои
публичные операции и единственного исполнителя доступа.

GET /api/v1/referrals и native /referrals показывают те же pending/granted
агрегаты из #50. DTO и UI не расширяются. HTTP actor определяется сессией,
Telegram actor — signed/private identity. Audit хранит только ID факта и
access operation, без codes, receipt payload, токенов или VPN identifiers.

## Приёмка

Собственные synthetic PG/Redis/TLS HTTP/SMTP/panel/Telegram fixtures:
две степени, invalid/extra/repeated payment, rollback/jobs atomicity,
concurrency, поздняя выдача/provider failure, потерянный ответ, worker restart,
restriction/refund before write, immutable source/link, один access ID/expiry,
сохранение лимитов и audit. Existing ru/en referral UI/error/empty/keyboard
checks сохраняются; native HTTP/Telegram показывают настоящую смену фактов.
Конкретные downgrade guards проверяются DownTo(41/40), backup включает весь
public schema. Локальная приёмка, independent review, full exact-head Platform
и три Image gates предшествуют manual merge в v2/Closed/Project Done. Preview
разрешён; production и live внешние системы отсутствуют.
