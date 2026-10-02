# С08/С41: локальная приёмка профилей, VPN-ban и месячного reset

**Результат Task 3 драйвера: 32/32 фактические строки PASS.** Root независимо
проверил [postflight](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/postflight-s08.json):
те же images и primary VPN digest, health OK, probe/gate/bot/reconcile
остановлены, UTC и unresolved access operations 0. Карта AC и финальный
обзор ветки остаются у root; это локальное evidence, а не окончательное
закрытие задачи. Все S08 поведенческие
стадии прошли один раз на продуктовой ревизии
`1b90f9f5c7cfe3f1bfcdbcaf9bcaa3ed19a75b1f`. Root сверил реальный
backend image `sha256:f676404a90c39349ee6539133d324fa0a01f64b1018c107591ac6fb853fc2975`,
gateway `sha256:d70e6e26598449623146affebac698fdf2d33b039bf3c09870b661561acc4ccb`
и pinned 3X-UI **3.7.0** в
[runtime manifest](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s08.json).
Сами acceptance-драйверы сверяли собственный Docker project, остановленные
bot/reconcile, отсутствие Telegram operators, подключение и digest исходного
VPN; image IDs сверял root отдельно. Для нового native трафика root запускал
отдельный локальный probe, затем останавливал его:
[regular 224 байта](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/s08-counter-readiness.json),
[EURU 1344 байта](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/s08-euru-counter-readiness.json).
Unmanaged inbound id2 был присоединён только к собственному finite fixture;
primary VPN `localhost:59448` и его конфигурация сохранялись. Никаких
действий с production, Telegram, установленными Happ/MacVPN, trust или
clipboard не было.

Контракт: [спецификация С08/С41](../superpowers/specs/2026-10-02-s08-access-profiles-design.md),
[Task 3 плана](../superpowers/plans/2026-10-02-s07-s08-access-operations.md),
[OpenAPI](../api/openapi.yaml). POST `set_profile {profile,reason}` и
`set_vpn_ban {vpn_banned,reason}` возвращает 202; GET operation — 200.
`monthly_reset` не принимается как HTTP input. `Subscription.access_profile`
и `vpn_banned` отражают подтверждённое состояние отдельно. PASS операции
засчитывался после `applied` и независимого native readback, не по одному 202.

Драйверы [browser.mjs](../../deploy/access-profiles/browser.mjs) и
[local.py](../../deploy/access-profiles/local.py) записали для **каждой** строки
критерий, реальную поверхность, полную дочернюю команду, ожидание, факт,
вердикт и непустой private artifact. Они выводили только коды, счётчики,
булевы признаки и безопасные имена профилей. Два свежих
`s08-UUID@example.test` no-client аккаунта получили credential checkpoint
mode 0600 до регистрации; прежние S07 fixtures, роли и тарифы не
переписывались. Точные локальные адреса: кабинет HTTPS `localhost:58443`,
native panel `localhost:59444`, Mailpit `localhost:59446`, отдельный
root-owned probe `localhost:59449`.

Команды ниже выполнены из корня worktree. Каждая стадия была ограничена
`run-check.mjs --timeout-seconds 420 --lines 8 -- env` и получила
`S08_RUNTIME_MANIFEST=/Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop/.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s08.json`.
Фактическая команда `browser.mjs` в JSONL включает полный manifest path;
`restore` запускался как `python3 deploy/s08/local.py restore` с тем же env.
Этапы одноразовые: повторять их на тех же fixtures нельзя.

| Стадия; дочерняя команда | Факт | Private строки | Bounded log |
| --- | --- | --- | --- |
| `readiness`: `node deploy/s08/browser.mjs readiness` | 3 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/readiness.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-7oKkgk/output.log>) |
| `setup`: `node deploy/s08/browser.mjs setup` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/setup.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-GNrKyk/output.log>) |
| `finite`: `node deploy/s08/browser.mjs finite` | 3 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/finite.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-0q1NO9/output.log>) |
| `unlimited`: `node deploy/s08/browser.mjs unlimited` | 3 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/unlimited.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-E6vAvj/output.log>) |
| `ban`: `node deploy/s08/browser.mjs ban` | 4 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/ban.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-F86x1f/output.log>) |
| `intent-trial`: `node deploy/s08/browser.mjs intent-trial` | 3 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/intent-trial.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-HmLdgL/output.log>) |
| `intent-bonus`: `node deploy/s08/browser.mjs intent-bonus` | 4 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/intent-bonus.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-MtsnFm/output.log>) |
| `guards`: `node deploy/s08/browser.mjs guards` | 3 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/guards.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-HvW3zM/output.log>) |
| `ui`: `node deploy/s08/browser.mjs ui` | 3 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/ui.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-upTz9q/output.log>) |
| `restore`: `python3 deploy/s08/local.py restore` | 4 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/restore.jsonl) | [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-Fw9zmL/output.log>) |

Ниже каждая строка AC указывает исполнявшуюся поверхность и команду,
ожидание, наблюдение и артефакт. Слово **source** обозначает Go test на
контролируемой PostgreSQL/panel fixture, **component** — браузерный test с
подменой HTTP route. Это не физический scheduler tick и не real native
fault injection С08.

| Критерий | Surface; exact stage/source command | Ожидание | Наблюдение; verdict и артефакт |
| --- | --- | --- | --- |
| AC1 | HTTPS operator/client API + 3X-UI 3.7.0; `node deploy/s08/browser.mjs finite` | regular→EURU→regular меняет только managed membership/profile | Два POST202→applied; native EURU→regular, срок, devices/limit, up/down/used, ключ/UUID/subId/server, ban и unmanaged id2 сохранены; replay202 той же операции, changed-body409. **PASS live** — [finite](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/finite.jsonl). |
| AC2 | Hidden current S09 plan + native 3X-UI; `node deploy/s08/browser.mjs unlimited` | Unlimited связывает ровно одну hidden seed revision, expiry0, regular+unlimited, сохраняет счётчики; revoke→EURU starter/reset | POST202→applied с exact plan/revision/devices/traffic; expiry0, managed regular+unlimited, counters unchanged, identity/unmanaged/ban preserved. Revoke POST202→applied дал future expiry, EURU only, counter0 и тот же native identity. **PASS live** — [unlimited](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/unlimited.jsonl). Девять непригодных plan/scheduler/inbound предпосылок проверены **source-only** в [focused Go test](../../backend/internal/platform/access_profiles_test.go) под `go test -race ./... -count=1` — [log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-IRMSGM/output.log>). |
| AC3 | Native counter 1344, VPN-ban и Subscription; `node deploy/s08/browser.mjs ban` | Ban отключает только VPN, reset не снимает ban, unban явный; account/support restrictions и profile независимы | POST202→applied ban: native disabled, Subscription banned, profile/limits/identity/unmanaged/account/support неизменны. Manual reset 1344→0 сохранил ban/disabled. Explicit unban включил native и Subscription active. **PASS live active case** — [ban](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/ban.jsonl). Expired/exhausted unban и expired→unlimited включение — **source-only**, [focused tests](../../backend/internal/platform/access_profiles_test.go) в том же [Go race log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-IRMSGM/output.log>). |
| AC4 | Два новых собственных web-аккаунта, operator API + panel; `node deploy/s08/browser.mjs setup`, `intent-trial`, `intent-bonus` | No-client EURU+ban намерение без выдачи; trial сохраняет оба; banned bonus409, после unban — EURU без trial Grant | Setup создал ровно два no-client без native/Grant. Оба `intent_saved`, Subscription none/EURU/ban. Первый trial дал один Grant и disabled EURU native. Другой account получил bonus409 без записи, затем explicit unban и bonus202→applied с enabled EURU и Grant0. **PASS live** — [setup](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/setup.jsonl), [trial](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/intent-trial.jsonl), [bonus](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/intent-bonus.jsonl). No-client unlimited one-client/no-Grant — **source-only** [Go test](../../backend/internal/platform/access_profiles_test.go). |
| AC5 | Реальный operator API/PG/panel; `node deploy/s08/browser.mjs finite`, `guards`; общий С07 executor и `go test -race ./... -count=1` | Строгие typed/auth/CSRF/Origin, replay/body conflict, new-key no-op без работы панели; сериализация и uncertainty | Live nonoperator403, spoof400, bad CSRF/Origin403, extra field/blank reason/system kind400; no write. New-key `state_unchanged` applied/job0/native unchanged, replay202 same ID, changed-body409. **PASS live guards** — [guards](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/guards.jsonl). Смена роли reconciler и lost reset reply проверены **source** [tests](../../backend/internal/platform/access_profiles_test.go), same-key concurrency — [test](../../backend/internal/platform/access_concurrency_test.go) под [Go race log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-IRMSGM/output.log>); физические lost-reply/restart/partial случаи общего executor уже зафиксированы в [С07 evidence](s07-acceptance.md), на S08 image fault gate не включался. |
| AC6 | Изолированный backend Go/PG/panel и две disposable restore БД; `go test -race ./... -count=1` (cwd `backend`), `python3 deploy/s08/local.py restore` | UTC/Europe-Moscow, grace3600/3601, account/month uniqueness, busy wait, elapsed period unserved, eligibility skip и lost reset без blind retry | Все focused [monthly tests](../../backend/internal/platform/monthly_reset_test.go) входят в полный [Go race run: 6 tested + 2 no-test](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-IRMSGM/output.log>). В первой disposable DB synthetic month вставлен один раз; во второй после pipe restore строка/все digests сохранились, duplicate insert не прошёл. **PASS source + disposable uniqueness** — [restore](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/restore.jsonl). Тест с двумя `Service` на общей PG моделирует restart/два экземпляра; два физических server process и live monthly tick не запускались, часы macOS не менялись. |
| AC7 | Реальные Chromium/operator и client375px RU/EN; `node deploy/s08/browser.mjs guards`, `ui`; component `npm run test:e2e` (cwd `web`) | Раздельный confirmed profile/ban, keyboard/labels/fit, предупреждения и confirmation; pending/error/retry | EN и RU: реальный Tab operation→profile→reason, содержательные предупреждения profile/ban/reset и confirmation, ширина помещается; client показывает EURU и VPN allowed отдельно. Live guards дали ожидаемые коды. **PASS live UI/API** — [ui](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/ui.jsonl), [guards](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/guards.jsonl). Pending/error/retry, skipped month и сохранение reason — **component route doubles**, [S08 tests](../../web/tests/access-profiles.spec.ts) 11/11 в [full web 105/105 log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-2wNUhv/output.log>); live UI fault не инжектировался. |
| AC8 | Own Docker HTTPS/pinned 3X-UI, PostgreSQL disposable restore; `node deploy/s08/browser.mjs readiness` … `ui`, `python3 deploy/s08/local.py restore` | Exact native/API/browser behavior, identities/counters retained, backup/restore profile/ban/operations/audit/monthly period and auth cleanup; runtime preserved | Все 32 driver rows PASS, live panel/API/browser checks above. Live DB→first fresh disposable DB сохранила 10 own accounts, 23 access operations и 43 access audit events, monthly periods было **0**. Только в disposable DB создан synthetic `2099-01`: unique row1; после repeated auth cleanup sessions/live proofs/ciphertext0 и те же product digests; второй disposable pipe restore сохранил period row/digests/uniqueness. Root independently confirmed unchanged images/primary VPN/health and stopped probe/gate/bot/reconcile, unresolved access 0. **PASS local + postflight** — [restore](../../.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/restore.jsonl), [runtime](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s08.json), [postflight](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/postflight-s08.json), [source checks](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/s08-final-source-checks.json). |

Source regression на frozen product: root подтвердил
`go test -race ./... -count=1` (cwd `backend`, exit0, 237.526s,
6 tested + 2 no-test packages), `go vet ./...` (exit0) и
`npm run test:e2e` (cwd `web`, 105/105; frontend source не изменился после
запуска). Полные команды, длительности и логи сохранены в
[root source-checks](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/s08-final-source-checks.json).
В этой приёмке не запускались live monthly scheduler tick, два отдельных
server process, S08 fault gate или трафик до 1 GiB для exhausted case;
соответствующие доказательства выше обозначены source/component, а не
native PASS. Root прочитал все 32 фактические строки, сопоставил восемь AC с source/native/UI/restore доказательствами и подтвердил postflight. C05 разрешён; локальная приёмка С08/необходимого С41 закрыта. Общий финальный обзор ветки остаётся впереди вместе с проверкой [semantic naming](../architecture/application-naming.md).
