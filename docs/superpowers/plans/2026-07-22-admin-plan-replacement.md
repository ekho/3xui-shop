# Admin Plan Replacement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow an administrator to replace a regular user's current tariff, or reset that user to the configured trial, without changing the subscription URL or VPN credentials.

**Architecture:** The User Editor gets an FSM flow for plan selection, duration selection, and confirmation. It reuses `VPNService.change_subscription()` for paid plans and extracts a deliberate forced-trial path from the current unlimited-revocation logic. The service remains the only layer that mutates the panel; the handler validates fresh user state, records audit data, and sends the localized notification.

**Tech Stack:** Python 3.13, aiogram 3 FSM/inline keyboards, SQLAlchemy, py3xui, Babel gettext, unittest.

## Global Constraints

- Work only from `codex/admin-change-plan` in `.worktrees/admin-change-plan`; do not touch the dirty root checkout.
- Show the action only for a live panel client without `unlimited`; `banned` remains an overlay and must survive the operation.
- Do not change `User.sub_id`, `User.vpn_id`, or the assigned server.
- Regular users may select only visible plans that do not contain `unlimited`; the hidden unlimited plan remains controlled solely by User Groups.
- A selected paid plan starts a new period from now and resets traffic, exactly like a client-paid tariff change.
- The special Trial choice uses `TRIAL_PERIOD`, `BONUS_DEVICES_COUNT`, and `TRIAL_TRAFFIC_GB` even if `TRIAL_ENABLED` is false or `is_trial_used` is true.
- Consume FSM data before calling the panel, verify the user again at confirmation, and use a callback without mutable plan data for confirmation.
- Keep audit and client notification best-effort; panel mutation success must not be rolled back because either notification fails.

---

### Task 1: Make VPN tariff and forced-trial mutations report complete success

**Files:**
- Modify: `app/bot/services/vpn.py:573-724`
- Modify: `tests/test_admin_trial_creation.py`

**Interfaces:**
- Consumes: `VPNService.change_subscription(user, devices, duration, traffic_gb)` and the existing unlimited-revocation logic.
- Produces: `VPNService.force_trial(user: User) -> bool`; `change_subscription()` returns `False` if group application or traffic reset fails.

- [x] **Step 1: Add failing service tests**

  Add a `ForcedTrialTests(unittest.IsolatedAsyncioTestCase)` that creates a `VPNService` with mocked `is_client_exists`, `update_client`, `apply_inbound_groups`, `reset_traffic`, `_enforce_ban`, and `_persist_groups`. Cover all of these assertions:

  ```python
  async def test_force_trial_replaces_limits_and_preserves_ban(self) -> None:
      self.vpn.is_client_exists = AsyncMock(return_value=True)
      self.vpn.update_client = AsyncMock(return_value=True)
      self.vpn.apply_inbound_groups = AsyncMock(return_value=True)
      self.vpn.reset_traffic = AsyncMock(return_value=True)

      self.assertTrue(await self.vpn.force_trial(self.banned_user))

      self.vpn.update_client.assert_awaited_once_with(
          user=self.banned_user,
          devices=self.config.shop.BONUS_DEVICES_COUNT,
          duration=self.config.shop.TRIAL_PERIOD,
          replace_devices=True,
          replace_duration=True,
          total_gb=gb_to_bytes(self.config.shop.TRIAL_TRAFFIC_GB),
      )
      self.vpn.apply_inbound_groups.assert_awaited_once_with(
          self.banned_user, groups=["regular", "banned"], enforce_enable=True
      )
      self.vpn.reset_traffic.assert_awaited_once_with(self.banned_user)
  ```

  Also test that `force_trial()` returns `False` without a panel client, that a failed
  group application or reset returns `False`, and that `change_subscription()` returns
  `False` when its `reset_traffic()` mock returns `False`.

- [x] **Step 2: Run the new tests and verify failure**

  Run: `poetry run python -m unittest tests.test_admin_trial_creation.ForcedTrialTests -v`

  Expected: failure because `VPNService.force_trial` does not yet exist and the old
  `change_subscription()` ignores failed post-update work.

- [x] **Step 3: Extract the forced-trial primitive and complete `change_subscription`**

  Add a private helper and two public entry points in `VPNService`:

  ```python
  async def force_trial(self, user: User) -> bool:
      return await self._apply_regular_trial(user, allow_missing_client=False)

  async def revoke_unlimited(self, user: User) -> bool:
      return await self._apply_regular_trial(user, allow_missing_client=True)

  async def _apply_regular_trial(self, user: User, *, allow_missing_client: bool) -> bool:
      groups = list(DEFAULT_INBOUND_GROUPS)
      if self.inbound_group_service.is_banned(user):
          groups.append(BANNED_INBOUND_GROUP)
      if not await self.is_client_exists(user):
          if not allow_missing_client:
              return False
          await self._persist_groups(user, groups)
          return True
      ok = await self.update_client(
          user=user,
          devices=self.config.shop.BONUS_DEVICES_COUNT,
          duration=self.config.shop.TRIAL_PERIOD,
          replace_devices=True,
          replace_duration=True,
          total_gb=gb_to_bytes(self.config.shop.TRIAL_TRAFFIC_GB),
      )
      if not ok:
          return False
      if not await self.apply_inbound_groups(user, groups=groups, enforce_enable=True):
          return False
      if not await self.reset_traffic(user):
          return False
      connection = await self.server_pool_service.get_connection(user)
      if connection:
          await self._enforce_ban(self._clients(connection), user)
      return True
  ```

  Preserve the existing `EmptyInboundSetError` logging boundary around the helper.
  In `change_subscription()`, return `False` when `apply_inbound_groups()` or
  `reset_traffic()` fails; only enforce the ban and return `True` after both succeed.

- [x] **Step 4: Run the targeted tests**

  Run: `poetry run python -m unittest tests.test_admin_trial_creation -v`

  Expected: all existing admin-trial tests and every new forced-trial test pass.

- [x] **Step 5: Commit the service change**

  ```bash
  git add app/bot/services/vpn.py tests/test_admin_trial_creation.py
  git commit -m "feat: add forced trial subscription reset" \
    -m "Co-authored-by: Codex <noreply@openai.com>"
  ```

### Task 2: Add a dedicated audit event for administrator tariff changes

**Files:**
- Modify: `app/bot/utils/constants.py:151-170`
- Modify: `app/bot/services/audit.py:25-95,203-245,348-373`
- Modify: `tests/test_admin_trial_creation.py`

**Interfaces:**
- Consumes: `AuditActor.admin(callback.from_user)` and a successfully mutated `User`.
- Produces: `AuditAction.USER_CHANGE_PLAN` and `AuditService.admin_plan_changed(actor, target, mode, duration, devices, traffic_gb)`.

- [x] **Step 1: Write the failing audit test**

  Add this assertion beside `AdminTrialAuditTests`:

  ```python
  async def test_records_forced_trial_change_parameters(self) -> None:
      service = object.__new__(AuditService)
      service.record = AsyncMock()
      actor = AuditActor.system()
      target = SimpleNamespace(tg_id=42, first_name="Мария")

      await service.admin_plan_changed(
          actor, target, mode="trial", duration=3, devices=2, traffic_gb=15
      )

      service.record.assert_awaited_once_with(
          AuditAction.USER_CHANGE_PLAN,
          actor,
          target=target,
          payload={"mode": "trial", "duration": 3, "devices": 2, "traffic_gb": 15},
          channel_note="триал: 3 дн.",
      )
  ```

- [x] **Step 2: Run the test and verify failure**

  Run: `poetry run python -m unittest tests.test_admin_trial_creation.AdminTrialAuditTests -v`

  Expected: failure because `USER_CHANGE_PLAN` and `admin_plan_changed()` are absent.

- [x] **Step 3: Implement the taxonomy, helper, mirror label, and history detail**

  Add `USER_CHANGE_PLAN = "user.change_plan"` to `AuditAction`, map it to
  `("🔁", "Тариф заменён")` in `_ACTION_META`, and add:

  ```python
  async def admin_plan_changed(
      self, actor: AuditActor, target: User, *, mode: str,
      duration: int, devices: int, traffic_gb: int,
  ) -> None:
      label = "триал" if mode == "trial" else "тариф"
      await self.record(
          AuditAction.USER_CHANGE_PLAN,
          actor,
          target=target,
          payload={
              "mode": mode,
              "duration": duration,
              "devices": devices,
              "traffic_gb": traffic_gb,
          },
          channel_note=f"{label}: {duration} дн.",
      )
  ```

  Extend `_entry_detail()` so the history renders the mode, duration, device limit,
  and `безлимит` when `traffic_gb == 0`.

- [x] **Step 4: Run the audit tests**

  Run: `poetry run python -m unittest tests.test_admin_trial_creation.AdminTrialAuditTests -v`

  Expected: all audit assertions pass.

- [x] **Step 5: Commit the audit change**

  ```bash
  git add app/bot/utils/constants.py app/bot/services/audit.py tests/test_admin_trial_creation.py
  git commit -m "feat: audit admin plan changes" \
    -m "Co-authored-by: Codex <noreply@openai.com>"
  ```

### Task 3: Add callbacks and keyboards for the administrator tariff wizard

**Files:**
- Modify: `app/bot/utils/navigation.py:73-90`
- Modify: `app/bot/routers/admin_tools/keyboard.py:558-673`
- Modify: `tests/test_admin_trial_creation.py`

**Interfaces:**
- Consumes: `Plan(devices, traffic_gb, hidden, inbound_groups)` and `NavAdminTools` callbacks.
- Produces: `user_change_plan_keyboard`, `user_change_plan_duration_keyboard`, and `user_change_plan_confirm_keyboard`.

- [x] **Step 1: Write keyboard tests**

  Create a visible regular plan and a hidden unlimited plan, then assert:

  ```python
  keyboard = user_change_plan_keyboard(
      tg_id=42,
      plans=[regular_plan, hidden_unlimited_plan],
  )
  callbacks = [button.callback_data for row in keyboard.inline_keyboard for button in row]
  self.assertIn(NavAdminTools.PICK_USER_TRIAL + "_42", callbacks)
  self.assertIn(NavAdminTools.PICK_USER_PLAN + "_42_2", callbacks)
  self.assertNotIn(NavAdminTools.PICK_USER_PLAN + "_42_7", callbacks)
  ```

  Add a second test that the card keyboard contains `CHANGE_USER_PLAN_42` only when
  `show_plan_change=True`, and a third test that the confirmation keyboard carries
  exactly `CONFIRM_USER_PLAN_CHANGE` without user, plan, or duration data.

- [x] **Step 2: Run the keyboard tests and verify failure**

  Run: `poetry run python -m unittest tests.test_admin_trial_creation.AdminPlanKeyboardTests -v`

  Expected: import or assertion failure because the new callbacks and keyboards are absent.

- [x] **Step 3: Implement callbacks and keyboards**

  Add these action prefixes to `NavAdminTools`:

  ```python
  CHANGE_USER_PLAN = "change_user_plan"
  PICK_USER_PLAN = "pick_user_plan"
  PICK_USER_TRIAL = "pick_user_trial"
  PICK_USER_PLAN_DURATION = "pick_user_plan_duration"
  CONFIRM_USER_PLAN_CHANGE = "confirm_user_plan_change"
  ```

  Add `show_plan_change: bool = False` to `user_card_keyboard()` and conditionally
  render the action. Implement selection callbacks using `_{tg_id}_{devices}` and
  `_{tg_id}_{duration}` only for the non-confirmation steps. Filter plans with:

  ```python
  [
      plan for plan in plans
      if not plan.hidden and UNLIMITED_INBOUND_GROUP not in plan.inbound_groups
  ]
  ```

  Render Trial first, use `format_device_count()` for plans and
  `format_subscription_period()` for durations, and provide `back_button()` routes to
  the preceding screen. The confirmation keyboard must use only the constant callback.

- [x] **Step 4: Run the keyboard tests**

  Run: `poetry run python -m unittest tests.test_admin_trial_creation.AdminPlanKeyboardTests -v`

  Expected: all keyboard and callback-shape tests pass.

- [x] **Step 5: Commit the keyboard change**

  ```bash
  git add app/bot/utils/navigation.py app/bot/routers/admin_tools/keyboard.py tests/test_admin_trial_creation.py
  git commit -m "feat: add admin plan change keyboards" \
    -m "Co-authored-by: Codex <noreply@openai.com>"
  ```

### Task 4: Implement the User Editor plan and trial FSM flow

**Files:**
- Modify: `app/bot/routers/admin_tools/user_handler.py:35-150,383-434`
- Modify: `app/locales/ru/LC_MESSAGES/bot.po`
- Modify: `app/locales/en/LC_MESSAGES/bot.po`
- Modify: `tests/test_admin_trial_creation.py`

**Interfaces:**
- Consumes: Task 1 `VPNService.force_trial()`, Task 2 `AuditService.admin_plan_changed()`, and Task 3 keyboard/callback helpers.
- Produces: the administrator FSM flow and localized user notification for a successful forced change.

- [x] **Step 1: Write handler-level tests**

  Add tests with an `FSMContext` fake and `AsyncMock` services that prove:

  Build one fake FSM context with `target=42`, `mode="plan"`, `devices=2`, and
  `duration=30`; the fresh target is regular and present in the panel. Invoke the
  confirmation handler with that fake context and assert
  `services.vpn.change_subscription.assert_awaited_once_with(target, 2, 30, 100)` and
  `services.audit.admin_plan_changed.assert_awaited_once()`. Build a second context
  with `mode="trial"`, assert that selecting Trial renders confirmation without the
  duration keyboard, and assert `services.vpn.force_trial.assert_awaited_once_with(target)`
  after confirmation. A third test makes the reloaded target unlimited and verifies
  that state is consumed while neither VPN mutation nor audit is called.

  Add a test that `_render_card()` passes `show_plan_change=True` only when
  `client_data` exists and `services.inbound_groups.is_unlimited(target)` is false.

- [x] **Step 2: Run the handler tests and verify failure**

  Run: `poetry run python -m unittest tests.test_admin_trial_creation.AdminPlanHandlerTests -v`

  Expected: failure because the states, callbacks, and handlers do not exist.

- [x] **Step 3: Add state, fresh guards, and confirmation handling**

  Add local FSM keys `USER_EDITOR_PLAN_MODE_KEY`, `USER_EDITOR_PLAN_DEVICES_KEY`, and
  `USER_EDITOR_PLAN_DURATION_KEY`; add `choose_plan`, `choose_plan_duration`, and
  `confirm_plan_change` states. In `_render_card()`, calculate:

  ```python
  show_plan_change=bool(
      client_data and not services.inbound_groups.is_unlimited(target)
  )
  ```

  The start handler reloads the target, refuses `unlimited` or a client missing from
  the panel, then stores only `tg_id` in state and renders the filtered plan keyboard.
  The paid-plan handler validates that the selected `devices` still resolves to an
  allowed visible regular plan, then renders durations from `services.plan.get_durations()`.
  The trial handler stores `mode="trial"` and renders confirmation directly.

  On confirmation, reload the target and plan, consume all FSM keys before the panel
  call, and run exactly one of:

  ```python
  ok = await services.vpn.force_trial(target)
  # or
  ok = await services.vpn.change_subscription(
      target, plan.devices, duration, plan.traffic_gb
  )
  ```

  Use failure popups for stale data, unlimited membership, missing panel client, empty
  plans/durations, and `ok is False`. After `ok is True`, call the audit helper, render
  a fresh card, and send a best-effort localized `notify_by_id()` message. For the
  notification, select `target.language_code` when it is in `i18n.available_locales`,
  otherwise `DEFAULT_LANGUAGE`; use `i18n.use_locale()` and a message describing Trial
  or the selected device count and duration. Register a no-state confirmation fallback
  after the stateful handler so a second tap gets a stale-flow popup.

- [x] **Step 4: Add and compile translations**

  Add matching `user_editor:` entries in both `.po` files for the action label,
  selection prompts, confirmation text, Trial label, success/failure/stale popups, and
  user notification. Do not run a wholesale `pybabel update`; preserve the existing
  catalog order and add only the required entries. Then run:

  ```bash
  poetry run pybabel compile -d app/locales -D bot
  ```

  Expected: both Russian and English catalogs compile successfully.

- [x] **Step 5: Run handler and localization tests**

  Run: `poetry run python -m unittest tests.test_admin_trial_creation -v`

  Expected: all existing admin-trial tests plus the new keyboard, handler, service,
  and audit coverage pass.

- [x] **Step 6: Commit the User Editor flow**

  ```bash
  git add app/bot/routers/admin_tools/user_handler.py app/locales/ru/LC_MESSAGES/bot.po \
    app/locales/en/LC_MESSAGES/bot.po tests/test_admin_trial_creation.py
  git commit -m "feat: change user plans from admin bot" \
    -m "Co-authored-by: Codex <noreply@openai.com>"
  ```

### Task 5: Run the complete regression and review the delivery diff

**Files:**
- Verify: `app/bot/services/vpn.py`
- Verify: `app/bot/services/audit.py`
- Verify: `app/bot/routers/admin_tools/user_handler.py`
- Verify: `app/bot/routers/admin_tools/keyboard.py`
- Verify: `app/locales/ru/LC_MESSAGES/bot.po`
- Verify: `app/locales/en/LC_MESSAGES/bot.po`
- Verify: `tests/test_admin_trial_creation.py`

**Interfaces:**
- Consumes: all completed tasks.
- Produces: evidence that the feature is localized, syntactically valid, and free of
  whitespace errors.

- [x] **Step 1: Run all automated checks**

  ```bash
  poetry run pybabel compile -d app/locales -D bot
  poetry run python -m unittest discover -s tests -v
  poetry check
  poetry run python -m compileall -q app
  git diff --check origin/main...HEAD
  ```

  Expected: catalog compilation succeeds, every test passes, Poetry emits no errors,
  Python byte-compilation succeeds, and `git diff --check` has no output.

- [x] **Step 2: Review scope before publishing**

  ```bash
  git status --short --branch
  git log --oneline origin/main..HEAD
  git diff --stat origin/main...HEAD
  ```

  Expected: only the design, plan, feature source, translations, and tests are in the
  branch; the root checkout's unrelated changes are absent.

## Plan Self-Review

- **Spec coverage:** Tasks 1 and 4 preserve credentials, reset expiry from now, reset
  traffic, retain bans, and implement Trial; Task 3 filters the UI by user profile;
  Task 2 records the required audit; Tasks 4 and 5 cover localized notification,
  stale-state safety, compilation, and regression checks.
- **Placeholder scan:** No unresolved decisions, placeholder paths, or unbounded error
  handling remain. Every service and callback introduced by later tasks is defined by
  an earlier task.
- **Type consistency:** `force_trial(user) -> bool` and
  `admin_plan_changed(actor, target, mode, duration, devices, traffic_gb)` use the
  same names throughout the plan, as do all five navigation constants.
