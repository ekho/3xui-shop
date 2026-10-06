# С14 — локальная приёмка Cryptomus

Владелец [#21](https://github.com/ekho/3xui-shop/issues/21), контракт
`2026-10-06-s14-cryptomus-v1`; [spec](../superpowers/specs/2026-10-06-s14-cryptomus-design.md),
[Native plan](../superpowers/plans/2026-10-06-s14-cryptomus.md), [решения](s14-decisions.md).
Backend checkpoint cbf857d, UI b14ff8e; финальный source и delivery ещё не зафиксированы.

Покупка выбирает серверную USD цену. Только signed API info с final paid/paid_over,
точным invoice principal и достаточной crypto оплатой сохраняет один receipt и
разрешает существующий purchase/VPN путь. USD-net остаётся NULL; surplus не
увеличивает срок. Source IP/signature подтверждают допустимость уведомления,
само его тело не подтверждает деньги.

| Проверка | Наблюдение |
| --- | --- |
| Connected Go money/config/old methods |61 root tests PASS,150.733s, race, no skip. Первое148.425s aggregate упало только на некорректной подготовке unverified fixture; исправлена регистрация без account row. |
| Полный web |176 PASS,98.990s; focused65 PASS25.684s. RU/EN keyboard, USD/RUB switch, frozen retry, freshness/owner/review/URL/return. Первые два keyboard RED64.842s на отсутствующем Cryptomus control. |
| Python/Go consumer |105 PASS,28.936s с защищёнными TEST FILEs; legacy behavior сохранён. |
| Generator/vet/typecheck/names |PASS; generated Go/TS не отличаются от checkpoint. Task1 TS7053 разрешён Task2 UI; временный backend checkpoint не считался полной web-фичей. |
| Stub self-check |PASS0.605s: signed requests, unique order_id/frozen bytes,500,info,paid/paid_over, persistent invoice. |
| Own Docker startup |PASS47.427s; ordinary source images, isolated cabinet-c14, native3X-UI3.7.0, TLS Mailpit/API; один Go backend процесс. |
| Native purchase |PASS51.035s: new/paid и trial/paid_over, initial500/backend+stub restart, same bytes/invoice, one receipt/job/access, trial ID/expiry+30days/limits сохранены. |
| Native paid-pending restore |PASS23.660s: writer quiesce/dump/read-only restore/checkouts/proof/auth maintenance unchanged, restored writers не запускались; исходный backend выдал ровно один native access. |
| Full Go race |294 root tests /13 packages PASS,458.269s; financial56 HTTP roots, no skip. |
| Final review / delivery |Pending: не выводятся из успешных local checks. |

| AC / Review Focus | Исполняемое доказательство |
| --- | --- |
| AC01; creation/expiry/drift |OrderAtomic/OrderGuards/RequestAndRecovery/ExpiryAndDrift; native500/restart; actual USD vs different RUB price and immutable request. |
| AC02; signature vs API |HTTPBoundary/HTTPAuthoritativeStatus/ProviderHTTPFailures: effective IP/forgedXFF, ordered unicode/slash/number/duplicate/tamper, unknown200/noAPI, paid callback+pending info→zero funding. |
| AC03; USD vs crypto |FundingBoundary28subcases, exact decimals/paid_over/NULL net/underpay/missing/currency/time/late/cancel/AML/refund; native paid/paid_over. |
| AC04; terminal race/common guard |ObservationRace real PostgreSQL barrier; ReceiptConflictAndForeignCollision; ChangedFactsBlockPreparedAccess five cases; ReviewCannotReconcile. First immutable receipt survives while prepare/access/reconcile reject contradictory money. |
| AC05; USD/fresh checkout |cryptomus.spec.ts20cases plus old purchase/manual/yookassa suites; full web176. |
| AC06; recovery |cryptomus-local.py check/restore, actual signed TLS request/info through River and native3X-UI; SQL digests and one writer. |
| AC07 |Fresh Astra/high review/one fix pass if required/exact CI/manual merge/actual preview still pending. |

Native replay reschedules the existing completed provider job in this owned
fixture and verifies another authenticated info without another invoice/job/receipt/
access. This is not vendor-IP webhook delivery. Connected Go handler tests cover
that boundary. Four concrete service images and their IDs, complete logs, durations,
source SHA256 and private restore reports are retained in
`.superpowers/acceptance/c14-provider`; no credentials or links published.

Run with protected TEST_DATABASE_URL_FILE/TEST_REDIS_URL_FILE:
`RUN_BROWSER_TESTS=1 go -C backend test -race -v ./... -count=1`;
`poetry run python -m unittest discover -s tests -v`;
`npm --prefix web run test:e2e`. [Own Docker commands](../../deploy/purchase/README.md)
use the separate cabinet-c14 identity/subnet/loopback ports. Existing projects
and host trust/VPN remain untouched; own fixture volumes are retained.

Real merchant/API delivery, real crypto payment/net/fiscal/hosted UI and production
are unverified. External readiness remains before С45–С47.
