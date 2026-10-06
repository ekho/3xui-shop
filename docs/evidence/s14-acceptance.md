# С14 — локальная приёмка Cryptomus

Владелец [#21](https://github.com/ekho/3xui-shop/issues/21), контракт
`2026-10-06-s14-cryptomus-v1`; [spec](../superpowers/specs/2026-10-06-s14-cryptomus-design.md),
[Native plan](../superpowers/plans/2026-10-06-s14-cryptomus.md), [решения](s14-decisions.md).
Backend checkpoint cbf857d, UI b14ff8e; whole-branch review 42a791c..1ac06ec.
Два Important исправлены в одном проходе; текущие исполняемые inputs сверены
по 515 SHA256. Ниже сохранён исторический checkpoint до доставки.
С14 доставлен через PR75/dev.39; итоговые CI/merge/preview и CLOSED/Project Done
указаны в последнем абзаце. Статусы pending в таблице относятся к checkpoint.

Покупка выбирает серверную USD цену. Только signed API info с final paid/paid_over,
точным invoice principal и достаточной crypto оплатой сохраняет один receipt и
разрешает существующий purchase/VPN путь. USD-net остаётся NULL; surplus не
увеличивает срок. Source IP/signature подтверждают допустимость уведомления,
само его тело не подтверждает деньги.

| Проверка | Наблюдение |
| --- | --- |
| Connected Go money/config/old methods |61 root tests PASS,150.733s, race, no skip. Первое148.425s aggregate упало только на некорректной подготовке unverified fixture; исправлена регистрация без account row. |
| Полный web после fix pass |178 PASS,100.685s; Cryptomus22cases, все старые методы сохранены. RU/EN keyboard, USD/RUB switch, frozen retry, freshness/owner/review/URL/return, period сохраняет выбранный RUB method и POST. Предварительный focused65 PASS25.684s и два keyboard RED64.842s сохранены отдельно. |
| Python/Go consumer после fix pass |105 PASS,31.226s с защищёнными TEST FILEs; legacy behavior сохранён. |
| Generator/vet/typecheck/names |PASS; generated Go/TS не отличаются от checkpoint; final static5.596s. Task1 TS7053 разрешён Task2 UI; временный backend checkpoint не считался полной web-фичей. |
| Stub self-check |PASS0.605s: signed requests, unique order_id/frozen bytes,500,info,paid/paid_over, persistent invoice. |
| Own Docker startup после fix pass |PASS45.041s; ordinary source images, isolated cabinet-c14, native3X-UI3.7.0, TLS Mailpit/API; один Go backend процесс. |
| Native purchase после fix pass |PASS51.024s: new/paid и trial/paid_over, initial500/backend+stub restart, same bytes/invoice, one receipt/job/access, trial ID/expiry+30days/limits сохранены. |
| Native paid-pending restore после fix pass |PASS23.576s: writer quiesce/dump/read-only restore/checkouts/proof/auth maintenance unchanged, restored writers не запускались; исходный backend выдал ровно один native access. |
| Full Go race после fix pass |295 root tests /13 packages PASS,459.552s; financial57 HTTP roots,62 по всем пакетам, no skip. |
| Final behavioral RED→GREEN |Pending nullable dates: RED7.300s, GREEN вместе с30 FundingBoundary cases36.174s; final paid без дат отклоняется. Manual/YooKassa period reset: rendered RED26.715s, два GREEN6.523s. |
| Final review / delivery |Fresh Astra/high: Critical0/Important2/Minor0; оба Important исправлены одним проходом, re-review не выполнялся. Exact-source CI/manual merge/actual preview пока pending. Все rulings/cost/Declined опубликованы в decisions. |

| AC / Review Focus | Исполняемое доказательство |
| --- | --- |
| AC01; creation/expiry/drift |OrderAtomic/OrderGuards/RequestAndRecovery/ExpiryAndDrift; native500/restart; actual USD vs different RUB price and immutable request. |
| AC02; signature vs API |HTTPBoundary/HTTPAuthoritativeStatus/ProviderHTTPFailures: effective IP/forgedXFF, ordered unicode/slash/number/duplicate/tamper, unknown200/noAPI, paid callback+pending info→zero funding. |
| AC03; USD vs crypto |FundingBoundary30subcases, PendingNullableDates2subcases и recovery/replay; exact decimals/paid_over/NULL net/underpay/missing/currency/time/late/cancel/AML/refund; native paid/paid_over. |
| AC04; terminal race/common guard |ObservationRace real PostgreSQL barrier; ReceiptConflictAndForeignCollision; ChangedFactsBlockPreparedAccess five cases; ReviewCannotReconcile. First immutable receipt survives while prepare/access/reconcile reject contradictory money. |
| AC05; USD/fresh checkout |cryptomus.spec.ts22cases plus old purchase/manual/yookassa suites; full web178. |
| AC06; recovery |cryptomus-local.py check/restore, actual signed TLS request/info through River and native3X-UI; SQL digests and one writer. |
| AC07 |Fresh Astra/high review и one fix pass завершены; exact CI/manual merge/actual preview pending и фиксируются отдельно в #21. |

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

Delivery after this local checkpoint: [PR75](https://github.com/ekho/3xui-shop/pull/75) merged into v2 at1733d50a9da41280406575beb8c50d9337329ecb, exact c2e01e9 source CI and source-equal merge tree verified. [Actual dev.39 build](https://github.com/ekho/3xui-shop/actions/runs/37512201378), annotated tag/prerelease and three OCI indexes/six platform revision/version/source labels verified. [#21 CLOSED/Project Done](https://github.com/ekho/3xui-shop/issues/21#issuecomment-6023041995). Own fixture stopped; protected immutable execution/delivery evidence retained.
