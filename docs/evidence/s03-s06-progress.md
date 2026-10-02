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
| С03 | Готовы | Backend `f710935`, UI `41a2ade` | 8/8 AC локально приняты; общий review/fix pass завершён |
| С04 | Готовы | UI `41a2ade` | 5/5 AC локально приняты; общий review/fix pass завершён |
| С05 | Готовы | Backend/client `745250f`, operator `5c21d94` | 7/7 AC локально приняты; F1/F2 RED→GREEN, web64 и actual UI6 PASS |
| С06 | Готовы | Backend `70a0294`, React-admin `5c21d94` | 8/8 AC локально приняты; trial/role/restore и shared support UI проверены |

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

Свежий whole-branch review завершён; его выводы и завершённый fix pass ниже.
External SMTP/mailbox, целевой benchmark, upstream dependency gate и внешний
cutover/rollout остаются вне локальной приёмки. Ни production readiness, ни
запуск установленного Happ из этих результатов не следуют. Локальная приёмка С03–С06 закрыта; внешние gates не входят в её объём.

## Финальный review и один проход исправлений

Свежий reviewer `gpt-6-astra/high` проверил весь диапазон
`0e2009e3cd16b134d43ce05fc6f3b71587c30824..2dfc380bf25b8f088df5c654328d2b003f400094`,
все28 AC и20 RF, human source/generated contracts/migrations/consumers и
реальные redacted logs. Checkout/HEAD не изменял. Verdict: локальная готовность
после исправлений, **0 Critical, 2 Important, 1 Minor**. Полные зелёные suites
не повторял; source assertions прочитаны, сравнение прежних contracts подтверждено.
Новые воспроизведения выполняли текущий transpiled SupportThread в памяти,
это не новый browser/native run.

| Finding | Эффект | Критерии / решение |
| --- | --- | --- |
| F1 Important | После скрытой вкладки и более50 новых сообщений polling объединяет последний page с прежним history, но курсор остаётся старым; часть переписки нельзя загрузить без reload | С05 AC1/RF5, С06 AC7; исправлен: browser RED→GREEN + actual104/104 обоих callers |
| F2 Important | Пока send POST выполняется, новый текст/файл остаётся редактируемым и стирается поздним success прежнего сообщения | С05 AC2/AC7, С06 AC7; исправлен: native disabled + browser RED→GREEN + delayed actual POST |
| F3 Minor | Старый history response может заменить локальные receipt counters; PostgreSQL GREATEST сохраняет durable receipt, но UI повторяет ack | Устранён следствием F1: history response больше не заменяет conversation; operator race test не допускает повторного ack. Отдельный Minor fix не добавлялся |

F1/F2 подтверждены чтением всех callers: client Support и OperatorSupport
используют один SupportThread. Native50/2 проверялось после reload, поэтому
его PASS не опровергает F1. Два исправления выполнены одной ограниченной
frontend-границей, с настоящими browser RED перед изменением продукта и общей
web64/64 проверкой после GREEN. Backend/API/migrations/бот не меняются; их Go/Python
результаты сохраняют силу. После изменения UI пройден focused actual support run,
проверки роли/триала/TG-only/restore не повторяются без изменения их source.

Final: Ruling: исключённые Happ/Mac VPN/clipboard/trust остаются исключёнными —
это явная граница владельца, browser protocol intercept и owned Docker VPN
проверяют разрешённый результат — цена: реальный native import не подтверждён.
Final: Ruling: external SMTP/benchmark/production/publication/cutover остаются
отдельными gates — локальный мандат их не включает — цена: внешняя готовность
не доказана и не объявляется.
Final: Ruling: совместная уникальность PG/legacy SQLite и импорт переписки
остаются С46/С37 — локальный source обещает уникальность только своей БД —
цена: перед внешним запуском обязателен согласованный cutover старого writer.
Final: Ruling: тарифы/компенсации/VPN-ban/account restrictions/деньги/промокоды
остаются С07/С08/С48/С18/С21/С22 — здесь читаются состояния, не добавляются
будущие mutations — цена: эти будущие действия пока доступны старым кодом.
Final: Ruling: upstream dependency gate сохраняется перед внешним запуском —
review не подтвердил полную недостижимость всех parser consumers и scanner
не отключён — цена: production readiness остаётся открытой.


## Итоговое закрытие локальной приёмки

Обязательные F1/F2 исправлены в `df34ef5b4cd06a1af0e831b8fd2e5ae1889c2fff`.
F1 дополнительно воспроизведён RED при own send до latest refresh: одиночный
ответ отправки больше не считается загруженной страницей и не маскирует gap.
Message/cursor/hasMore обновляются атомарно; поздняя history не откатывает новый
cursor и conversation. Пересечение определяется по sequence загруженных страниц,
без предположения о соседних глобальных номерах. F2 использует native disabled
textarea/file; draft/file/idempotency сохраняются при failure, следующий ввод
доступен после success. F3 устранён следствием обязательного history fix;
отдельных deferred minors по этому review не осталось.

Final: minor (deferred): coordinator source inspection `Support.tsx:19,24`
показал редактируемую причину support-ban во время POST и её очистку при success.
Можно потерять причину, введённую следом; уже отправленный reason/audit сохраняется.
Это отдельный operator UX Minor, не новый обязательный composer fix F2;
browser-воспроизведение этого дополнительного случая не выполнялось.

Final: fixed F1 — customer hidden-history и own-send-before-poll tests
RED→GREEN; operator late-history race GREEN. Final focused25/25 и full web64/64 GREEN.
Final: fixed F2 — customer/operator pending-send composer tests RED→GREEN;
final focused25/25 и full web64/64 GREEN, включая typecheck/build.
Полный `npm --prefix web run test:e2e` завершился exit0 на source `df34ef5`.
Все предыдущие Go-race пакеты и Python103 PASS сохраняют силу: деревья
`backend`, `app`, `tests` точно совпадают с `5c21d94`. Стандартная generation
no-diff/vet и сохранность24 старых paths/44 schemas подтверждены до review;
contracts/generated source в этом fix pass не менялись.

Actual focused command: `node .superpowers/sdd/2026-10-02-s03-s06/s05-support-focus.mjs`
через bounded run-check с лимитом900s: **6 PASS, 0 FAIL, 0 BLOCKED, exit0**.
Оба реальных HTTPS callers загрузили latest50/older2 до exhaustion, после52
новых сообщений восстановили все104 без reload, по одному older click.
Две реальные записи второй беседы создали пропуски в глобальной нумерации;
максимум28 сообщений на actor при лимите30/15min, без bypass/SQL inserts.
При delayed POST обе стороны имели заблокированные composer fields; следующий
binary attachment сохранён и скачан byte-exact200. Все три новые роли отозваны.

Пересобран и пересоздан только gateway из frontend `df34ef5`:
`sha256:622f78822c7e59763787430b88857791148ba239d453919b203515d3cfbe8c14`.
Backend/pinned native3X-UI3.7.0 не менялись. Независимый coordinator postflight
подтвердил ready, строгие singleton headers, web-only/bot stopped, отсутствие
fault triggers/reconcile, неизменные source hashes и прежний Docker VPN файл.
Focused native readback подтвердил прежние target/panel/one Grant. Ранее
полные30 native PASS на `d6ae035` остаются доказательством неизменённых trial,
role, TG-only и настоящего PG restore; они не перезапускались.

Новые private0600 proofs: `review-fix-verification.json`,
`s05-support-focus.jsonl`, `s05-support-focus-result.json`,
`support-review-gateway-rollout.json`, `support-review-gateway-after.json`,
`coordinator-support-final-postflight.json` внутри общего local evidence.
Полные логи прочитаны coordinator; aggregate PASS не включает прошлые FAIL.

Final: Ruling: управляемый visibilityState/visibilitychange используется на
реальных HTTPS/API страницах — два дешёвых preflight показали visible во всех
окнах Chromium, а AC требует работающего UI/API, не OS tab-manager proof —
цена: физическое скрытие/occlusion вкладки не подтверждено. Само сохранение
истории104/104, отправка/bytes/receipt/authorization выполнены реальным сервисом.

Task С05.2: Ruling: delegated frontend slice выполнялся без commits/helpers —
координатор сохранял интеграцию и финальный review — цена: при неверной границе
понадобится дополнительный frontend review.
Task С06.2: Ruling: delegated frontend slice выполнялся без commits/helpers —
координатор сохранял интеграцию/runtime/whole-branch review — цена: при неверной
границе понадобится дополнительный frontend review.

Все28 функциональных AC проверены; один свежий whole-branch review и один
проход обязательных исправлений завершены. С03–С06 локально приняты.
Локальная ветка/worktree сохраняются. Push, PR/MR, merge, внешний CI,
production/release и внешний rollout не выполнялись.
Следующий отдельный этап: спецификация С48; затем С09 и зависимые С07/С08,
при этом unlimited требует С41. Эти этапы здесь не реализовывались.
