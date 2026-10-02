# С07: локальная приёмка операций доступа — подготовлено, не выполнялось

**Статус PENDING.** Новый backend/gateway image ещё не подтверждён root.
Ни один С07 behavioral driver не запускался против прежнего runtime. Текущие
ожидания взяты из [спецификации](../superpowers/specs/2026-10-02-s07-subscription-operations-design.md),
[плана](../superpowers/plans/2026-10-02-s07-s08-access-operations.md),
[OpenAPI](../api/openapi.yaml) и незавершённого source S07; перед запуском
driver/source contract будет сверен с финальной продуктовой ревизией.

Реальная будущая поверхность ограничена собственным Docker project
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
повтор. При окончательной сверке source это ожидание будет проверено заново.

Драйверы [browser.mjs](../../deploy/s07/browser.mjs) и
[local.py](../../deploy/s07/local.py) сейчас проходят только статическую
проверку синтаксиса. Они сохраняют criterion, target, exact command,
expected, actual, verdict и непустую ссылку на private JSONL artifact.
До реального прогона запись со статусом PASS не создаётся. Пароли,
cookies, subscription links, raw panel replies и личные данные в публичный
evidence не попадают. Приватные собственные файлы располагаются в
`.superpowers/sdd/2026-10-02-s07-access-operations/e2e/` (mode600);
[запрос инфраструктуры](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/infrastructure-request.json)
фиксирует точные переключения gateway и probe, которые выполняет root.

Следующие команды приведены для корня worktree
`/Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop`. Root должен
передать фактический путь к runtime manifest; здесь ожидается
`.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json`.
`S07_RESTART_PROOF` и `S07_FAULT_PROOF` — пути к частным root-owned
артефактам restart/fault-hit, без секретов в командной строке.

```text
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs setup
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs normal
S07_RUNTIME_MANIFEST=.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s07.json node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --cwd /Users/ekho/.codex/worktrees/web-trial-s01/3xui-shop --timeout-seconds 420 --lines 12 -- node deploy/s07/browser.mjs ui
printf '{"action":"restore"}' | python3 deploy/s07/local.py
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

| Критерий | Real target; exact command/stage | Ожидание | Факт сейчас | Verdict; артефакт подготовки |
| --- | --- | --- | --- | --- |
| AC1 | HTTPS operator API + 3X-UI3.7.0; `browser.mjs normal` | Активному finite прибавлены ровно2 дня от прежнего expiry, истёкшему —1 день от now; счётчики, limits, profile и identity прежние; 0/366 отклонены | Не запускалось | **PENDING**; [normal driver](../../deploy/s07/browser.mjs) |
| AC2 | Own panel/PG и exact-key upstream read fault; `browser.mjs setup`, `normal`, `fault-unavailable` | Unlimited, perpetual, VPN-banned, unavailable не пишут операцию/панель; missing bonus создаёт один native client без первого trial grant и cap | Не запускалось | **PENDING**; [bridge](../../deploy/s07/local.py), [fault request](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/infrastructure-request.json) |
| AC3 | Hidden regular/EURU catalogue revisions, native panel, PG; `browser.mjs normal`, `fault-partial-start`, `fault-partial-reconcile` | Текущая revision/period принимается, stale409; snapshot остаётся прежним после edit; assignment/starter начинают новый срок/reset, UUID/subId/server постоянны | Не запускалось | **PENDING**; [driver](../../deploy/s07/browser.mjs) |
| AC4 | Native nonzero counters, true VPN ban, root exact fault; `browser.mjs normal`, `fault-lost-reply`, `fault-restart-check`, `fault-noack`, `fault-ack` | Manual reset обнуляет счётчики и сохраняет ban/limits/expiry; потерянный ответ успешного reset оставляет needs_review даже при нулевом readback и не повторяет reset без явного согласия | Не запускалось | **PENDING**; [driver](../../deploy/s07/browser.mjs), [fault request](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/infrastructure-request.json) |
| AC5 | HTTPS operator API, River/PG, restart; `browser.mjs normal`, `fault-lost-reply`, `fault-restart-check`, `fault-partial-start` | Same-key replay не создаёт второй эффект, changed-body409, конфликтующая операция409, lost upstream reply и restart не дублируют reset/дни | Не запускалось | **PENDING**; [driver](../../deploy/s07/browser.mjs) |
| AC6 | Same operation and audit under partial fault; `browser.mjs fault-noack`, `fault-ack`, `fault-partial-start`, `fault-partial-reconcile` | Несовпавший panel readback блокирует вторую запись; сверка без reset-cost бережёт новый трафик, с явным согласием завершает ту же операцию; partial membership не теряет шаги | Не запускалось | **PENDING**; [driver](../../deploy/s07/browser.mjs) |
| AC7 | HTTPS session/CSRF/Origin, RU/EN Chromium375px, S03 subscription; `browser.mjs normal`, `ui` | Nonoperator/revoked/forged/foreign/invalid отказы без изменения; UI keyboard/focus/mobile и статусы; текущая подписка отражает последний applied target | Не запускалось | **PENDING**; [driver](../../deploy/s07/browser.mjs), [web tests](../../web/tests/s07.spec.ts) |
| AC8 | Own Docker 3X-UI/API/browser/PG disposable restore; `browser.mjs setup`, `normal`, `ui`, fault stages; `python3 deploy/s07/local.py restore` | Identity/counter/readback, regression и backup/restore access audit/catalogue плюс auth cleanup подтверждены на frozen source/image | Не запускалось | **PENDING**; [bridge](../../deploy/s07/local.py), [runtime request](../../.superpowers/sdd/2026-10-02-s07-access-operations/e2e/infrastructure-request.json) |

После actual run таблица заменяется фактическими кодами, счётчиками,
дигестами, source/image ревизией, bounded log links и private JSONL rows.
Ограничения отдельно фиксируются без выдуманного PASS. В частности,
реальный concurrent worker/initial-trial race и loss-window до PostgreSQL
lease могут потребовать focused Go race evidence сверх управляемого native
сценария; их нельзя выводить из одного replay.
