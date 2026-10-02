# С03–С06: автономная реализация

Мандат 2026-10-02: реализовать все четыре сценария по роадмапу, решения принимать
автономно. Начальная ревизия `0e2009e3cd16b134d43ce05fc6f3b71587c30824`.
Исполнитель готовит отдельные spec/plan; ручные approvals заменены явным
поручением принимать решения автономно. Coordinator владеет contracts,
интеграцией/commits/приёмкой; native backend/frontend specialists — своими файлами.
Один свежий whole-branch review после всех четырёх сценариев. Ни production,
ни живой Happ/настройки Mac, ни внешняя публикация в мандат не добавлены.

| Сценарий | Спецификация/план | Реализация | Приёмка |
| --- | --- | --- | --- |
| С03 | Готовы | Backend `f710935`, UI `41a2ade`; focused GREEN | Native acceptance Pending |
| С04 | Готовы | UI `41a2ade`; 39 browser checks GREEN | Native acceptance Pending |
| С05 | Готовы | Author contract/generated ready; implementation pending | Pending |
| С06 | Готовы | Pending | Pending |

Discovery: сохранены профильные split/unlimited/ban случаи, платформы + QR,
Telegram-only создание триала и shared /info card. Старый support не хранит
историю сообщений/вложений: backend-owned web conversation строится в С05,
Telegram proxy остаётся будущим С37. Native3.7.0 traffic endpoint реально проверен
на собственном стенде: up/down, email/uuid/subId; raw данные не опубликованы.

С01 локально принят; С02 реализован/проверен. External SMTP/mailbox, целевой
benchmark и внешний rollout остаются gates перед внешним запуском.
Полная цель С03–С06 остаётся активной до реализации и доказанной приёмки всех строк.

С03 frontend handoff: четыре целевых Playwright checks GREEN, существующие29
С01/С02 GREEN, typecheck/build GREEN; обычный test discovery дополнен С03.
Backend исправляет семантику ban: независимый accounts.vpn_banned, не inbound;
unlimited включает regular, но не означает безлимитные quotas. Native acceptance
выполняется после integration. С04 docs готовы: local QR, пять платформ,
явный собственный key fetch и ручной fallback; установленный Happ не запускается.

Последний С03 RF4 test задерживает старый native read, сохраняет новое наблюдение
и доказывает единый snapshot в позднем response. Backend focused race GREEN;
профиль/counters/metadata сохраняются атомарно. С03/С04 browser39 GREEN,
typecheck/build GREEN, worker-owned output logs прочитаны coordinator.
Native acceptance использует текущий собственный Docker stack; image панели
сверен с pinned3.7.0 digest. С05 добавляет12 paths/10 schemas без изменения прежних
paths/schemas; generated models и nullable response contract check прошли.
С06 дизайн сохраняет единый UUID/worker и real Telegram-only identity; web actors
отдельны от Telegram. Между новой PG и старой SQLite нет общей uniqueness до
cutover/импорта; внешнее включение TG-only creation требует прекращения старого writer.
