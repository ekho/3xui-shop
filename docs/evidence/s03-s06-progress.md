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
| С03 | Готовы | Backend `f710935`, UI `41a2ade` | 8 технических AC проверены; fresh review Pending |
| С04 | Готовы | UI `41a2ade` | 5 технических AC проверены; fresh review Pending |
| С05 | Готовы | Backend/client `745250f`, operator `5c21d94` | 7 AC проверены, включая actual restore; fresh review Pending |
| С06 | Готовы | Backend `70a0294`, React-admin `5c21d94` | AC1–7 и runtime/regression AC8 проверены; fresh review AC8 Pending |

## Промежуточная история

Записи ниже сохраняют границы и результаты предыдущих этапов. Их Pending и
неуспешные attempts описывают состояние на тот момент; актуальная сводка —
[итоговые проверки](#итоговые-проверки-перед-review).

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

Финальная проверка source `5c21d94`: полный `go test -race ./... -count=1`
прошёл во всех пакетах, полный `poetry run python -m unittest discover -s tests -v`
прошёл103/103 с необходимыми file inputs. Это новые полные GREEN results;
прежние частичные/неуспешные запуски сохранены отдельно. Browser59/59,
typecheck/build и generation no-diff остаются действительными: их source не менялся.

Actual С05/С06 run ещё не завершён. Ранние ошибки ожиданий driver исправлены;
добавлено ожидание подтверждения email и завершённого POST сообщения. Общая
proxy-конфигурация теперь устанавливает singleton headers при записи ответа;
raw HTTP проверки HTML200/API401/health200 подтвердили ровно один nosniff/no-store
и сохранённые CSP/Referrer. Gateway пересоздан из прежнего image, остальные
сервисы и прежний owned VPN сохранены. Строгая attachment проверка не ослаблена;
полный двухсторонний сценарий, restore и final review остаются открыты.

Actual С05/С06 continuation на `b3bfac4` подтвердил строгую attachment boundary,
двухсторонние recipient receipts, idempotency/CSRF/Origin/input guards, support ban,
RU customer/mobile/keyboard и history50. Составная проверка web reject остановила
общий run; source продукта не меняли. Узкая controlled browser проверка exit0
воспроизвела create→immediate GET без UUID, затем POST201→valid UUID→reject/card
с правильным web actor и required Telegram-null. Это достаточная гонка исходного
driver; URL прежнего сбоя не был сохранён, поэтому точная причина остаётся выводом.
Для повторов добавлены ожидания POST и UUID preconditions, не ослаблены guards.
Драйвер дополнен actual RU operator/keyboard и body-only search pagination;
их выполнение вместе с поздними trial/reconcile/TG-only/restore cases ещё ожидается.
`deploy/s06/` — воспроизводимый кандидат полной локальной приёмки, общий PASS
по нему пока не заявлен. Полные предыдущие неуспешные attempts сохранены privately.

## Итоговые проверки перед review

Actual С05/С06 run на чистом `d6ae035188edd6c0d5b14dfa9ec39abdb7da8a58`:
**30 PASS, 0 FAIL, 0 BLOCKED**, child exit0, без timeout. Проверены actual RU/EN
375px/Enter search/card и страницы поиска1/2; оператор и два клиента, text/bytea
attachments, download-only singleton headers и foreign denial, explicit recipient
ack, close/reopen/auto-reopen, history50/2, support ban при active key/VPN.
Web reject/reconsider/approve/replay и controlled needs_review→reconcile сохраняют
настоящий UUID actor, один Grant/job и native target. Настоящий owner TG ID создаёт
Telegram-only клиента с NULL web credentials через тот же worker; duplicate409.
Все эти операции прошли без Telegram operators/adapter. Fixture сбоя apply удалён
до reconcile; fixture restricted ограничен новым собственным тестовым оператором.

Настоящий PG dump/restore сохранил exact per-account digests account/role/support
text/bytea/receipts/trial/actor/audit/operation/grant и native target/panel identity.
Старые sessions/proofs отозваны maintenance SQL, payload очищен; старый cookie401,
fresh login/key/file200. Прежний Docker VPN config совпал побайтно и proxy работает.
Revoked/restricted operator получил403, раскрытый ключ очищен; logout завершился.
Coordinator независимо подтвердил HTTPS readiness, web-only, adapter/reconcile
stopped, отсутствие обоих fault triggers, pinned panel digest и работу own VPN.

| Проверка | Точная команда и поверхность | Проверенная ревизия / результат |
| --- | --- | --- |
| Все Go пакеты | `go test -race ./... -count=1` из `backend`, real own PG/Redis через `S01_TEST_DATABASE_URL_FILE` и `S01_TEST_REDIS_URL_FILE` | `5c21d94`, exit0, все пакеты |
| Полный Python | `poetry run python -m unittest discover -s tests -v`, те же file inputs | `5c21d94`, 103/103, exit0 |
| Web + build/typecheck | `npm --prefix web run test:e2e` | `5c21d94`, 59/59, exit0 |
| Go vet | `go -C backend vet ./...` | source `d6ae035`, exit0 |
| Генерация API/store | `go tool oapi-codegen -config oapi-codegen.yaml ../docs/api/openapi.yaml`, `go tool sqlc generate` из `backend`; `npm --prefix web run api:generate`; `git diff --exit-code` только generated files | source `d6ae035`, exit0, no diff |
| С03/С04 native/browser | `node deploy/s04/browser.mjs` | runtime snapshot `0a14733`, expanded driver exit0; core source Connection/subscription/panel не изменялся до `d6ae035` |
| С05/С06 native/browser/restore | `node deploy/s06/browser.mjs` через plugin `run-check.mjs --timeout-seconds 900 --lines 20` | чистый `d6ae035`, 30 PASS, exit0, 293837ms |

После source suites `5c21d94` менялись только Caddy headers, smoke/native drivers
и evidence. Backend/web/bot/tests source не изменялся, поэтому полные suites
остаются применимы; gateway headers подтверждены actual native run. Матрицы
[С03](s03-acceptance.md), [С04](s04-acceptance.md), [С05](s05-acceptance.md),
[С06](s06-acceptance.md) содержат все28 AC и различают native/real-PG/TLS/browser
fixtures. Необычные данные панели проверены на TLS fixtures без порчи native БД;
Happ navigation перехватывается, clipboard только в памяти browser context.

Локальные redacted evidence/logs: `s03-s04-surface-evidence.md`,
`s06-native-final-acceptance.md`, `s06-native-final-rows.jsonl`,
`final-source-checks.json`, `coordinator-final-postflight.json` внутри
`.superpowers/sdd/2026-10-02-s03-s06/`. Предыдущие неуспешные attempts сохранены
отдельно и не считаются PASS. Публичные drivers и именованные tests воспроизводимы;
секреты/личные ID/email/cookies/ключи/тела сообщений в evidence не копируются.

Ruling: исходная последовательность RED→GREEN части ранних specialist tasks
не подтверждена доступными историческими логами — не отмечать эти RED checkbox
как выполненные и не выдумывать прошлое. Текущие проверки требований исполнены
полностью и GREEN; функциональная приёмка сохраняет все28 AC. Цена отклонения:
невозможно подтвердить соблюдение начального TDD-порядка для этих задач; свежий
review отдельно проверяет достаточность нынешних тестов. RED→GREEN исправлений
NUL/recipient ack/React-admin card/refresh/none и actual header RED→GREEN сохранены.

Свежий whole-branch review ещё Pending. External SMTP/mailbox, целевой benchmark,
upstream dependency gate и внешний cutover/rollout остаются вне локальной приёмки.
Ни production readiness, ни запуск установленного Happ из этих результатов
не следуют. Цель остаётся active до рассмотрения review и финальной сверки.
