# С08/С41 — подготовка локальной приёмки

**PENDING: поведенческие драйверы не запускались.** Основание:
[спецификация](../superpowers/specs/2026-10-02-s08-access-profiles-design.md),
[Task 3 плана](../superpowers/plans/2026-10-02-s07-s08-access-operations.md)
и текущий [OpenAPI](../api/openapi.yaml). Точный runtime contract
будет повторно сверен после source freeze: backend С08/С41 ещё разрабатывается.
Источник S07 принят локально на `4042885`; его исторические JSONL и
контролируемые fixtures не переписываются. Здесь не заявлено ни одного
нового live PASS.

Целевая поверхность — только собственный Docker project `cabinet-s01-local`:
HTTPS `localhost:58443`, native readback 3X-UI3.7.0 `localhost:59444`,
Mailpit `localhost:59446`, собственная disposable PostgreSQL restore БД.
Primary VPN `localhost:59448` и его конфигурация должны остаться неизменными;
локальный отдельный probe `localhost:59449` и fault gate включает только root.
Telegram, production, установленные Happ/MacVPN, trust и clipboard исключены.

Планируемые команды ниже исполняются **только после** нового root-reviewed
runtime manifest и отдельного разрешения каждого bounded stage. Реальный
driver запишет для каждой строки критерий, поверхность, exact command,
ожидание, факт, verdict и ссылку на private artifact. В строках PENDING
ссылка ведёт к [подготовке драйвера](../../deploy/s08/README.md), а не к
несуществующему результату. Пароли, cookies, subscription links и raw
panel responses в публичный evidence не попадут. С41 проверяется
изолированными Go tests с переданным clock/timezone: системные часы macOS
не меняются, live scheduler PASS из source test не выводится.

`guards` планирует live HTTP-проверки роли, Origin/CSRF, typed body,
idempotency и no-op. Конкурентные profile/ban/compensation/provision,
частичный attach/detach/re-enable и S41 lost reply требуют отдельных
исполненных Go-тестов с их логами. `ui` проверит реальные элементы и
клавиатуру; pending/error/retry с подменой API останутся component-тестами.
Ни source-тест, ни component-тест не будут названы live fault/scheduler
исполнением.

| Критерий | Planned exact stage/target | Ожидание | Факт | Verdict; artifact |
| --- | --- | --- | --- | --- |
| AC1 | `node deploy/s08/browser.mjs finite`; own operator API + native panel | regular→euru→regular меняет только managed memberships/profile; identity, expiry, devices, limit, used traffic, ban и unmanaged membership сохранены | Не запускалось | **PENDING**; [readiness](../../deploy/s08/README.md) |
| AC2 | `node deploy/s08/browser.mjs unlimited`; hidden current С09 revision + native panel | expiry0, inherited regular+unlimited, revision/terms exact, counters unchanged on grant; revoke → selected starter trial/reset, ban retained; absent/ambiguous plan/device/scheduler guards | Не запускалось | **PENDING**; [readiness](../../deploy/s08/README.md) |
| AC3 | `node deploy/s08/browser.mjs ban`; own native counters, ban and subscription | ban→manual reset→ban remains; unban expired/exhausted remains not active; account/support restrictions separate | Не запускалось | **PENDING**; [readiness](../../deploy/s08/README.md) |
| AC4 | `node deploy/s08/browser.mjs intent-trial` and `intent-bonus`; two own fresh no-client web accounts | Profile+ban intent saved without client/grant; first trial consumes profile+ban and remains disabled. Banned no-client compensation409; after explicit unban bonus consumes preserved profile without first trial Grant. Unknown/empty groups source-negative | Не запускалось | **PENDING**; [readiness](../../deploy/s08/README.md) |
| AC5 | `node deploy/s08/browser.mjs guards`; own operator API; separate Go race source test | strict kind/body/reason, live role/CSRF/Origin, same-key replay/body409, new-key no-op state_unchanged without River/native write; competing writes serialized | Не запускалось | **PENDING**; [readiness](../../deploy/s08/README.md) |
| AC6 | isolated `go test -race ./internal/s01 -run 'TestMonthly' -count=1` in `backend` (final pattern after source freeze) | UTC/Europe-Moscow month edges, 3600/3601s grace, unique period across restart/processes, busy wait or explicit unserved, eligibility skip, ambiguous lost reply no blind reset | Не запускалось; exact final test pattern ждёт source | **PENDING**; [readiness](../../deploy/s08/README.md) |
| AC7 | `node deploy/s08/browser.mjs ui`; own Chromium RU/EN375px and real keyboard Tab, client С03 | confirmed profile и ban показаны отдельно; pending/error/retry and destructive confirmation; live operator guards | Не запускалось | **PENDING**; [readiness](../../deploy/s08/README.md) |
| AC8 | `S08_RUNTIME_MANIFEST=<root manifest> python3 deploy/s08/local.py restore`; two own disposable DBs, native3.7.0 from other stages | profiles/ban/access audit survive backup/restore; synthetic month row inserted only in first disposable DB survives second pipe restore with uniqueness intact; repeated auth cleanup zero; root confirms images/primary VPN/postflight | Не запускалось | **PENDING**; [readiness](../../deploy/s08/README.md) |

Перед первым mutation root должен подтвердить точные native tags/IDs и
выбранные аккаунты. По миграции ранее назначенные С07 аккаунты могут
иметь NULL в поле profile. Driver принимает regular лишь при совпадении
последнего **applied** С07 target, Subscription и точного native managed
membership; из одного внешнего вида клиента regular не выводится.
Пригодные С07 fixtures будут переиспользованы
только после read-only preflight. Если таких нет, достаточно одного
контролируемого finite аккаунта и двух fresh `s08-UUID@example.test`
no-client аккаунтов для независимых trial/bonus путей; оператор С07
переиспользуется. Credentials/fixture metadata фиксируются в приватном
файле атомарно **до** чувствительных native/API шагов. Никакие старые
fixtures или чужие роли не удаляются и не перезаписываются.

Требуется root-owned fixture решение для сохранения unmanaged membership:
существующий `local-probe-vless` (id2) можно связать только с выбранным
собственным client, не меняя inbound. Для exhausted case 1GiB traffic
не генерируется: boundary остаётся изолированным source test. Negative
ambiguous plan/empty/retag/scheduler guard также source test, если root не
выдаст отдельный безопасный fixture packet. Root подтверждает ровно один
текущий hidden unlimited plan и валидный С41 scheduler prerequisite;
driver не заменяет уже существующий план. Проверки fault/probe не
запускаются до отдельного gate packet.

Live `ban` stage использует finite client с действующим сроком. Unban при
истёкшем или исчерпанном лимите, а также enable при переводе expired→unlimited
будут отмечены как **source-only**, если root не даст отдельный controlled
native fixture. Эти границы нельзя выводить из успешного active-case.
