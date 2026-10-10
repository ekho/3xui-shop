# С40 — локальная приёмка сверки групп

Scope #43, owner `vpn`. Base `a6ef9bf3dab4407dcf664dda04c93619f549deb8` (`v2`).
Contracts `2026-10-05-modular-monolith-v1`, `2026-10-09-s39-server-pool-v1`.
Migration39 reservation: [canonical record](https://github.com/ekho/3xui-shop/issues/43#issuecomment-6092044105).

## Подтверждённые сценарии

- Enabled tags дают regular/euru/unlimited; unlimited включает regular.
  Disabled known inbound удаляется из membership, absent current ID не передаётся
  в detach, неизвестные tags сохраняются. Неполный provider row отклоняется.
- Existing AccessWorker исполняет одну зафиксированную operation; повтор scan и
  worker не создаёт другой client/job/target. Частичный detach и retag/delete
  system target требуют fresh read; заменяется только group work, original target
  сохраняется. Unresolved purchase/reset/trial блокируют подготовку group work.
- limitIP=1, expiry/quota/traffic, manual disabled и assignment/UUID/subID/key
  сохраняются. Ban использует только disable, включая empty desired membership.
  Add/delete/reset/enable/remap в group operation отсутствуют.
- Paid renewal target после retag/delete отклоняется до write; money/quote/receipt
  и target неизменны. Simultaneous paid worker + scan сохраняют единственного owner.
- Ошибки дают system audit и durable ru/en Telegram alert через existing outbox.
  Одинаковая ошибка ограничена одним alert на account/code/UTC day. Current
  infrastructure+operator grant/binding/version/source проверяются до send.
- HTTP recovery403 без infrastructure grant; grant даёт202; revoke перед worker
  блокирует запись. Recovery сохраняет target и не повторяет traffic reset.
  `group_reconcile` есть только в output enum, API creation этого kind даёт400.
- Migration Down/Up с ordinary delivery сохраняет её; group history запрещает
  downgrade. SQL payload guard отказывает incomplete/arbitrary alerts.
- Independent review обнаружило два integration дефекта: aligned membership
  после удаления inbound/manual recovery не обновлял confirmed baseline;
  ban-only target ошибочно читался как membership drift. Regression tests
  воспроизвели оба до исправлений. Fresh read-only confirmation сохраняет старые
  targets и URL; ban-only читатели проверяют identity/limits/disable, а unban
  требует непустого enabled profile. Статистика считает подтверждённый ban inactive.

## Проверки

Owned loopback PostgreSQL18/Redis8 используют random host ports; каждый DB test
создаёт свою `platform_test_*` database. TLS panel/SMTP и Bot API синтетические.
URL/credential files находятся за пределами repository, permissions0600.

Focused Go `TestGroup*`, migration guard и HTTP recovery: PASS с `-race`.
Fresh `cmd/server` SHA256:
`58a2d85a2e393457bd73c0bf527e7f8eb52fcf877edb68ff6d277debc86cdfe8`.
Native test запускает настоящий HTTP/River/Telegram process пять раз: initial
trial, panel outage/guarded alerts, recovery, no-op restart, ban. Он подтверждает
original grant/target/key, physical limitIP1, traffic42 и unknown inbound91,
а также HTTP banned/non-stale при empty enabled profile.
Provider transport симулируется; native Go consumers и jobs не подменяются.

Python connected suite: **112 passed**. Web E2E: **551 passed**, существующие
ru/en, keyboard/error/empty-state flows. Typecheck/build/runtime-config и vet PASS.
Полный Go race/browser-consumer прогон на исходниках до review fixes не прошёл:
HTTP package достиг локального cutoff25m во время текущего теста длительностью7s
(stack: OpenAPI JSON decode); backup test потребовал стандартный Compose endpoint.
Остальные packages PASS. Это не доказательство полного Go PASS или зависания.
Согласованный restore consumer берётся из доставленной #44 через public
`TEST_POSTGRES_COMPOSE_FILE`, контракт `2026-10-10-owned-postgres-restore-v1`
[owner #45](https://github.com/ekho/3xui-shop/issues/45#issuecomment-6091389100).
Нового общего fixture adapter в #43 нет. Итоговый полный прогон использует
fixture с явным project name, random loopback ports и cutoff60m как в CI.
Независимое whole-branch review и follow-up review исправлений PASS: оба Important
закрыты, новых actionable findings нет. Повторный focused Go `TestGroup*` и
statistics PASS с `-race`; fresh native binary PASS. Exact-head CI/merge
фиксируются после результата, ранее полученные PASS не заменяют их.

## Целевой настоящий panel API

`TestNativeGroupReconciliationRealPanel` PASS с `-count=2 -race`,29.136s на исходниках
implementation `3e52058` и добавленном acceptance test. Fresh compiled
cmd/server SHA256 `0c02cb12b6717c5d7ed65656668bcdfdec163fc217fab8e0eec684dadb1822bd`.
Owned fixture переиспользует `deploy/server-management`: pinned 3X-UI3.7.0,
настоящий TLS gateway, уникальный project/image/state; две synthetic client
records. Через provider API создаются unknown/new regular inbound и ненулевой
счётчик traffic. Disabled old regular требует detach, новый regular — attach.
Проверены unchanged UUID/subID/panel key/expiry/quota/physical limitIP1/traffic,
unknown membership, foreign client, HTTP active/non-stale и тот же no-store URL
после restart, одна operation и неизменный frozen target. Fixture удаляет только
собственные записи/инбаунды и восстанавливает old inbound. Cleanup зарегистрирован
до первой mutation, ошибки отдельных удалений не прерывают остальные действия.
Независимое дополнительное review закрыло Minor cleanup finding; повтор на той
же панели прошёл без оставшихся записей и конфликта тестовых портов.

Panel API/HTTP/River здесь настоящие; Bot API и TLS SMTP — fixtures. Browser
проверки записаны отдельно выше. VPN data-plane traffic не генерируется.
Existing CI stage двух TLS panels запускает этот тест, без второй тяжёлой
fixture stage. Переиспользованный baseline CI не заменяет целевой С40 сценарий.

## Проверка после доставки38

Actual #44/PR100 merge `9a078b35aac2cf2d96fcf2b307ffbf1913e8d009`
интегрирован в `7137eb3b49360430404a62cb2035a5c677e8abc5`. Порядок37→38→39,
целевой DownTo36 и public Compose restore consumer сохранены. Независимое
интеграционное review PASS: maintenance admission ограничивает новые клиентские
операции, сохраняет scheduler/worker/recovery/delivery; оба API-контракта сохранены.

Полный `go test ./... -count=1 -race -timeout=60m -json` с
`RUN_BROWSER_TESTS=1` и canonical public `TEST_POSTGRES_COMPOSE_FILE`: **PASS**,
17 тестовых packages, около26m11s, exit0 и пустой stderr. HTTP1567.613s,
native/browser consumers501.618s. Это итоговый прогон исходников `7137eb3`;
последующие изменения acceptance documentation не меняют product source.
Fresh native group cmd/server SHA256:
`31cf3a40bf7d1fa74b7cc0cef040cac5dfe7e47575cb78a743c1982c4120caf3`.

Canonical backup/restore отдельно PASS39.732s. Targeted реальная TLS-панель на
объединённых исходниках до фиксации merge commit PASS19.046s, binary SHA256
`5f7be614ca011d2ad99b01b4af003bf10d61fbb57c3eb927bc7f6ddd978279c7`.
Gen/vet/typecheck/build/runtime-config PASS. Объединённый Web E2E **561 passed**;
Python **112 passed**, его исходники и зависимости после этого прогона не менялись.
Exact-head PR CI и delivery фиксируются в canonical issue43/PR после результата.

Первый CI `800d8df` остановился в `check_names.py`: четыре fixture label
содержали номер сценария. Они заменены semantic names; локальный checker и
generated diff PASS, targeted реальный panel/race повтор PASS13.751s.
Product source после полного Go прогона не менялся. Перед повтором собственный
macOS bind-mounted panel получил SQLite disk I/O при чтении настройки2FA;
credential hash, integrity и свободный диск проверены. Restart только своего
primary container сохранил данные и восстановил login/сценарий. Причина
нижнего I/O слоя не установлена; это не заявляется как исправленный product bug.

## Проверка после доставки45

Actual #45/PR101 merge `ad66cfeb8bf89d8259f88ff0a8f80d0011c022b0`
потребовал разрешить один настоящий конфликт в existing CI stage. Group test,
backup rehearsal и четыре independent cleanup stages сохранены; код #45 не менялся.
Независимое integration review PASS: backup CLI не запускает native schedulers,
общий public Compose consumer совместим, schema/inventory включают migration39,
group operations и новые outbox columns. DownTo36 guard сохранён.

На объединённом рабочем дереве targeted `-race` PASS88.877s: настоящая TLS-панель
13.59s, native group24.15s, maintenance10.41s, canonical backup/restore37.62s,
ownership/selector guards. Fresh cmd/server SHA256
`ebc1ea8e447145ffb1d1f90dc6fae21d569ea547a9d1d489baee0527b23439da`.
Backup/schema/manifest/connection tests трёх packages PASS; первый отдельный DB
запуск не получил обязательный Redis URL file, после добавления входа PASS.
Names/generation/vet PASS. Platform run38020894575 отменён после обнаруженного
конфликта, до полного завершения; его PASS static и три image builds не заменяют
новый exact-head CI. Итог CI и delivery фиксируются в canonical issue43/PR.

## Эксплуатационные пределы

Native scheduler работает при старте и раз в час; legacy adapter mode не запускает
его. Existing lifecycle завершает scheduler вместе с HTTP/River/Telegram. Нет
добавочного процесса, executor, dependency или configuration option.

Для восстановления панели исправляется её состояние; следующий scan читает её
снова. Stale system target остаётся evidence и заменяется после fresh owned read.
Paid/user/reset targets не закрываются scheduler. Ambiguous reset guards сохранены.

Merge ждёт actual #44/migration38: порядок37 →38 →39. Production, внешний
Telegram, реальные платежи и пользовательские данные не затронуты. Частота
alert после same-day recovery ограничена existing dedup; новый incident state
потребуется только при отдельном требовании немедленного повторного сообщения.
