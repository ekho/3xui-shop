# EURU Inbound Group Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `euru` as a bot-managed access profile that is mutually exclusive with `regular` and `unlimited`, while preserving the independent `banned` overlay.

**Architecture:** Keep group invariants in `InboundGroupService` as pure transition helpers, then route admin and VPN profile changes through those helpers. Reuse the existing starter-trial and unlimited-plan flows so profile changes also update the special unlimited tariff state correctly. Reconciliation continues to consume `effective_groups()`, which will return a canonical profile.

**Tech Stack:** Python 3.12, aiogram, SQLAlchemy async, py3xui, unittest, Poetry.

## Global Constraints

- Fixed groups are `banned`, `regular`, `unlimited`, and `euru`.
- Exactly one access profile is effective: `regular`, `unlimited`, or `euru`.
- `banned` remains an independent overlay and is preserved by profile transitions.
- `unlimited` continues to inherit `regular` inbounds; `euru` inherits nothing.
- Selecting `euru` from `unlimited` applies starter-trial limits and ends in `euru`.
- Empty inbound resolution must leave stored groups unchanged.
- No schema migration, purchasable EURU plan, dynamic panel-group sync, dependency change, or unrelated refactor.
- Do not commit unless the user explicitly requests it.

---

### Task 1: Define Canonical Access Profiles

**Files:**
- Modify: `app/bot/utils/constants.py:28-55`
- Modify: `app/bot/services/inbound_groups.py:59-108`
- Create: `tests/test_inbound_groups.py`

**Interfaces:**
- Produces: `EURU_INBOUND_GROUP = "euru"`
- Produces: `ACCESS_INBOUND_GROUPS: tuple[str, ...]`
- Produces: `InboundGroupService.transition_access_profile(groups: list[str] | None, selected: str) -> list[str]`
- Produces: `InboundGroupService.canonical_groups(groups: list[str] | None) -> list[str]`
- Consumes: existing `BANNED_INBOUND_GROUP`, `DEFAULT_INBOUND_GROUPS`, and `INBOUND_GROUP_INCLUDES`

- [ ] **Step 1: Write failing transition and resolution tests**

```python
def test_transition_to_euru_replaces_regular_and_preserves_banned() -> None:
    assert InboundGroupService.transition_access_profile(
        ["regular", "banned"], "euru"
    ) == ["banned", "euru"]


def test_transition_to_unlimited_replaces_euru() -> None:
    assert InboundGroupService.transition_access_profile(
        ["euru"], "unlimited"
    ) == ["unlimited"]


def test_euru_does_not_inherit_regular() -> None:
    assert InboundGroupService.expand_access_groups(["euru"]) == ["euru"]
```

Also cover `euru -> regular`, `unlimited -> euru`, empty input defaulting to `regular`, malformed multi-profile values, exact `euru-*` tag segments, disabled inbounds, and `INBOUND_GROUPS` containing all four names.

- [ ] **Step 2: Run tests and confirm RED**

Run: `poetry run python -m unittest tests.test_inbound_groups -v`

Expected: failure because `EURU_INBOUND_GROUP`, canonicalization, and transition helpers do not exist.

- [ ] **Step 3: Implement the minimal constants and pure helpers**

```python
EURU_INBOUND_GROUP = "euru"
ACCESS_INBOUND_GROUPS = (
    REGULAR_INBOUND_GROUP,
    UNLIMITED_INBOUND_GROUP,
    EURU_INBOUND_GROUP,
)
INBOUND_GROUPS = (BANNED_INBOUND_GROUP, *ACCESS_INBOUND_GROUPS)
```

`transition_access_profile()` validates `selected`, replaces all access profiles with it, preserves `banned`, and returns a sorted list. `canonical_groups()` chooses one deterministic profile for malformed stored data, preserving current special-state precedence: `unlimited`, then `euru`, then `regular`; it defaults to `regular` when no access profile exists. Update `effective_groups()` to delegate to `canonical_groups()`.

- [ ] **Step 4: Run focused tests and confirm GREEN**

Run: `poetry run python -m unittest tests.test_inbound_groups -v`

Expected: all profile, inheritance, and tag-resolution tests pass.

---

### Task 2: Route VPN Profile Transitions Through One Service Boundary

**Files:**
- Modify: `app/bot/services/vpn.py:318-376,475-529,571-759`
- Create: `tests/test_vpn_group_transitions.py`
- Modify: `tests/test_admin_trial_creation.py`

**Interfaces:**
- Consumes: `InboundGroupService.canonical_groups()` and `transition_access_profile()`
- Produces: `VPNService.select_access_profile(user: User, selected: str) -> bool`
- Changes: `_apply_regular_trial()` becomes `_apply_starter_trial(user, selected_profile, *, allow_missing_client)`

- [ ] **Step 1: Write failing VPN transition tests**

```python
async def test_select_euru_from_regular_replaces_regular() -> None:
    vpn.apply_inbound_groups = AsyncMock(return_value=True)
    user.inbound_groups = ["regular"]
    assert await vpn.select_access_profile(user, "euru") is True
    vpn.apply_inbound_groups.assert_awaited_once_with(user, groups=["euru"])


async def test_select_euru_from_unlimited_uses_starter_trial() -> None:
    vpn._apply_starter_trial = AsyncMock(return_value=True)
    user.inbound_groups = ["unlimited", "banned"]
    assert await vpn.select_access_profile(user, "euru") is True
    vpn._apply_starter_trial.assert_awaited_once_with(
        user, "euru", allow_missing_client=True
    )
```

Cover `euru -> unlimited`, `euru -> regular`, preservation of `banned`, no-panel-client persistence, empty EURU resolution, and plan/trial replacement removing EURU.

- [ ] **Step 2: Run tests and confirm RED**

Run: `poetry run python -m unittest tests.test_vpn_group_transitions -v`

Expected: failure because profile selection and parameterized starter-trial reset do not exist.

- [ ] **Step 3: Canonicalize every VPN persistence input**

In `create_client()` and `apply_inbound_groups()`, canonicalize explicit or effective groups before resolving or persisting them. Preserve the existing rule that an API or empty-resolution failure occurs before `_persist_groups()`.

- [ ] **Step 4: Add profile selection routing**

```python
async def select_access_profile(self, user: User, selected: str) -> bool:
    if selected == UNLIMITED_INBOUND_GROUP:
        return await self.grant_unlimited(user)
    if self.inbound_group_service.is_unlimited(user):
        return await self._apply_starter_trial(
            user, selected, allow_missing_client=True
        )
    groups = self.inbound_group_service.transition_access_profile(
        user.inbound_groups, selected
    )
    return await self.apply_inbound_groups(user, groups=groups)
```

Parameterize the starter reset so its target groups are built with `transition_access_profile(user.inbound_groups, selected_profile)`. Keep `force_trial()` and `revoke_unlimited()` targeting `regular`; selecting EURU from unlimited targets `euru`.

- [ ] **Step 5: Make paid plans authoritative**

Before `create_client()` or `apply_inbound_groups()`, canonicalize plan groups and preserve only the existing `banned` overlay. Ensure `extend_subscription()` returns `False` when `apply_inbound_groups()` fails instead of continuing to reset traffic.

- [ ] **Step 6: Run focused and regression tests**

Run: `poetry run python -m unittest tests.test_vpn_group_transitions -v`

Run: `poetry run python -m unittest tests.test_admin_trial_creation.ForcedTrialTests -v`

Expected: all tests pass.

---

### Task 3: Make the Admin Editor Select Exclusive Profiles

**Files:**
- Modify: `app/bot/routers/admin_tools/group_handler.py:26-141`
- Modify: `app/bot/routers/admin_tools/user_handler.py:107-126,488-728`
- Create: `tests/test_admin_group_handler.py`
- Modify: `tests/test_admin_trial_creation.py`

**Interfaces:**
- Consumes: `VPNService.select_access_profile()`
- Consumes: `EURU_INBOUND_GROUP` and `ACCESS_INBOUND_GROUPS`

- [ ] **Step 1: Write failing handler tests**

```python
async def test_regular_to_euru_routes_through_profile_selection() -> None:
    services.vpn.select_access_profile = AsyncMock(return_value=True)
    target.inbound_groups = ["regular"]
    await callback_toggle_user_group(callback, admin, session, services)
    services.vpn.select_access_profile.assert_awaited_once_with(target, "euru")


async def test_removing_only_euru_is_refused() -> None:
    target.inbound_groups = ["euru"]
    await callback_toggle_user_group(callback, admin, session, services)
    services.vpn.select_access_profile.assert_not_awaited()
    services.notification.show_popup.assert_awaited()
```

Cover unlimited-to-EURU, EURU-to-unlimited, `banned` toggling preserving EURU, and no-client profile selection going through the VPN service rather than direct `User.update()`.

- [ ] **Step 2: Run tests and confirm RED**

Run: `poetry run python -m unittest tests.test_admin_group_handler -v`

Expected: access groups are still toggled additively.

- [ ] **Step 3: Replace additive access toggles with profile selection**

Keep the existing independent `banned` branch. For access-profile callbacks, refuse removal of the current sole profile, preserve current unlimited-to-regular behavior when clicking active unlimited, and otherwise call `select_access_profile()`.

- [ ] **Step 4: Permit EURU users to choose a regular plan**

Replace `_is_regular_user()` with a predicate that accepts canonical `regular` or `euru` profiles and rejects `unlimited`. Keep `_is_regular_plan()` limited to non-hidden regular plans. Update existing tests to assert EURU users can enter and confirm regular trial/plan flows, which then remove EURU through Task 2.

- [ ] **Step 5: Run handler and plan-flow tests**

Run: `poetry run python -m unittest tests.test_admin_group_handler -v`

Run: `poetry run python -m unittest tests.test_admin_trial_creation.AdminPlanHandlerTests -v`

Expected: all tests pass.

---

### Task 4: Verify Reconciliation Uses the Canonical EURU Profile

**Files:**
- Modify: `app/bot/tasks/inbound_reconcile.py:31-124`
- Create: `tests/test_inbound_reconcile.py`

**Interfaces:**
- Consumes: canonical `InboundGroupService.effective_groups()`
- Consumes: unchanged `expand_access_groups()` inheritance map

- [ ] **Step 1: Write failing reconciliation tests**

```python
async def test_reconcile_euru_detaches_managed_regular_only() -> None:
    user.inbound_groups = ["euru"]
    await reconcile_inbound_groups(
        session_factory, redis, server_pool, groups, notifications
    )
    clients.attach.assert_awaited_once_with(str(user.tg_id), [EURU_ID])
    clients.detach.assert_awaited_once_with(str(user.tg_id), [REGULAR_ID])
```

Also cover EURU plus `banned`, malformed `regular+euru` canonicalization, unknown/manual inbound preservation, empty EURU resolution producing an alert without attach/detach, and unlimited still resolving both unlimited and regular.

- [ ] **Step 2: Run tests and confirm RED**

Run: `poetry run python -m unittest tests.test_inbound_reconcile -v`

Expected: EURU is absent from known groups or malformed stored profiles are not canonicalized.

- [ ] **Step 3: Make only the required reconciler changes**

Update comments and any direct assumptions that the fixed set contains only three groups. Continue deriving groups through `effective_groups()` and desired inbounds through `expand_access_groups()`. Do not add database rewrites or alter attach/detach error policy.

- [ ] **Step 4: Run focused reconciliation tests**

Run: `poetry run python -m unittest tests.test_inbound_reconcile -v`

Expected: all tests pass.

---

### Task 5: Final Verification

**Files:**
- Verify all modified files and tests from Tasks 1-4.

- [ ] **Step 1: Run Python diagnostics**

Run `lsp_diagnostics` on every changed `.py` file. Expected: no errors or warnings introduced by this change.

- [ ] **Step 2: Run the full test suite**

Run: `poetry run python -m unittest discover -s tests -v`

Expected: exit code 0 with all tests passing.

- [ ] **Step 3: Run syntax/import verification**

Run: `poetry run python -m compileall -q app tests`

Expected: exit code 0.

- [ ] **Step 4: Inspect scope and whitespace**

Run: `git diff --check`

Confirm no migration, dependency change, dynamic group sync, EURU tariff, or unrelated refactor was added.
