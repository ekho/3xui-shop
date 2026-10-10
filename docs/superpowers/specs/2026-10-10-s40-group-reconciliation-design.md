# С40 — сверка групп и состояния панели

Дата: 2026-10-10. Scope: #43. Native implementation и manual merge в `v2`
разрешены полным поручением задачи. Контракты:
`2026-10-05-modular-monolith-v1`, `2026-10-09-s39-server-pool-v1`, С08/#16,
registry и инфраструктурное право С38/#42. Owner: vpn.

## Уже работает

AccessWorker владеет единственной panel-write границей, account advisory lock,
durable frozen target, readback, VPN-ban и guarded reset. PanelClient выбирает
regular/euru/unlimited по сегментам tags, unlimited включает regular.
Python legacy содержит часовую сверку; в native mode его процесс запрещён.
С08 операции сохраняют цель и отказывают при изменившемся профиле.
С39 сохраняет assignment, UUID/subID/panel key и изолирует панели.

Пробелы: Go не планирует сверку после изменения tags/inbounds; disabled inbound
теряет ownership tag при diff; пропавший current inbound блокирует diff.

## Поведение

При старте native Go и раз в час scheduler проверяет только назначенные accounts
через публичную accounts-операцию. Подготовка берёт existing account owner,
пропускает unresolved trial/чужую access operation, читает точную assigned panel.
Online registry cache не заменяет live read. UUID/subID/key должны совпасть.
Known account profile определяет desired enabled inbounds. Unknown profile
пропускается; unknown-tag memberships сохраняются. Disabled known inbounds
можно detach; отсутствующий current ID не передаётся в detach.

Непустой desired и подтверждённый drift или устаревшая подтверждённая group-цель
создают `group_reconcile` operation + existing River AccessArgs атомарно. Если
memberships уже исправлены самой панелью, worker подтверждает новую цель без
panel writes. Цель фиксирует IDs и наблюдаемые лимиты.
Executor — existing AccessWorker, без второго writer. Только attach/detach и
disable при account VPN-ban; никогда add/delete/reset/enable/remap. Empty desired
сохраняет membership и вызывает alert; VPN-ban может выполнить только disable.
Срок, quota, limitIP (включая 1), traffic и unknown membership не меняются.

Повтор подготовки без drift с актуальной подтверждённой целью — no-op.
Ban-only цель с пустыми IDs подтверждает disable, сохраняет memberships и
читается как banned; unban требует восстановленного непустого enabled profile.
Pending operation не дублируется; River
сохраняет её при restart. Частичный membership write восстанавливается только
идемпотентными attach/detach после fresh readback того же клиента. Если target
устарел или нужна проверка, system operation остаётся needs_review с alert.
Следующая успешная сверка может закрыть только system group operation как
skipped и подготовить новую цель; прежний target и user/purchase/reset операции
не меняются. Изменение profile/ban/assignment останавливает запись. Captured
profile IDs purchase/assign/trial/profile/monthly/group операций сравниваются
точно до любого write: diff, сохраняющий unknown, не подтверждает frozen target.

## Ошибки, права и уведомления

Offline/invalid/missing/foreign client/empty desired не создают клиента и не
удаляют чужого. Следующий scan повторяет read-only подготовку. Системный audit
содержит account/operation IDs и фиксированный code, без VPN identifiers,
credentials, provider body или subscription URL.

Existing client_telegram_deliveries получает безопасный alert code и target
account ID, unique account/event key ограничивает одинаковый alert одним в UTC
сутки. Только verified, unrestricted infrastructure+operator account с текущим
Telegram binding и policy proof получает alert. Existing bounded delivery guard
дополнительно проверяет infrastructure grant в той же transaction перед send.
Revoked role/binding пропускает сообщение. Delivery failure не откатывает VPN.
Текст ru/en сообщает класс ошибки и предлагает проверить серверы/журнал; ключи
VPN и сырые ошибки отсутствуют. Existing access status UI и recovery остаются;
системные события сверки получают ru/en labels в журнале оператора.

## Приёмка и доставка

Synthetic TLS panel + isolated PostgreSQL/Redis: изменение tags, disabled/new/
removed inbound, unknown memberships, regular/euru/unlimited, ban, foreign
identity, offline/recovery, partial attach/detach, profile drift, restart,
unresolved purchase/reset и повтор без duplicate. Native compiled HTTP/River/
local Bot API проверяет доставку и права, данные и frozen targets.
Затронутые status labels проверяются ru/en и existing browser keyboard/error
flows; новых форм нет. Full exact-head CI и independent whole-branch review.

Migration39 зарезервирована #43; merge ждёт actual #44/migration38. Порядок
37 → 38 → 39, без чужих DDL в PR. Preview разрешён; production/внешняя панель,
реальный Telegram и пользовательские данные исключены.
