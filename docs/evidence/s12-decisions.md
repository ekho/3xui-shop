# С12 — Native/Final rulings

Task 1: Ruling: Native coordinator with one final Astra/high review — direct owner mandate preserves Native; no implementation delegation — cost if wrong: missing implementation detail before final review.

Task 1: Ruling: app/config_test.go does not exist; create app/yookassa_config_test.go, existing testkit/purchaseFixture remain reusable — cost if wrong: one bounded file location change.

Task 1: Ruling: connected provider tests live in httpapi/yookassa_test.go next to reusable real purchaseFixture, rather than recreating cross-module fixtures in payments — cost if wrong: tests may miss pure helper behavior; provider-owned validation and shared SQL are exercised through real public module/HTTP callers.

Task 1: Ruling: freeze first known checkout income while immutable receipt net remains unknown/NULL — optional provider income may arrive later; two known different values fail closed — cost if wrong: provider enrichment may need separate reconciliation.

Task 1: Ruling: settled receipt outranks late pending/waiting_for_capture observations; canceled or invalid/contradictory settled facts still block access — concurrent GET can return older nonterminal state after succeeded; behavioral RED reproduced both statuses — cost if wrong: a nonterminal provider regression requires separate reconciliation rather than automatic revocation.

Task 1: Ruling: validate later income/refund currency and range before receipt replay branch — immutable first receipt must not make malformed enrichment appear valid; behavioral RED demonstrated all three cases — cost if wrong: malformed enrichment moves the order to review.

Task 1: Runtime contract: deploy/purchase/compose.yookassa.yml and README added with disabled default, FILE-only token, retained credentials; generation/typecheck/diff-check PASS. RED→GREEN late-state and late-invalid-amount logs retained.

Task 2: Ruling: behavioral RED runs the two keyboard/preparing cases; repeating the same missing-control failure across all safety cases adds no evidence — complete new/old browser suites run at completion — cost if wrong: a negative test may need later correction, not claimed RED upfront.

Task 2: Ruling: locate the retry-bearing action alert in fresh-unsafe-url test; error and unsafe-link warning are separate existing roles — actual 44/45 run proved locator ambiguity, not navigation failure — cost if wrong: duplicated warning can be an accessibility follow-up, no money/URL safety weakened.

Task 2: Workflow retry event first returned INVALID_EVENT because ref grammar excludes bare absolute paths/spaces; normalized file URI/version after reading validator. No test retry executed before valid C07 check.

Task 3: Ruling: own subnet10.253.12.0/28 replaces proposed172.31.97.0/28, which belongs to live cabinet-test fixture; no existing network/stack altered. Docker host/none Config=null normalized during read-only preflight — cost if wrong: native fixture cannot start, unrelated resources untouched.

Task 3: Ruling: reuse installed/pinned python3.14.0-alpine3.22 for disposable stdlib API stub; no production Python dependency added — cost if wrong: fixture runtime needs a separate pinned update.

Task 3: Ruling: existing local CLI has no init; new driver prepare calls its public prepare() directly. Invalid CLI choice made no container/secret change; source read before continuation — cost if wrong: fixture readiness, not product behavior.

Task 3: Ruling: reuse unchanged purchase restore sequence on independently imported fixture with explicit compose/create/notification/snapshot overrides and private STATE/roles — exact writer quiesce/read-only restore/auth maintenance retained, provider snapshot added — cost if wrong: fixture adapter may omit a provider fact; connected restore digest and source restart assertions cover it.

Task 3: Native check first failed before plan: own operator1/catalogue0, diagnostic HTTP400 INVALID_INPUT, catalogue.go requires period x3 currencies. Fixture prices now match existing RUB/USD/XTR contract; product unchanged — cost if wrong: native cohort cannot begin.

Task 3: Python initial104/105 passed, real consumer lacked inherited TEST_DATABASE_URL_FILE/REDIS. Supply same protected absolute fixture files used by Go; no production/env/global auth change — cost if wrong: consumer prerequisite failure remains local.

Task 3: Native restart failed at provider_row cold connection; subsequent same CA/auth control succeeded. Narrow ConnectionError/TimeoutError becomes None for existing bounded wait; auth/certificate failures still fail immediately — cost if wrong: readiness waits until its bound; no funds invented.

Task 3: Go full race had one failure in HTTPContractPaths: test public proxy forwarded YooMoney only, new route fell through to SPA200. Production middleware/Caddy and direct HTTP tests already cover query rejection. Extend fixture exact route; no product guard duplicated — cost if wrong: test proxy parity, not payment authority.

Task 3: Ruling: rerun whole affected backend/tests race suite after proxy-only test edit; retain passing unchanged package records from full race run with explicit provenance, not claim first overall exit0. Exact-source CI later runs whole suite — cost if wrong: source-equivalence must be verified before combined local proof.

Task 3: Bounded restart probe demonstrated SSLEOFError/UNEXPECTED_EOF_WHILE_READING at12/121/224ms, then authentic TLS success331ms; add this precise transient class to existing wait. Certificate verification/auth errors remain uncaught. Earlier connection-only hypothesis incomplete; no product/API authority change.

Task 3: Ruling: own webhook returned503 with no funding after stub restart. Independent authenticated Python/Go fixed-host TLS succeeded; recreating backend then returned200, so no product root-cause fix is claimed. Simulate documented provider redelivery only for503 with30s bound; auth/input/TLS failures still fail — cost if wrong: persistent API failure still fails the cohort. Temporary diagnostic source restored; original backend rebuilt before acceptance. Two fmt diagnostic builds failed unused-import before runtime; builtin diagnostic build succeeded and was removed.

Task 3: Wrapper ignores stdin; Python heredoc exited0 in23ms with empty output and ran no check. This is not acceptance. Run stored private driver file, not heredoc, through wrapper — cost if wrong: missing native proof must remain pending.

Task 3: C07 pending input returned WAIT because pending means an active run; the prior empty process had already exited and failed to execute native proof. Corrected outcome to failed/no-proof. Driver had started from one shell before WAIT output was read; guard checks and dependent actions in separate calls thereafter.

Task 3: Instrumented full cohort passed and captured fixed API connect: connection refused during stub restart (plus deliberately armed500). This proves the transient path; no debug-cohort source is counted as acceptance. Source restored; one bounded original-build validation follows, with provider503 redelivery.

Task 3: Ruling: compose local Go regression from12 unchanged passing packages plus entire affected9-root backend/tests rerun; own verifier checks product equivalence and full log/file hashes — no overall-success claim for initial exit1; exact-source CI remains mandatory — cost if wrong: a dependency/source mismatch would require a new affected check. Native ordinary source41.509s and restore12.803s passed; diagnostic cohorts/empty stdin not counted.

Final preflight: C08 ref rejects URL fragments; replaced canonical issuecomment link with exact API comment URL after INVALID_EVENT, before dispatch. No evidence/authority changed.

Final: Ruling: Important canceled/settled race confirmed from shared SQL; retain grade Important, fix nonterminal receipt/cancellation in same account/order transaction; no new product decision — cost if wrong: conflict could still authorize queued native access. New test first failed compilation for nonexistent public ReviewReason; remove that assumed field, inspect retained reason through real receipt SQL instead; no behavioral RED claimed for compile failure.

Final finding: Critical 0 / Important 1 / Minor 0. Important accepted; account/order serialization reuses the existing transaction pattern and review SQL. Controlled SQL barrier reproduced behavioral RED (5.530s); same regression GREEN (7.126s). Full Go280 including financial47, Python105, ordinary native new/trial and paid-pending restore passed at the exact fix file hashes. Unchanged web156 evidence retained; exact-source CI remains a separate gate. One fix pass; no re-review. Cost if wrong: contradictory observation could authorize queued access.

Declined: real merchant credentials/payment/hosted provider UI/public webhook/fiscal readiness/production; live Happ/VPN/Mac trust/Telegram/SMTP; refunds/disputes/renewal/MiniApp/import/Python removal/promos/referrals. CI/manual merge/prerelease remain delivery gates, not locally inferred.
