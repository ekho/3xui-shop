# С07: локальная приёмка операций доступа

**Статус: С07 локально принят, AC1–8 проверены root.** Полный source regression
и свежий review всей ветки выполняются после С08 как отдельный Task4.
Исходная продуктовая ревизия `432137dd201fa58920f866da5f80303db685441d`,
native update fix `0bf420e`, исправление активации истёкшего доступа
`1f1ba8692f01ac77e9064a64761873d503b325ab`. Текущий собственный Docker
[runtime manifest](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json)
указывает backend `sha256:291ac7ef…`, gateway `sha256:5d2864d…`, pinned
3X-UI 3.7.0; первичный VPN `localhost:59448` и его config hash не менялись.

История не переписана: первый `setup` дал 3 PASS/1 BLOCKED на дополнительном
readback после создания probe-конфига; его низкоуровневая причина не установлена.
Root восстановил **те же** восемь
контролируемых аккаунтов без новой регистрации/триалов/тарифов. Первый
`normal` дал 2 PASS/1 BLOCKED: native 3X-UI3.7.0 возвращал ClientRecord,
несовместимый с update payload; bridge и backend-адаптер исправлены. Первый
`ui` дал 2 PASS/1 BLOCKED, потому что RU selector драйвера искал
«Операция» вместо фактического «Действие». Исторические
[setup.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/setup.jsonl),
[normal.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/normal.jsonl) и
[ui.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/ui.jsonl)
сохранены; широкие этапы не повторялись.

Последующие focused этапы подтвердили native фикстуры, компенсации,
назначение, reset, guards, RU UI и все управляемые fault/reconcile пути.
В первой проверке компенсации истёкшему клиенту драйвер проверял срок, но
пропустил `enabled` и статус подписки: read-only
[diagnostic.json](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/diagnostic.json)
обнаружил будущий срок при native `enabled=false` и `disabled` подписке.
На ревизии `1f1ba86` тот же клиент был контролируемо возвращён в истёкшее
состояние; `expired-recovery` подтвердил +1 день от now, native enabled,
подписку active и один эффект. Реальный порядок Tab в EN/RU проверен отдельно,
backup/restore и повторная очистка авторизации прошли на собственной
disposable PostgreSQL БД. Fault/assignment/reset evidence получены на
`0bf420e`; последующий `1f1ba86` изменил восстановление истёкшего доступа
и subscription readback, а gateway/panel не менялись. Исправленные пути
проверены отдельно на конечном image. Контрактные ожидания взяты из
[спецификации](../superpowers/specs/2026-10-02-s07-subscription-operations-design.md),
[плана](../superpowers/plans/2026-10-02-s07-s08-access-operations.md),
[OpenAPI](../api/openapi.yaml) и указанной продуктовой ревизии.

Реальная поверхность ограничена собственным Docker project
`cabinet-s01-local`, HTTPS `localhost:58443`, Mailpit `localhost:59446`,
3X-UI 3.7.0 через независимый readback `localhost:59444`, отдельным
root-owned VPN-пробником `localhost:59449` и собственной disposable PostgreSQL
restore БД. Базовый VPN `localhost:59448` и его конфигурация сохраняются.
Пробный трафик идёт только к локальному origin. Telegram, production,
Happ, установленный MacVPN, trust/clipboard и внешняя публикация исключены.

Источник контрактных кодов: POST operation **202** с типизированным
`AccessOperation`, GET **200**, reconcile **202** с обязательными `reason`
и `acknowledge_reset_cost`. Виды: `compensate`, `assign_plan`,
`starter_trial`, `reset_traffic`. GET subscription добавляет nullable
`access_operation_id/status`. Ответ202 означает постановку работы; PASS
требует подтверждённого состояния `applied` и native readback. Потерянный
ответ POST reset оставляет `needs_review` даже при нулевом native readback:
после него мог накопиться новый трафик. False-ack reconcile может вернуть202
и снова `needs_review` без второго reset; только явный true-ack разрешает
повтор. Эти переходы подтверждены native readback и matched fault proofs root.

Драйверы [browser.mjs](../../deploy/s07/browser.mjs) и
[local.py](../../deploy/s07/local.py) сохраняют criterion, target, exact command,
expected, actual, verdict и непустую ссылку на private JSONL artifact.
PASS создан только для реально пройденных шагов. Пароли,
cookies, subscription links, raw panel replies и личные данные в публичный
evidence не попадают. Приватные собственные файлы располагаются в
`.superpowers/sdd/2026-10-02-s07-access-operations/e2e/` (mode600);
[запрос инфраструктуры](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/infrastructure-request.json)
фиксирует точные переключения gateway и probe, которые выполняет root.

Ниже фактические bounded команды для worktree
`/Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop`. `setup` и
исходные `normal`/`ui` исторические; их повторять нельзя. Команды
`fault-*` выполнялись с тем же `run-check` и отдельными root-controlled
exact-path gate/probe доказательствами. Файл runtime manifest на этапах
менялся только после root-owned backend rollout; gateway и panel images
оставались теми же.

```text
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs setup
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs normal
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs fixtures
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs compensation
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs assignment
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs reset
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs guards
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs ui
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs recovery-expire
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs expired-recovery
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs ui-ru
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs ui-keyboard
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs fault-unavailable
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json S07_FAULT_PROOF=.superpowers/sdd/2026-10-02-s07-s08-access-operations/infrastructure/gate-current.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs fault-lost-reply
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json S07_RESTART_PROOF=.superpowers/sdd/2026-10-02-s07-s08-access-operations/infrastructure/backend-restart-proof.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs fault-restart-check
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs fault-noack
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs fault-ack
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs fault-partial-start
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs fault-partial-reconcile
node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 120 --lines 12 -- python3 -c 'import json,subprocess,sys; r=subprocess.run([sys.executable,"deploy/s07/local.py"],input=json.dumps({"action":"restore"}),text=True,capture_output=True,check=True,timeout=110); print(r.stdout,end="")'
```

Для fault этапов root включает ровно один exact method/path gate по
private descriptor, запускает ту же bounded `node deploy/s07/browser.mjs
<stage>` команду и возвращает gate в off. Порядок:
`fault-unavailable`, `fault-lost-reply`,
`fault-restart-check`, `fault-noack`, `fault-ack`,
`fault-partial-start`, `fault-partial-reconcile`.
После `fault-lost-reply` root выключает gate, направляет новый
контролируемый трафик через probe, затем перезапускает только backend и
передаёт restart proof. Перед `fault-partial-reconcile` root сравнивает
сохранённый target с native readback: недостающий detach ещё не выполнен.
RequeueAccess сбрасывает число попыток; worker сам завершает membership и
readback без прямого исправления панели root.
Сам driver не активирует gateway/probe и не меняет чужие inbounds.

Каждая строка private JSONL содержит критерий, реальную поверхность,
команду child process, ожидаемое и фактическое состояние, verdict и
непустую ссылку на artifact; полный bounded argv приведён выше.
Сумма исторических и focused строк: **47 PASS, 3 BLOCKED**. BLOCKED
остались только в исходных `setup`/`normal`/`ui`; соответствующие
продолжения доказаны отдельными строками, записи не перезаписывались.

| Stage | Строки | Evidence | Bounded output |
| --- | --- | --- | --- |
| `setup` | 3 PASS / 1 BLOCKED | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/setup.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-drovua/output.log>) |
| `normal` | 2 PASS / 1 BLOCKED | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/normal.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-dQIRwX/output.log>) |
| `fixtures` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fixtures.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-pqoLDo/output.log>) |
| `compensation` | 5 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/compensation.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-rU78ts/output.log>) |
| `assignment` | 3 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/assignment.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-NU04EJ/output.log>) |
| `reset` | 3 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/reset.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-NB6YsL/output.log>) |
| `guards` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/guards.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-sKGdwY/output.log>) |
| `ui` | 2 PASS / 1 BLOCKED | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/ui.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-yGUbwe/output.log>) |
| `ui-ru` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/ui-ru.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-xn1MS9/output.log>) |
| `fault-unavailable` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-unavailable.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-4Iv7MJ/output.log>) |
| `fault-lost-reply` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-lost-reply.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-5CGRPi/output.log>) |
| `fault-restart-check` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-restart-check.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-9RgruL/output.log>) |
| `fault-noack` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-noack.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-eVYcxm/output.log>) |
| `fault-ack` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-ack.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-fLEpFl/output.log>) |
| `fault-partial-start` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-partial-start.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-mR1dUw/output.log>) |
| `fault-partial-reconcile` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-partial-reconcile.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-6WFF1u/output.log>) |
| `recovery-expire` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/recovery-expire.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-9yjuoS/output.log>) |
| `expired-recovery` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/expired-recovery.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-C3ZM1t/output.log>) |
| `ui-keyboard` | 3 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/ui-keyboard.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-z3t27c/output.log>) |
| `restore` | 2 PASS | [JSONL](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/restore.jsonl) | [bounded log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-P5sNq1/output.log>) |

Независимые root-owned matched fault proofs: [before GET, 2×502](../../.superpowers/sdd/2026-10-02-s07-s08-access-operations/infrastructure/gate-proof-before-1790950911080359000.json),
[after POST reset, 1×502](../../.superpowers/sdd/2026-10-02-s07-s08-access-operations/infrastructure/gate-proof-after-1790951053535049000.json),
[before POST detach, 1×502](../../.superpowers/sdd/2026-10-02-s07-s08-access-operations/infrastructure/gate-proof-before-1790951322511927000.json).
[Restart proof](../../.superpowers/sdd/2026-10-02-s07-s08-access-operations/infrastructure/backend-restart-proof.json)
фиксирует 1344 bytes нового native трафика до и после рестарта того же
backend image. [Postflight](../../.superpowers/sdd/2026-10-02-s07-s08-access-operations/infrastructure/postflight-s07.json)
фиксирует health, совпадение images с manifest, неизменность primary VPN,
gate off, probe stopped, bot/reconcile stopped.

| Критерий | Real target; exact command/stage | Ожидание | Факт сейчас | Verdict; артефакт подготовки |
| --- | --- | --- | --- | --- |
| AC1 | HTTPS operator API + 3X-UI3.7.0; `browser.mjs fixtures`, `compensation`, `recovery-expire`, `expired-recovery` | Активному finite +2 дня, истёкшему от now; native enabled и подписка active после восстановления, identity/limits/counters сохранены | Active +172800000ms exact; первый expired +now1d сохранил disabled (исторический gap). После fix тот же клиент controlled expired/disabled → POST202/replay202/body409, +now1d, enabled=true, subscription active, identity/limits/counters unchanged, one op/audit1/1 | **PASS после fix**; [compensation.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/compensation.jsonl), [diagnostic.json](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/diagnostic.json), [recovery-expire.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/recovery-expire.jsonl), [expired-recovery.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/expired-recovery.jsonl) |
| AC2 | Own panel/PG и exact-key upstream read fault; `browser.mjs setup`, `fixtures`, `compensation`, `fault-unavailable` | Unlimited, perpetual, VPN-banned, unavailable отклонены; missing bonus без первого trial grant/cap | 0/366→400, perpetual/banned/unlimited→409 без writes; bonus202/replay202 один client без grant/cap; upstream GET fault→409/0 writes/native unchanged | **PASS**; [compensation.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/compensation.jsonl), [fault-unavailable.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-unavailable.jsonl) |
| AC3 | Hidden regular/EURU catalogue revisions, native panel, PG; `browser.mjs assignment`, `fault-partial-start`, `fault-partial-reconcile` | Текущая revision/period принимается, stale409; immutable snapshot, assignment/starter reset с сохранением identity | Hidden regular assign202/edit200/stale409, target applied, лимит5GiB/reset0; starter grant1/identity same; EURU partial needs_review→same-op reconcile applied/native target | **PASS**; [assignment.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/assignment.jsonl), [fault-partial-start.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-partial-start.jsonl), [fault-partial-reconcile.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-partial-reconcile.jsonl) |
| AC4 | Native nonzero counters, true VPN ban, root exact fault; `browser.mjs reset`, `fault-lost-reply`, `fault-restart-check`, `fault-noack`, `fault-ack` | Manual reset обнуляет счётчики и сохраняет ban/limits/expiry; lost reply needs_review без слепого retry | Banned native reset счётчик→0/ban retained; masked success reply→needs_review/audit1/0; новый трафик пережил restart и no-ack; true-ack завершил same op/счётчик→0 | **PASS**; [reset.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/reset.jsonl), [fault-lost-reply.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-lost-reply.jsonl), [fault-restart-check.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-restart-check.jsonl), [fault-ack.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-ack.jsonl) |
| AC5 | HTTPS operator API, River/PG, restart; `browser.mjs compensation`, `fault-lost-reply`, `fault-restart-check`, `fault-partial-start`; `go test -race ./internal/s01 -run '^TestAccessConcurrentOwnership$' -count=1` (cwd `backend`) | Same-key replay один эффект, changed-body409, conflicting409, lost reply/restart без дублей; concurrent workers и trial/bonus race | Live replay202/same op/conflict409/audit1/1; masked success needs_review после restart при новых 1344 bytes; partial blocks conflict409. Отдельный Go race test на source commit `fdd1ef8` прошёл за9.473s: concurrent same key, two workers one native fixture effect, trial-vs-bonus account ownership. Реальную гонку двух worker на 3X-UI не инжектировали | **PASS: live + source test, с указанной границей**; [compensation.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/compensation.jsonl), [fault-restart-check.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-restart-check.jsonl), [source test](../../backend/internal/s01/access_concurrency_test.go), [bounded test log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-aSBmqY/output.log>) |
| AC6 | Same operation and audit under partial fault; `browser.mjs fault-noack`, `fault-ack`, `fault-partial-start`, `fault-partial-reconcile` | Несовпавший panel readback блокирует вторую запись; сверка без reset-cost бережёт новый трафик, с явным согласием завершает ту же операцию; partial membership не теряет шаги | No-ack202 оставил needs_review/новый счётчик, true-ack202 applied same op; EURU partial mismatch/conflict409→reconcile202 applied exact target без ручного native repair | **PASS**; [fault-noack.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-noack.jsonl), [fault-ack.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-ack.jsonl), [fault-partial-start.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-partial-start.jsonl), [fault-partial-reconcile.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/fault-partial-reconcile.jsonl) |
| AC7 | HTTPS session/CSRF/Origin, RU/EN Chromium375px, S03 subscription; `browser.mjs reset`, `guards`, `ui-ru`, `ui-keyboard`; `npm --prefix web run test:e2e` | Role/CSRF/input/foreign guards; обе локали keyboard/focus/mobile; pending/error/retry и текущая подписка | Live guards8 кодов/no native write; S03 latest applied PASS; EN/RU UI visible/focused/fit, real Tab select→days→reason обе локали. Component route-double S07 tests12/12 и full web94/94 покрыли pending/error/retry; live UI fault injection не выполнялся, исходный RU selector BLOCKED сохранён | **PASS: live HTTP/keyboard + component tests с указанной границей**; [guards.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/guards.jsonl), [ui-keyboard.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/ui-keyboard.jsonl), [S07 tests](../../web/tests/s07.spec.ts), [focused log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-cWvDtp/output.log>), [full web log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-xnt9AK/output.log>), [historic UI](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/ui.jsonl) |
| AC8 | Own Docker 3X-UI/API/browser/PG disposable restore; `browser.mjs setup`, focused continuations, `ui-ru`, fault stages; bounded `python3 -c` bridge restore | Identity/counter/readback, regression и backup/restore access audit/catalogue плюс auth cleanup | Live browser/API/native rows, root matched fault proofs, own DB restore equal operations9/applied9/audit requested/completed9/9; repeated auth cleanup zero and access/catalogue unchanged; postflight health/images/VPN/gate off confirmed. Full web regression94/94 on unchanged frontend source; final backend full-suite regression proof belongs to root ledger | **PASS local actual/restore/web regression; backend full regression separately root-audited**; [restore.jsonl](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/restore.jsonl), [postflight](../../.superpowers/sdd/2026-10-02-s07-s08-access-operations/infrastructure/postflight-s07.json), [full web log](</var/folders/4v/scwck4ld5nz14y5_5g196phw0000gn/T/tradeos-check-xnt9AK/output.log>) |

Границы доказательств: гонки двух workers и initial trial/bonus проверены
Go race тестом на контролируемых PostgreSQL/panel doubles, а не live
инъекцией двух workers в 3X-UI. Pending/error/retry UI проверены
Playwright component route doubles; реальные HTTP guard, RU/EN375px и
порядок Tab проверены отдельно. Full backend regression после тестового
коммита `fdd1ef8` не приписывается этому локальному драйверу; его сводит
root. Production и внешние каналы не затрагивались.
