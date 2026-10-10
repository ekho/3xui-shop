# С21 — локальная приёмка

Issue: #49. Base: `f64c13ce9fbc3b2dfccc6ec67d0558987f0c0fd4`.
Contract: `2026-10-10-promocode-activation-v1`,
[canonical decision](https://github.com/ekho/3xui-shop/issues/49#issuecomment-6095033564).
No own DDL; existing 00040/code history and persistent access operations are reused.
Integrated delivered target `f75dbb72c65c40dce099e2919e8809ee947951ea`
(#50/PR105, migration 00041) after actual shared-file conflicts were confirmed.
The existing early-created bonuses owner and campaign→referral registration
hook are retained; subscriptions is configured on that same owner.

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
| Integrated native promo + referrals, `-race`, 12.235s | Both actual HTTP/Telegram flows after delivered #50 integration; same owner, registration attribution, restart and stable activation behavior |
| Native Chromium + real API, `-race`, 8.715s | RU/EN, labelled keyboard form, actual POST/GET, current status, single retained grant, no code in URL/storage |
| Integrated native Chromium promo + referrals, `-race`, 10.329s | Both real API UI flows after integration and the two UI review fixes |
| Telegram focused real principal/unit checks, `-race`, 2.880s | Stable update key/lost Bot API reply, private verified actor, reject group/forwarded messages, safe responses and legacy callback compatibility |
| Focused Chromium mock checks, 7 passed | Same key after 502/503/504, current status, invalid/used errors and focus, signed MiniApp bearer/CSRF and EN, responsive width, none/expired subscription card and connection controls update without reload |
| Existing connected browser regression set | Frontend worker ran 50 checks for promo/MiniApp/web trial successfully; mandatory full CI remains a separate gate |
| Module/runtime/SQL boundaries | `go test ./internal/app -run 'Boundary\|Boundaries\|SharedFacade' -count=1`: passed, 1.333s |
| Generated contracts | Repeated `make generate` + web `api:generate` leave both combined contracts unchanged |
| Static/build | Go vet, web typecheck, test-mode build and `git diff --check`: passed |

Focused commands, with this task's private fixture URL files:

```sh
go -C backend test -race ./internal/httpapi -run '^(TestPromoActivation|TestPromocodes|TestRegressionAccessCompensation|TestRegressionAccessPreparation|TestServerPoolOperator|TestAccessOperationHTTP)' -count=1 -timeout=4m
go -C backend test -race ./internal/httpapi -run '^TestPromoActivationWorkerGuardAndUncertainWrite$' -count=1 -timeout=1m
go -C backend test -race ./internal/modules/telegram -run '^Test(ClientPromocode|ClientLegacy)' -count=1 -timeout=1m
go -C backend test -race ./tests -run '^TestNativePromoActivationSharedOwnerRestart$' -count=1 -timeout=2m
RUN_BROWSER_TESTS=1 go -C backend test -race ./tests -run '^TestNativePromoActivationBrowser$' -count=1 -timeout=2m
go -C backend test -race ./tests -run '^(TestNativePromoActivationSharedOwnerRestart|TestNativeReferralsFlow)$' -count=1 -timeout=3m
RUN_BROWSER_TESTS=1 go -C backend test -race ./tests -run '^(TestNativePromoActivationBrowser|TestNativeReferralsBrowser)$' -count=1 -timeout=3m
E2E_PORT=4189 npm --prefix web run test:e2e -- tests/promo-activation.spec.ts
```

Independent source review of `a21b9757c68efd59a1e335ad60f9d1bfc71889d4`
found two UI P2 issues: losing the retry key on gateway 502/504 and leaving the
subscription card stale after a grant. Both were reproduced by four new failing
browser scenarios, then fixed with retained 5xx attempts and the existing
Cabinet revision refresh. All seven focused tests passed. Final integrated
revision review is a separate gate recorded in the PR.

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
