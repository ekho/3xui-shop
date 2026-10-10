# С24 — локальная приёмка реферальных наград

Дата: 2026-10-10. Владелец [#51](https://github.com/ekho/3xui-shop/issues/51),
контракт `2026-10-10-referral-rewards-v1`. Ветка
`feature/s24-referral-rewards`, исходная база
`f75dbb72c65c40dce099e2919e8809ee947951ea`, миграция **00042**.
[Спецификация](../superpowers/specs/2026-10-10-s24-referral-rewards-design.md)
и [план](../superpowers/plans/2026-10-10-s24-referral-rewards.md).
Точная ревизия, независимое ревью и внешние CI/merge gates фиксируются в #51/PR
после коммита; этот документ описывает локальные проверки.

## Результат

Подтверждённый native order создаёт одну сохранённую DAYS-награду каждому из
двух предков графа: по умолчанию10/3 дня. Payments подтверждает точный funding
receipt; bonuses сохраняет факты и River jobs в той же транзакции. Shared
subscriptions port готовит одну абсолютную цель, прежний VPN AccessWorker
выполняет её и атомарно сохраняет результат награды с доступом и audit.
Отдельного исполнителя платежа или доступа нет.

Настройки берутся из прежних SHOP_REFERRER_* и SHOP_REFERRED_REWARD_TYPE.
Отключение новых наград не удаляет pending; money не запускает native начисление.
Legacy DAYS/MONEY, точные суммы, IDs и timestamps сохраняются. Нулевая степень
выключена, ненулевой период ограничен1..365 днями публичной компенсации.
Исходный канал/первый inviter не меняются при link-account; web trial остаётся
support-issued, расширенный Telegram trial относится к #52.

## Проверки

Использовались собственные PostgreSQL/Redis с отдельным nonoverlapping IPAM,
изолированные testkit databases, локальные TLS HTTP/SMTP/panel и synthetic
подписанные Telegram/Stars события. Реальные внешние аккаунты и сообщения отсутствуют.

| Граница | Доказательство |
| --- | --- |
| Funding и callback | `httpapi/referral_funding_test.go`: signed YooMoney HTTP, invalid signature, disputed currency/gross/protected/unaccepted money, extra/concurrent replay, order lock, native refund veto и callback rollback/retry. Focused HTTP funding/regression race PASS69.544s. |
| Факты и очередь | `bonuses/rewards_test.go`: две степени, stable order/recipient IDs, concurrency, конфигурационный snapshot, disable, atomic fact/job/audit rollback, отсутствие доказанной оплаты, MONEY no-op и immutable amount. Финальный focused race PASS3.721s. |
| Схема и владельцы SQL | Миграция42 сохраняет exact legacy MONEY и блокирует изменение/удаление native факта; конкретный DownTo41 блокируется при native данных и работает для legacy-only. Existing referrals/cycle/DownTo40 checks и ownership/composition checks PASS; финальный focused race db6.007s/app8.435s. |
| Native runtime | Четыре reward сценария и прежний `TestNativeReferralsFlow`, вместе `-race` PASS35.356s. Настоящие Go HTTP/SDK polling/River/PanelClient границы: две степени, replay/link, pending/provider503, restart, потерянный ответ, current recipient restriction и реальный native Stars refund после подготовки до записи, ordinary bonus без referral факта. |
| Восстановление до записи | `TestNativeReferralRewardProfileReadRetry`: исправленный тест RED на прежнем provider-error поведении через Go overlay1.792s, GREEN на исправлении. Сохранены access ID/target, отсутствие write/reset/grant и River retryable job; повтор выдаёт один grant audit. Fixture ускоряет штатное расписание через River JobRetry. |
| Финальная транзакция | `TestNativeReferralRewardFinalAuditRollback`: после TLS readback ошибка referral grant audit откатывает access applied/rewarded_at/panel assignment и оба audit. Protected cookie/CSRF operator reconcile завершает прежний target; новый client/add и второе начисление отсутствуют. |
| Права и параметры доступа | Native fixtures проверяют запрещённый customer reconcile, операторский reconcile, refund/restriction before-write, прежние UUID/sub ID/devices/traffic/memberships/counters, один срок и отсутствие traffic reset у наград. Результат `needs_review` остаётся pending до штатной сверки. |
| Интерфейсы | Existing `web/tests/referrals.spec.ts`: Chromium15/15 PASS9.0s, ru/en web и Mini App, semantic roles/keyboard, loading/error/retry/empty, auth/restriction и unsafe payload. Native HTTP/Telegram отдельно подтверждают pending/applied counters и source identity. DTO/UI не менялись. |
| Статика и конфигурация | Go generation, `go vet ./...`, naming/SQL boundaries, web typecheck/build, Compose config и `git diff --check` PASS. Настройки проверяют defaults/disable/MONEY/0/365 и безопасные ошибки без исходных значений. |

## Ревью и ограничения

Независимое read-only ревью нашло P2: transient profile read после подготовки
до записи сразу переводил бонус в review. Исправлено только для bonus/ErrPanel;
настоящее изменение профиля, ErrMembership, начатая/неоднозначная запись,
потеря владельца и прежний предел пяти попыток сохраняют ручное восстановление.
Повторное source/delta ревью не нашло блокирующих замечаний.

Restart локально означает новые Modules/River/Telegram runtime instances на
сохранённой БД; отдельный OS-процесс не завершался. TLS panel и Bot API —
собственные synthetic fixtures. Browser тест использует перехваченный API,
native HTTP/Telegram проверяются отдельными сценариями. Full Platform CI
перед merge дополнительно проверяет весь Go-race, connected consumers,
native process/3X-UI3.7.0, backup/restore всего public schema и три Image gates.
Локально новый reward fact не проверялся отдельным dump/restore roundtrip.
Production, live payment/Telegram/3X-UI, денежный баланс/payout, полный import
и расширенный Telegram trial не входят в эту приёмку.

## Совместимость с активацией промокодов

После merge #49 в `v2` возник реальный конфликт двух общих файлов. Интегрирован
target `6eb84888e7cdabe5ee3a8fccb385332a5675f220`: сохранены активация промокода,
reward funding/source guards и один ранний `ConfigureSubscriptions`. Общие
`access_operations.go`/`bonus_days.go` совпадают с target; миграция42 не менялась.

На объединённом дереве `470315eddef33c31b047545b2882deb9cd14a181` прошли:
семь native сценариев `-race`45.566s, включая прежние пять referral сценариев,
promo restart и Chromium с настоящим HTTP backend; 35 основных/56 с подслучаями
HTTP/bonuses/app/db проверок `-race` без failures/skips; referral+promo UI22/22
PASS9.4s. Генерация Go/web, vet, naming, Compose config и diff checks PASS.
Исходники во время проверок не менялись; последующая правка добавляет только
эту запись. Commit, независимое ревью и новые обязательные CI gates относятся
к окончательной объединённой ревизии и фиксируются в #51/PR.
