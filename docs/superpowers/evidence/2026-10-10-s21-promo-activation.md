# С21 — локальная приёмка

Issue: #49. Base: `f64c13ce9fbc3b2dfccc6ec67d0558987f0c0fd4`.
Contract: `2026-10-10-promocode-activation-v1`,
[canonical decision](https://github.com/ekho/3xui-shop/issues/49#issuecomment-6095033564).
No DDL; existing 00040/code history and persistent access operations are reused.

## Среда и доказательства

Only this task's Compose project `cabinet-promo49-51e2d4a1`: private PG/Redis,
dynamic localhost ports, private network `10.241.49.0/24`. URLs are read from
mode-0600 files; tests create and remove separate UUID databases. TLS panel,
SMTP and Telegram Bot API fixtures never contact external users or production.

The first meaningful RED was the absent client route: unauthenticated POST
returned 404 instead of 401. The worker acceptance then exposed the previous
operator-only compensation guard (`actor_missing`); the client compensation
path now checks current account eligibility before writes and final apply.

Passed local checks (2026-10-10):

| Check | Result and scope |
| --- | --- |
| HTTP/grant regression, `-race`, 60.667s | Rights, strict input, ownership, concurrent clients, atomic rollback, same-key replay/conflict, account restriction, native compensation limits, stable identities/devices/traffic, expired access, real edit/delete lock races including operator self-activation, preparation races, and existing operator/server regressions |
| Worker guard/uncertain write, `-race`, 5.076s | Restriction after acceptance prevents panel write. Failure after panel update retains activation/history and exposes `needs_review`; restart replay and operator reconciliation reuse the same absolute target with one update and no traffic reset |
| Whole-process shared-owner restart, `-race`, 6.114s | Real HTTP cookie + signed MiniApp accept persistent grants; stop/recreate HTTP/River/Telegram, replay old responses, run jobs, recover lost panel reply, native private bot activation/status, foreign-owner denial and code privacy |
| Native Chromium + real API, `-race`, 8.715s | RU/EN, labelled keyboard form, actual POST/GET, current status, single retained grant, no code in URL/storage |
| Telegram focused real principal/unit checks, `-race`, 2.880s | Stable update key/lost Bot API reply, private verified actor, reject group/forwarded messages, safe responses and legacy callback compatibility |
| Focused Chromium mock checks, 3 passed, 5.7s | Same key after uncertain response, current status, invalid/used errors and focus, signed MiniApp bearer/CSRF and EN, responsive width |
| Existing connected browser regression set | Frontend worker ran 50 checks for promo/MiniApp/web trial successfully; mandatory full CI remains a separate gate |
| Module/runtime/SQL boundaries | `go test ./internal/app -run 'Boundary\|Boundaries\|SharedFacade' -count=1`: passed, 1.333s |
| Generated contracts | Second `make generate` + web `api:generate` produced no changes across 48 generated files |
| Static/build | Go vet, web typecheck, test-mode build and `git diff --check`: passed |

Focused commands, with this task's private fixture URL files:

```sh
go -C backend test -race ./internal/httpapi -run '^(TestPromoActivation|TestPromocodes|TestRegressionAccessCompensation|TestRegressionAccessPreparation|TestServerPoolOperator|TestAccessOperationHTTP)' -count=1 -timeout=4m
go -C backend test -race ./internal/httpapi -run '^TestPromoActivationWorkerGuardAndUncertainWrite$' -count=1 -timeout=1m
go -C backend test -race ./internal/modules/telegram -run '^Test(ClientPromocode|ClientLegacy)' -count=1 -timeout=1m
go -C backend test -race ./tests -run '^TestNativePromoActivationSharedOwnerRestart$' -count=1 -timeout=2m
RUN_BROWSER_TESTS=1 go -C backend test -race ./tests -run '^TestNativePromoActivationBrowser$' -count=1 -timeout=2m
E2E_PORT=4189 npm --prefix web run test:e2e -- tests/promo-activation.spec.ts
```

## Границы приёмки и delivery gates

Automated Chromium checks cover labels, status/error announcements and keyboard
focus. Manual VoiceOver, production, live 3X-UI/Telegram and final migration are
not tested here. An uncertain grant remains retained in `needs_review` until
the existing operator reconciliation confirms it; retry does not unconsume code.
Build emits existing dependency directive/chunk-size warnings and exits zero.

An independent whole-branch review, one principal full Platform run and all
three Image checks on the final PR HEAD are required before manual merge into
`v2`. Exact revision, CI links, review findings and cleanup/Closed/Project Done
are recorded in the PR and final issue comment. Merge starts the authorized v2
preview image/prerelease workflow; production is outside this acceptance.
