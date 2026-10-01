# С01 — evidence и незавершённая реальная приёмка

Дата локальных проверок: 2026-10-01. Ветка: `feature/web-trial-s01`.
Base: `74c124906462f5b75a323aa9b80944dd08df2037`.
Проверенная ревизия кода: `333c96bb11789707cc406b9f2bb55f6e60015aaa`.
До Docker-приёмки полный набор Tasks1–7 был проверен на `0ce85fe`; backend
после исправления native API3.7.0 повторно прошёл полный Go race suite и vet.
Docker API/VPN/restore/rollback и мобильный browser повторно проверены на3.7.0.
Предыдущая приёмка3.5.0 на `7306973` сохранена как историческое доказательство.
Полная спецификация: [S01](../superpowers/specs/2026-10-01-s01-web-trial-design.md).

**Implementation:** Tasks1–7 локально реализованы и проверены; локальная подготовка
Task8 проверена. Независимое ревью выполнено; четыре Important исправлены
с проверками RED→GREEN и полным зелёным набором. Два Minor отложены ниже.
**Delivery:** локальные commits, без push/PR/merge/remote CI/deploy.
**Acceptance:** OPEN — локальные Mailpit/3X-UI/Xray/VPN/PG restore проверены.
Настоящие Telegram approve/reject и обновления карточек проверены на отдельном
тестовом боте, пересмотр поддержкой сохранил причину и создал связанную заявку.
Backend завершил выдачу после остановки бота; key и настоящий VPN доступны.
Ручной импорт в Happ, внешняя доставка email и замер на целевой машине остаются
открыты.
Локальный стенд запущен и сохранён.

## Проверенные локальные команды

URL-file prerequisites и setup: [runbook](../runbooks/s01-test-rollout.md).
Все команды используют свои случайные test DBs, не production.

| Проверка | Результат |
| --- | --- |
| `make -C backend generate`, TS `api:generate`, generated no-diff | PASS |
| `go -C backend vet ./...`, web `typecheck`/`build --mode test` | PASS |
| `go -C backend test ./... -race -count=1` | PASS после исправлений ревью, включая running-job dump/restore |
| Python `unittest discover -s tests -v` | PASS, 102 tests |
| Web mock `test:e2e` | PASS, 10 tests |
| `S01_E2E_MODE=real npm run test:e2e` из web | PASS, real HTTPS API/PG/Redis/River/Python + browser; external services fixtures |
| `TestS01BackupRestore` | PASS, actual local pg_dump/restore с running River job, HTTPS panel fixture |
| Docker builds (backend/web/bot) | PASS, local ARM64 images, no publish |
| Compose config / container smoke / bounded rollback | PASS, disposable local project, real TLS validation; restore runtime обрабатывает provision, оставляет mail без попыток, не открывает HTTP |
| Native panel parser `TestPanelClientRecordNative` | RED→GREEN: numeric id + uuid на3.5.0; новый точный native not-found3.7.0 и en-US; unknown/empty error не absence |
| `python3 deploy/s01/local.py up` | PASS: собственная 3X-UI3.7.0/Mailpit1.31.1/PG/Redis/HTTPS, native preflight до включения trial; exact origin/32 TCP8000 finalRule |
| Docker API/VPN/restore (`local.py check`) | PASS: настоящее TLS/auth письмо, adapter decision, native panel readback, VLESS/TLS Xray26.7.28 и PG restore исходной операции |
| `local.py rollback()` | PASS: gateway остановлен; новые запросы закрыты, PG/панель/действующий VPN сохранены; gateway возвращён |
| `node deploy/s01/browser.mjs` | PASS: мобильный375px browser, actual Mailpit/API/panel, fragment очищен, no-store, HttpOnly/Secure, logout; Telegram transport не вызывается |
| Dedicated test bot + настоящий оператор | PASS: доставка, approve/reject, actual actor, send/edit одной карточки, support reason/confirmation, approve связанной заявки, bot-stop до завершения выдачи, key/native readback/VPN и доставка после возобновления |
| Independent whole-branch review | Проверен диапазон `74c1249..a6ca9d0`; Critical0, Important4 исправлены автором с RED→GREEN, Minor2 отложены. Повторного ревью не проводилось |

Tool versions: Go1.27.1, Node24.11.1, Python3.13, Poetry2.5.1, Chromium153
(Playwright1.63.0); React19.3.0, Vite8.3.2, TS5.9.3. Official container digests
закреплены в Dockerfiles/Compose. Для повторной native3.7.0 проверки host runtime
обновлён до Node26.10.0/Python3.14.7; bot image остаётся Python3.13.
Измерение Argon2id на local Apple M3 Pro:
`BenchmarkPasswordHash`50.2ms/op,19,926,727B/op,32allocs/op. Hash parameters
19456KiB/2/1, максимум два параллельных вычисления. Замер на целевой test машине
остаётся pending; это измерение не гарантирует её latency.

## AC — статус автоматизации и реальных проверок

| AC | Автоматическое доказательство | Статус / остаётся |
| --- | --- | --- |
| 1 no-Telegram → trial → VPN | Native Docker panel/SMTP/Xray data plane + mobile browser; genuine operator approve, native readback/VPN | Docker + real Telegram PASS; внешний mailbox и ручной Happ import pending |
| 2 bot down before decision | `TestS01FlowAndFailures`; регистрация/письмо/login/request при остановленном test bot, доставка после запуска | Local + real Telegram PASS |
| 3 bot down after approve | Worker starts after Python consumer exits; настоящий approve, наблюдённые callback.created_at < Docker FinishedAt < granted_at, key/native readback/VPN при остановленном боте | Local + real bot-stop PASS |
| 4 scanner/duplicate/parallel verify | registration tests; browser fragment cleared, no automatic POST | Local PASS |
| 5 token/code TTL/reuse/brute force | registration tests with controlled clock | Local PASS |
| 6 Origin/CSRF/owner/key privacy | HTTP boundaries, key tests, real browser private ingress404/no-store/logout | Local PASS |
| 7 request/idempotency duplicates | trial atomicity tests + stable browser retry key | Local PASS |
| 8 forged actor/anonymous/old callback | handler tests, actual parsed aiogram Update through HTTPS client; настоящий callback от allowlisted оператора | Local PASS; positive real operator identity PASS, negative cases автоматизированы |
| 9 concurrent decisions/lost reply | PG decision tests, repeated real consumer callback, internal-only winning409 | Local PASS |
| 10 reject final/account unchanged | trial tests + mock browser reject/support; genuine reject, отсутствие Operation/Grant, login и public retry409 | Local + real Telegram PASS |
| 11 support reconsider/old card/used guard | trial/FSM tests reason+stable confirmation key; реальные «Пересмотреть» → причина → «Подтвердить» → approve, один связанный request/Grant и неизменный отказ | Local + real support path PASS; used guard/old buttons автоматизированы |
| 12 panel down/no regular/bad config | provision/panel/config tests | Local + native panel preflight PASS; production version не проверена |
| 13 interrupted panel add/restart | lost reply1create, apply rollback/reconcile, physical PG session loss | Local + native panel running-job restore PASS |
| 14 partial/foreign/no hop | concrete panel partial attach/preserved protocol fields/mismatch tests | Local + 3.7.0 get/add/attach/not-found PASS; несовпадения/foreign guards проверены fixtures |
| 15 immutable config/bytes/N+1 | snapshot/zero/overflow/expiry provisioning tests | Local PASS |
| 16 duplicated card/edit fail | lease/ack tests + Python claim/complete replay; delayed decision, deleted-card replacement, unchanged-card ack, lost replacement reply; реальные approve/reject edits одной карточки | Local PASS; normal real Telegram send/edit PASS, failure cases автоматизированы |
| 17 legacy/unknown groups | SQL cohort separate, no legacy apply call, fixture rejects other writes/groups | Local PASS; Docker panel собственная, legacy jobs отсутствуют |
| 18 logout/TTL/keyboard/ru/en/error | session tests +10 browser tests; inspected375/1280 screens | Local PASS |
| 19 PG restore retains operation | real local dump after external add before applied, running River job; штатный rescue, same target/grant/session | Native panel + real PG dump/restore PASS; original target/grant/session/VPN сохранены |

## Исправления независимого ревью

1. Dedicated acceptance entrypoint больше не падает на неинициализированном
   `IsDev`: свежий процесс проверяет разрешённого и постороннего оператора.
2. Очередь доставки — единственный writer карточки. Запоздалый ответ approve
   не переписывает уже доставленную `needs_review` и её кнопку сверки.
3. После удаления карточки отправляется замена с актуальными кнопками и новым
   message ID. «Message is not modified» подтверждает прежнюю карточку;
   потерянный ответ замены оставляет lease неподтверждённой.
4. Поставляемый режим `reconcile` запускает только provision queue. Docker smoke
   проверяет running-job rescue, отсутствие HTTP и untouched mail; dump/restore
   использует настоящий River worker. В тесте сдвигается только `attempted_at`
   для ожидания порога, state/operation/grant/target сохраняются.

## Зафиксированные решения

| Решение | Основание | Цена ошибки |
| --- | --- | --- |
| Продолжение означает реализацию и локальные commits; публикация отдельно | Подготовка плана завершена, пользователь поручил продолжать | Обратимые локальные изменения |
| Локальные Docker-ресурсы разрешены; полная приёмка остаётся открытой | Новое указание пользователя заменяет ожидание panel/SMTP; Telegram/Happ пока не предоставлены | Нельзя считать локальный SMTP внешней доставкой или Xray импортом в Happ |
| DecisionResult сохраняет card и delivery_state | Полная спецификация важнее сокращённой сигнатуры плана | Дополнительные внутренние поля |
| Implicit-owner key endpoint без готовой операции возвращает409; чужой resource path404, account_id query400 | В принятом API нет параметра владельца/ресурса для примера404 из плана | Клиент должен обрабатывать409 |
| compose.acceptance.yml отделён от compose.test.yml | Обязательные внешние secrets не блокируют fixture tests | Дополнительный manifest и дублирование image pins |
| Native3.7.0 API/VPN/restore повторно проверены; Telegram/Happ/external SMTP/target performance не оценены | Версия задана владельцем как совпадающая с production; проверялась только собственная Docker-панель | Для оставшихся внешних свойств доказательств нет |
| Native readback VPN UUID — client.uuid; absence имеет три точных поддержанных msg | Numeric client.id, native3.5.0 msg=" (record not found)", native3.7.0 msg="Obtain (record not found)", старый compatibility fixture="record not found"; en-US, write input id=UUID | Другие неподтверждённые форматы fail-closed |
| Duplicate guard остаётся false | Native3.7.0 повторно принимает UUID в разных inbounds и повтор key/subId без смены UUID; общего запрета дубля нет | Uncertain create не повторяется; нужна сверка прежней операции |
| Production, shared legacy panel, publication и remote CI не оценены | За пределами локального мандата | Нет доказательства поведения в этих средах |
| Payments/import/MiniApp/web-admin/password recovery не входят в С01 | Явно отложенные сценарии | Эти пути недоступны в С01 |
| River rescueAfter=3min вместо default1h | Больше worker timeout125s, совпадает с operation lease; штатный механизм River | При увеличении worker timeout порог тоже нужно пересмотреть; иначе возможен повтор активной задачи, физическая блокировка защищает выдачу |

## Отложенные Minor

- Logout уже ограниченного аккаунта возвращает403 до отзыва текущей сессии и
  удаления cookie. Обычный logout проверен; доступ ограниченного аккаунта закрыт.
- Текст регистрации ru/en утверждает отправку письма после202 enqueue,
  когда SMTP delivery ещё не подтверждена. Требуется формулировка о принятии запроса.

## Native Docker-приёмка

Использован только проект `cabinet-s01-local`, собственные volumes/network/state,
без production .env/данных/системного VPN. Панель: **3X-UI3.7.0**, image source
revision `f727d04f6522bb94a8fb52e8352fdcafb51c11e1`; Xray **26.7.28** (`5ca6f4b`);
SMTP: **Mailpit1.31.1**. Native digests закреплены в compose.local.yml.
Перед обновлением собственной3.5.0 сохранены private SQLite/PG backups; текущая
проверка использует обновлённую собственную панель. Production не проверялся.
Повторные проверки не меняли старые bot/legacy аккаунты. Secret/dump files ignored,
0700/0600; private state исключён из Docker build context. TLS verification
сохранён; Chromium доверяет только SPKI собственного сертификата.

На настоящем API найдены и исправлены два расхождения с прежними fixtures:
readback UUID находится в `client.uuid`, numeric `id` не является VPN ID;
absence3.5.0 msg — ` (record not found)`. На3.7.0 точный msg изменился на
`Obtain (record not found)`. Исправлен общий parser и оба GET fixtures,
запросы используют `Accept-Language: en-US`.
Подтверждены UUID/subId/expiry/N+1/15GiB/regular membership; attach сохраняет поля.

Duplicate probes удалены: повтор key с тем же subId принят без смены UUID;
UUID в другом inbound тоже принят. Это наблюдение об указанной версии, не
гарантия уникальности. Guard false сохранён; unknown/timeout не считается absence.
Пробы не являются поведением продуктового provision worker: тот не вызывает delete.

VPN-клиент получает UUID/flow/port из настоящей выданной subscription; заменён
только host на Docker DNS той же панели. Проверен запрос через VLESS/TLS к
своему origin, без third-party трафика или переключения host VPN. Ручной Happ
import не проверен. Mailpit получил настоящее SMTP письмо через TLS/auth, но
принимает только тестовые `@example.test`, внешнюю доставку не доказывает.
Xray26.7.28 по умолчанию блокирует частные IP на финальном исходящем этапе:
native лог подтвердил блокировку собственного Docker origin. Driver задаёт
единственное разрешение `finalRules` для текущего origin `/32`, TCP8000;
остальные native ограничения сохранены. Реальный вызов driver проверен при
отсутствующем правиле и повторно: первый записывает правило, второй не обновляет
настройки; VPN работает. Это настройка test fixture, не изменение backend или
production. Источник: [Xray finalRules](https://xtls.github.io/en/config/outbounds/freedom.html#finalrules).

Restore использовал dump с reserved Grant + provisioning Operation + running River
job после создания клиента. Собственный backend убит, dump восстановлен в новую
пустую БД с теми же encryption keys. Искусственный pause trigger удалён;
`attempted_at` сдвинут на4min только в restored fixture. Только reconcile worker
через штатный River rescue подтвердил прежний target: клиентский DB id, VPN UUID,
subId, expiry/limits не изменились, client/Grant один. После запуска HTTP backend
прежняя owner session получила key, VPN работает. Bounded rollback остановил
только gateway и сохранил PG/панель/VPN.

Setup-ошибки исправлены в driver/config: native updateUser path, `.json` для
Xray config и executable tmpfs для native runtime; ожидание health после restart.
Браузер использует fetch в actual Chromium для того же TLS trust; повторённый
`no-store` от API+gateway нормализован как список директив. Эти ошибки harness
не заявляются RED продуктового кода. Продуктовые RED относятся к native parser;
блокировка Docker origin — отдельное исправление test fixture.

## Настоящий Telegram — 2026-10-01

Владелец подтвердил отдельный тестовый бот без другого обработчика и выполнил
`/start`. Read-only preflight подтвердил private chat разрешённого оператора и
пустой webhook. Из предоставленного `.env` прочитаны только BOT_TOKEN и
ADMIN_TG_ID; token передан через private mode0600 file, root `.env` не монтируется.
Backend и dedicated adapter получили одинаковый allowlist. Используется только
собственный Docker project `cabinet-s01-local`, без legacy/payment обработчиков.

Регистрация/SMTP verification/password/login/request прошли при остановленном
test bot. После запуска карточка доставлена. Настоящий approve записал actor из
Telegram callback, одну Operation и один Grant. Три доставки/обновления карточки
подтверждены Telegram и сохраняют один message ID. Native3.7.0 readback подтвердил
исходные UUID/subId/expiry/limits/membership, VLESS/TLS запрос к собственному
origin прошёл. Credential, email, operator ID и subscription URL не публикуются.

Другой test account получил настоящий reject: Operation/Grant отсутствуют,
карточка обновлена, login сохранён. Public self retry вернул
409/TRIAL_RECONSIDERATION_REQUIRED. Реальные «Пересмотреть» → причина →
«Подтвердить» записали actual operator и причину, создали одну pending-заявку
с previous_request_id; исходный отказ не изменён. Следующий настоящий approve
выдал один Grant; native readback/VPN и обновление той же карточки проверены.

Для real bot-stop создан отдельный control account; observer запущен до доставки
карточки. После настоящего approve бот остановлен до apply. Перед повторным
запуском проверено callback.created_at < Docker State.FinishedAt < granted_at;
actor соответствует allowlist, Operation/Grant по одному, owner key доступен.
Native readback и VLESS/TLS к своему origin прошли при остановленном боте.
После возобновления доставлены накопленные terminal updates той же карточки.

Успех не заявляется по одному observer: его read/login попал в timeout во время
искусственной20s apply-паузы. Отдельная проверка после apply подтвердила итог и
порядок времён. Scoped3s account FOR UPDATE воспроизвёл ожидание session INSERT
и успешный login после освобождения; session FK ссылается на accounts. Причина
timeout — test pause дольше public15s deadline, product code не менялся.
Первый observer начал слишком поздно; два ожидания оператора закончились по
таймауту. В финальном запуске бюджет ожидания увеличен до2h. Это setup/harness
диагностика, не RED продуктового кода; настоящий callback не подменён fixture.

Все временные apply-delay triggers удалены. Test bot после проверки остановлен;
локальная конфигурация возвращена к fixture operator101 и dummy token path,
чтобы `local.py check` сохранял исходные условия. Копия test token удалена,
предоставленный владельцем `.env` не изменён. Backend HTTPS, owner key и VPN
сохранены. Полная приёмка остаётся OPEN.

## Внешние prerequisites и владелец

Владелец предоставил отдельный test bot и operator ID, подтвердил отсутствие
другого обработчика и начал личный чат. Настоящие approve/reject/edit и пересмотр
поддержкой, approve новой заявки и завершение выдачи после остановки бота проверены.
Остаются поддерживаемый Happ для ручного import/connect, внешний SMTP/test mailbox
и целевая машина для benchmark. DNS/сертификаты и
опубликованные policies/support нужны при внешнем тестовом запуске; локальные
terms/privacy пока fixtures. Happ version: pending. Production panel/version и
performance не оценены.

Native3.7.0 duplicate guard: **не подтверждён**, config false. Приёмка С01 в целом
OPEN. Независимое ревью было одно, указан старый диапазон; новых повторных
whole-branch reviews после Docker-findings не проводилось. CI workflow только
проверяет; main/tag Docker Publish не менялся. Нет push/PR/merge/remote CI/release.
