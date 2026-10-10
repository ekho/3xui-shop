# Referral rewards implementation plan

> For agentic workers: execute the bounded tasks in this session; preserve file ownership and other edits.

**Goal:** issue confirmed-payment referral days once through the existing access executor.
**Architecture:** payments retains funding, bonuses retains reward facts/jobs,
subscriptions prepares the shared compensation target, VPN AccessWorker writes it.
**Tech stack:** existing Go/PG/River and native HTTP/Telegram fixtures.
**Spec:** ../specs/2026-10-10-s24-referral-rewards-design.md.

## Constraints and review focus

Feature branch from f75dbb7/origin/v2; migration00042 only; no MONEY/payout,
trial extension/import/Python removal, production, new dependency or second
payment/access executor. Review refund/replay before write, callback rollback,
account/source/link changes, absolute target after lost reply and provider recovery.

1. [x] Focused funding tests, owner port/callback in payments/service.go,
   renewal.go/referral_funding.go; prove same-Tx receipt/reward rollback.
2. [x] Schema00042 + owned bonuses reward SQL/worker/config. Preserve legacy facts;
   stable keys, pending jobs and applied outcome through public module ports.
3. [x] Integrate only the agreed four-file shared #49 port; wire existing app owner,
   same VPN worker funding guard/outcome, worker/lifecycle registration.
4. [x] Run migration/module/funding/native HTTP/Telegram/restart/concurrency tests
   on owned fixtures; generate SQL and run boundary/name/static checks. Record
   exact revision/limits and independent read-only review; fix actual findings.
5. [ ] Commit/push/PR v2; one principal full exact-head Platform and three image
   gates. Verify fresh target/dependencies and manual merge. Close #51, set Project
   Done, remove owned fixtures and hand evidence to parent for archive.
