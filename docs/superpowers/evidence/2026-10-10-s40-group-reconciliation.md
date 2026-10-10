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

## Проверки

Owned loopback PostgreSQL18/Redis8 используют random host ports; каждый DB test
создаёт свою `platform_test_*` database. TLS panel/SMTP и Bot API синтетические.
URL/credential files находятся за пределами repository, permissions0600.

Focused Go `TestGroup*`, migration guard и HTTP recovery: PASS с `-race`.
Fresh `cmd/server` SHA256:
`1d8cf21e50b7abf3f3b6534d4ebb9c3edf597ecaee08be0e7b7078f4289a27ad`.
Native test запускает настоящий HTTP/River/Telegram process пять раз: initial
trial, panel outage/guarded alerts, recovery, no-op restart, ban. Он подтверждает
original grant/target/key, physical limitIP1, traffic42 и unknown inbound91.
Provider transport симулируется; native Go consumers и jobs не подменяются.

Python connected suite: **112 passed**. Web E2E: **551 passed**, существующие
ru/en, keyboard/error/empty-state flows. Typecheck/build/runtime-config и vet PASS.
Полный Go race/browser-consumer прогон выполняется; независимое ревью и exact-head
CI/merge фиксируются после результата, ранее полученные PASS не заменяют их.

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
