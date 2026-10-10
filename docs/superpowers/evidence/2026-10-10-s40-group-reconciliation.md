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
