# С08/С41 acceptance driver readiness

Scope: owned `cabinet-s01-local` only. This directory contains acceptance
drivers and source-review notes, not product code or runtime controls.
No S08 behavioral command may run before root supplies a new verified
runtime manifest and a per-stage packet. S07 evidence and fixtures are
immutable inputs. No gate, probe, Compose, native inbound, installed VPN,
browser trust or clipboard operation is performed here.

Current contract source review (pre-freeze):

- [S08 design](../../docs/superpowers/specs/2026-10-02-s08-access-profiles-design.md)
  has eight criteria and mandates S41 monthly reset before unlimited is
  enabled. [Task 3 plan](../../docs/superpowers/plans/2026-10-02-s07-s08-access-operations.md)
  requires native profiles/ban and isolated Go clock/period proof.
- Current [OpenAPI](../../docs/api/openapi.yaml) extends POST
  `/api/v1/operator/clients/{id}/access-operations` with exact typed inputs
  `set_profile {profile,reason}` and `set_vpn_ban {vpn_banned,reason}`;
  `monthly_reset` is read-only response kind, never HTTP input. POST202,
  GET200, reconcile202 retain S07 semantics. Final fields/status/error
  codes must be re-read after backend source freeze.
- `Subscription.access_profile` is regular/euru/unlimited/unknown and
  `vpn_banned` is separate. A no-client intent does not fabricate a live
  subscription. New-key no-op must produce applied `state_unchanged`
  without panel write/River job; no-client save uses `intent_saved`.
- S41 source proof must cover injected UTC and Europe/Moscow clocks,
  3600/3601s startup grace, unique account/local-month period across two
  workers/restart, busy wait and elapsed-period unserved audit, banned and
  non-unlimited skip, and lost reset reply. Never change the Mac clock.

Planned one-shot stages (each stops on first FAIL/BLOCKED and emits only
booleans, counts, opaque digests and private artifact paths):

1. `readiness` — source/runtime/image/panel/fixture/plan cardinality and
   ownership read-only checks, then atomically checkpoint selected owned
   credentials and metadata before any write.
2. `finite` — current regular→euru→regular, preserving native key/UUID/subId,
   server, expiry, devices, traffic limit/used, ban and unmanaged inbound.
3. `unlimited` — hidden current plan/revision, expiry0, inherited regular,
   unchanged grant counters, reversion to chosen starter profile/reset and
   ban preservation; negative
   absent/ambiguous plan, device conflict, scheduler prerequisite.
4. `ban` — ban→S07 manual reset→ban retained→unban on the finite live
   fixture. Expired-unban, exhausted-unban and expired→unlimited enable
   boundaries require isolated source proof unless root supplies a controlled
   owned native fixture. No implicit account/support unban.
5. `intent-trial` and `intent-bonus` — two separate owned no-client accounts.
   The first keeps profile+ban; first trial consumes both and remains
   disabled. The second proves banned no-client compensation409, explicit
   unban, then bonus consumes the preserved profile without a first-trial
   Grant. No duplicate native effect.
6. `guards` and `ui` — no-op/new-key, replay/body conflict, strict
   auth/CSRF/Origin/input, live RU/EN375px and real Tab path; pending/error/
   retry can use component route doubles but must be labelled as such.
7. `S08_RUNTIME_MANIFEST=<root manifest> python3 deploy/s08/local.py restore`
   — live DB read-only pipe backup to a fresh disposable DB, synthetic period
   and uniqueness there, repeated auth cleanup, then second disposable pipe
   backup/restore to prove the period itself survives. Never seed live DB.
8. S41 isolated source tests — actual executed Go commands and test logs
   supplied by backend/root, never inferred from static source.

Root readiness packet required before stage 1:

- Exact final commit, backend/gateway/pinned panel digests, health, bot and
  reconcile stopped, primary VPN config hash unchanged, gate off/probe
  stopped. Stage 1 checks owned transport and primary VPN digest against the
  manifest; root supplies separate live image-ID inspection proof.
- Read-only classification of S07 owned accounts after S08 migration:
  confirmed profile versus legacy unknown, pending operations, native
  identity, ban and counter. An assigned S07 account may have nullable DB
  profile after migration; reuse only if latest **applied persisted access
  target**, client Subscription and native managed memberships all confirm
  regular. A native appearance alone never upgrades unknown state.
  Otherwise create at most one finite target and two independent no-client
  `s08-UUID@example.test` accounts, using S07 operator.
- Exact managed regular/EURU/unlimited and existing unmanaged local-probe
  inbound tag/ID mapping: root currently reports id1 regular port24443,
  id2 unmanaged probe port24444, id3 EURU port34444, id4 unlimited
  port34445. Approve attaching only a selected owned client to unmanaged
  id2 if needed. Never modify the inbound.
- Exactly one current hidden unlimited catalogue plan (or permission for
  the driver to create one owned plan if none), its native prerequisites and
  scheduler config. No overwrite of S09/S07 plans.
- Explicit plan for nonzero counters. Exhausted 1GiB traffic and negative
  ambiguous-plan/empty/retag/scheduler boundaries remain isolated source
  tests unless root supplies a separate controlled fixture packet.

The driver will use S07's file-backed local TLS/Mailpit and PostgreSQL
bridges, avoid raw response/secret output, and write private files mode600.
Do not execute a placeholder command merely because the source parses.
