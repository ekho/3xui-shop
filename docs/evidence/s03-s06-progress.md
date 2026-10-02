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
| С05 | Готовы | Backend и client UI готовы; focused Go-race и browser10 GREEN | Two-actor/native acceptance Pending |
| С06 | Готовы | Backend `70a0294`; React-admin и карточка готовы, полный browser59 GREEN | Native browser/restore и fresh review Pending |

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

С05 integration: shared text guard отклоняет NUL до PostgreSQL (400 вместо503);
customer receipt отправляет максимальный отображённый operator sequence, даже
если собственное сообщение новее. Оба дефекта воспроизведены RED и исправлены
focused GREEN. Coordinator повторил support service/HTTP с race и browser10;
полная двухсторонняя приёмка и PG restore выполняются вместе с операторским С06.

С06 authored API:9 paths/16 DTO; остальные paths и schemas сохранены.
TelegramPayload nullable email + optional real identity адаптированы в builder
и bot formatter; wire contract/typecheck и12 adapter tests GREEN. Новые operator
DTO используют точный decimal string для Telegram ID и NULL для неизвестной
исторической даты регистрации. Native backend/frontend работают по frozen contract.

Расширенная native приёмка С03/С04: два реальных владельца, disabled/expired/
exhausted/unlimited/VPN-ban и stop/start панели прошли. Собственные client fields,
membership/target/one Grant и прежний Docker VPN восстановлены; postflight
подтвердил health/images/pinned digest. Runtime освобождён для С06.
Общий Go-race подтвердил все service/HTTP/schema/CLI пакеты и выявил устаревший
TLS panel fixture в cross-language С01/С02: отсутствовал новый traffic endpoint.
Общий fixture исправлен; полный `go test -race ./tests -count=1` прошёл, защиту
product key не ослабляли. Python suite:102 checks прошли, один contract check
сначала не получил обязательные file inputs; с ними и исправленным fixture
повтор этого check прошёл. Standard Go/wire/sqlc/TypeScript generation no-diff.
React-admin action/card исправления подтверждены RED→GREEN: success refresh,
полная подписка/ограничения/actor history, честное unknown для none. Browser
regression59/59 после последнего none-case прошёл; suite включает build/typecheck.
Npm production audit сохраняет7 moderate записей одной upstream dependency chain;
совместимый исправленный release/override не найден, scanner не отключался.
Это отдельный gate внешнего запуска, не доказательство production readiness.
Actual С05/С06 browser/restore и свежий whole-branch review ещё открыты.
