# С21 — ограниченный план

Spec: ../specs/2026-10-10-s21-promo-activation-design.md.
Mandate разрешает реализацию, ordinary independent workers/review, push/PR,
manual merge в v2 и preview; отдельный plan approval повторно не требуется.

1. Coordinator: failing real HTTP activation test; reuse subscriptions #14
   with public GrantBonusDays/GetBonusOperation and current account locks.
   Bonuses transactional activation/history/replay/status, sqlc queries,
   bootstrap wiring, strict OpenAPI/routes/generated contracts. No DDL.
2. Ordinary frontend worker owns ClientPromocode.tsx, Cabinet/MiniApp mount,
   api/client.ts, i18n.ts, focused mock/native browser scripts and config.
   Coordinator owns schema.gen.ts. Check RU/EN/keyboard/errors/uncertain replay.
3. Ordinary Telegram worker owns promo_activation.go/tests/runtime/app Telegram
   wiring. Public bonuses operation only; stable update identity, nonforwarded
   private commands/callback, verified actor, no code in response/logs.
4. Coordinator: real worker + panel fixture acceptance including preservation,
   lost response/restart, rollback and management races; integrate web/bot.
   Run focused race/native/browser checks, generated/vet and diff checks.
5. Fresh ordinary read-only reviewer: entire branch and acceptance evidence.
   Fix material findings, Conventional Commit + Co-Authored-By, verify exact
   SSH remote/branch/upstream, push and PR base v2.
6. One principal Platform run and three image gates at exact final HEAD.
   Cancel duplicate only after principal running + equal PR/source tree.
   Recheck target/conflicts/dependencies/allowed effects, manual merge,
   issue Closed/Project Done, clean own fixtures, handoff for parent archive.

Review focus: atomic code/grant persistence, policy before replay, account ->
promocode including operator self-activation, ban/perpetual/unlimited refusal,
stable IDs/traffic and fresh GET status after old response. Each has a real
fixture assertion; wider suite runs once in mandatory CI.
