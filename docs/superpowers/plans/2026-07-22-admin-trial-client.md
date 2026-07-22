# Admin Trial Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Let an administrator create a new VPN client by Telegram ID and display name, provision the standard trial, and hand over the subscription URL without requiring the client to start the bot.

**Architecture:** `SubscriptionService` owns creation of a fresh approved `User` and calls the existing `gift_trial` path. It returns a typed result so the admin router renders exact errors without duplicating 3x-ui provisioning. The user editor collects input in an FSM and confirms once; the audit service logs successful provisioning only.

**Tech Stack:** Python 3.13, aiogram 3 FSM, SQLAlchemy async, Babel gettext, standard-library `unittest` with `AsyncMock`.

## Global Constraints

- Work in `/Users/ekho/d/tools/3xui-shop/.worktrees/create-trial-client` on `codex/create-trial-client`.
- Preserve the dirty primary checkout.
- Reuse `SubscriptionService.gift_trial`; do not make a second 3x-ui payload path.
- User fields: UUID `vpn_id`, `generate_sub_id()`, `DEFAULT_LANGUAGE`, `ApprovalStatus.APPROVED`.
- ID range is `1..2**63-1`; trimmed display name is 1–32 characters.
- Trial values are `TRIAL_PERIOD`, `TRIAL_TRAFFIC_GB`, and `BONUS_DEVICES_COUNT`.
- No third-party testing dependency. Run `poetry run python -m unittest discover -s tests -v`.

---

### Task 1: Test and implement typed trial provisioning

**Files:**

- Create: `tests/__init__.py`
- Create: `tests/test_admin_trial_creation.py`
- Modify: `app/bot/services/subscription.py`
- Modify: `app/bot/services/vpn.py`
- Modify: `app/db/models/user.py`

**Interfaces:**

- Produces `AdminTrialStatus`, `AdminTrialResult`, and `SubscriptionService.create_admin_trial(tg_id: int, first_name: str, *, approved_by: int) -> AdminTrialResult`.
- Produces `VPNService.get_available_server() -> Server | None` and `User.delete(session_factory, tg_id) -> bool`.

- [x] **Step 1: Write failing service tests**

Create `tests/test_admin_trial_creation.py`:

```python
import unittest
from contextlib import asynccontextmanager
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from app.bot.services.subscription import AdminTrialStatus, SubscriptionService


class CreateAdminTrialTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        @asynccontextmanager
        async def session_factory():
            yield object()

        self.vpn = SimpleNamespace(
            get_available_server=AsyncMock(return_value=object()),
            is_client_exists=AsyncMock(return_value=None),
        )
        self.service = SubscriptionService(
            config=SimpleNamespace(shop=SimpleNamespace(TRIAL_ENABLED=True)),
            session_factory=session_factory,
            vpn_service=self.vpn,
        )

    @patch("app.bot.services.subscription.User.create", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.get", new_callable=AsyncMock, return_value=None)
    async def test_creates_approved_user_then_uses_standard_trial(self, get, create) -> None:
        created = SimpleNamespace(tg_id=42)
        create.return_value = created
        self.service.gift_trial = AsyncMock(return_value=True)
        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)
        self.assertEqual(result.status, AdminTrialStatus.CREATED)
        self.assertIs(result.user, created)
        self.assertEqual(create.await_args.kwargs["approval_decided_by"], 7)
        self.service.gift_trial.assert_awaited_once_with(created)

    @patch("app.bot.services.subscription.User.get", new_callable=AsyncMock)
    async def test_refuses_existing_telegram_id(self, get) -> None:
        get.return_value = SimpleNamespace(tg_id=42)
        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)
        self.assertEqual(result.status, AdminTrialStatus.ALREADY_EXISTS)
        self.vpn.get_available_server.assert_not_awaited()

    @patch("app.bot.services.subscription.User.delete", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.create", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.get", new_callable=AsyncMock, return_value=None)
    async def test_removes_new_user_when_provisioning_fails(self, get, create, delete) -> None:
        create.return_value = SimpleNamespace(tg_id=42)
        self.service.gift_trial = AsyncMock(return_value=False)
        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)
        self.assertEqual(result.status, AdminTrialStatus.PROVISION_FAILED)
        delete.assert_awaited_once_with(self.service.session_factory, 42)

    @patch("app.bot.services.subscription.User.update_trial_status", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.delete", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.create", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.get", new_callable=AsyncMock, return_value=None)
    async def test_preserves_partial_panel_client(self, get, create, delete, update_trial_status) -> None:
        created = SimpleNamespace(tg_id=42)
        create.return_value = created
        self.service.gift_trial = AsyncMock(return_value=False)
        self.vpn.is_client_exists.return_value = object()
        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)
        self.assertEqual(result.status, AdminTrialStatus.PARTIAL_PROVISION)
        delete.assert_not_awaited()
        self.assertEqual(update_trial_status.await_args.kwargs["tg_id"], 42)
        self.assertTrue(update_trial_status.await_args.kwargs["used"])
```

- [x] **Step 2: Verify tests fail for missing interfaces**

Run: `poetry run python -m unittest tests.test_admin_trial_creation -v`

Expected: import error for missing `AdminTrialStatus`.

- [x] **Step 3: Implement the narrow service API**

In `subscription.py`, define:

```python
class AdminTrialStatus(StrEnum):
    CREATED = "created"
    ALREADY_EXISTS = "already_exists"
    TRIAL_DISABLED = "trial_disabled"
    NO_SERVER = "no_server"
    PROVISION_FAILED = "provision_failed"
    PARTIAL_PROVISION = "partial_provision"


@dataclass(frozen=True)
class AdminTrialResult:
    status: AdminTrialStatus
    user: User | None = None
```

Create the user only after trial/server prechecks. On `gift_trial` failure, query `is_client_exists`: delete the new DB record if no panel client exists; otherwise open a new service session, call `User.update_trial_status(session=session, tg_id=tg_id, used=True)`, and return `PARTIAL_PROVISION`, preventing a duplicate trial after an ambiguous panel response. Use this `User.create` call:

```python
user = await User.create(
    session=session, tg_id=tg_id, vpn_id=str(uuid.uuid4()), sub_id=generate_sub_id(),
    first_name=first_name, language_code=DEFAULT_LANGUAGE,
    approval_status=ApprovalStatus.APPROVED,
    approval_decided_at=datetime.now(timezone.utc), approval_decided_by=approved_by,
)
```

Implement `VPNService.get_available_server` as a one-line delegation to `server_pool_service.get_available_server`. Implement `User.delete` with an `async with session_factory()` block, `User.get`, `session.delete`, and `commit`.

- [x] **Step 4: Run focused tests after implementation**

Run: `poetry run python -m unittest tests.test_admin_trial_creation -v`

Expected: four passing tests.

- [x] **Step 5: Commit the service unit**

Run: `git add app/bot/services/subscription.py app/bot/services/vpn.py app/db/models/user.py tests/__init__.py tests/test_admin_trial_creation.py && git commit -m "feat: add admin trial provisioning service" -m "Co-authored-by: Codex <noreply@openai.com>"`

### Task 2: Audit the successful administrative mutation

**Files:**

- Modify: `app/bot/utils/constants.py`
- Modify: `app/bot/services/audit.py`
- Modify: `tests/test_admin_trial_creation.py`

**Interfaces:**

- Produces `AuditAction.USER_CREATE_TRIAL` with value `user.create_trial`.
- Produces `AuditService.admin_trial_created(actor, target, duration, devices, traffic_gb)`.

- [x] **Step 1: Add a failing audit-helper assertion**

Patch `AuditService.record` in a test, call `admin_trial_created`, and assert that its action is `AuditAction.USER_CREATE_TRIAL`, target is the passed user, and payload equals `{"duration": 3, "devices": 2, "traffic_gb": 15}`.

- [x] **Step 2: Verify it fails**

Run: `poetry run python -m unittest tests.test_admin_trial_creation -v`

Expected: `AttributeError` because `admin_trial_created` is missing.

- [x] **Step 3: Implement the audit event**

Add to `AuditAction` and `_ACTION_META`:

```python
USER_CREATE_TRIAL = "user.create_trial"

AuditAction.USER_CREATE_TRIAL: ("🆕", "Клиент создан с триалом"),
```

Add the helper:

```python
async def admin_trial_created(
    self, actor: AuditActor, target: User, duration: int, devices: int, traffic_gb: int
) -> None:
    await self.record(
        AuditAction.USER_CREATE_TRIAL, actor, target=target,
        payload={"duration": duration, "devices": devices, "traffic_gb": traffic_gb},
        channel_note=f"триал: {duration} дн.",
    )
```

Extend `format_audit_entry` with a created-trial row that shows duration, device count, and traffic cap from the stored payload.

- [x] **Step 4: Re-run the test file**

Run: `poetry run python -m unittest tests.test_admin_trial_creation -v`

Expected: five passing tests.

- [x] **Step 5: Commit the audit unit**

Run: `git add app/bot/utils/constants.py app/bot/services/audit.py tests/test_admin_trial_creation.py && git commit -m "feat: audit admin trial creation" -m "Co-authored-by: Codex <noreply@openai.com>"`

### Task 3: Add the user-editor FSM, validation, and URL handoff

**Files:**

- Modify: `app/bot/utils/navigation.py`
- Modify: `app/bot/routers/admin_tools/keyboard.py`
- Modify: `app/bot/routers/admin_tools/user_handler.py`
- Modify: `tests/test_admin_trial_creation.py`

**Interfaces:**

- Consumes `create_admin_trial` and `admin_trial_created` from Tasks 1–2.
- Produces `parse_new_client_tg_id(raw: str) -> int | None` and `normalize_new_client_name(raw: str) -> str | None`.
- Produces FSM states `create_trial_tg_id`, `create_trial_name`, and `confirm_create_trial`.

- [x] **Step 1: Add failing pure validation tests**

```python
from app.bot.routers.admin_tools.user_handler import (
    normalize_new_client_name, parse_new_client_tg_id,
)


class AdminTrialInputTests(unittest.TestCase):
    def test_accepts_signed_64_bit_positive_telegram_id(self) -> None:
        self.assertEqual(parse_new_client_tg_id("9223372036854775807"), 9223372036854775807)

    def test_rejects_invalid_telegram_ids(self) -> None:
        for raw in ("", "-1", "0", "9223372036854775808", "4.2"):
            self.assertIsNone(parse_new_client_tg_id(raw))

    def test_trims_and_bounds_display_name(self) -> None:
        self.assertEqual(normalize_new_client_name("  Мария  "), "Мария")
        self.assertIsNone(normalize_new_client_name("   "))
        self.assertIsNone(normalize_new_client_name("x" * 33))
```

- [x] **Step 2: Verify validation tests fail**

Run: `poetry run python -m unittest tests.test_admin_trial_creation.AdminTrialInputTests -v`

Expected: import error for the two missing functions.

- [x] **Step 3: Implement FSM and result rendering**

Add callbacks `CREATE_TRIAL_CLIENT`, `CONFIRM_CREATE_TRIAL_CLIENT`, and `SHOW_USER_KEY`. Prepend a localized create button to `user_editor_users_keyboard`; add a confirmation keyboard and a back-to-card keyboard. Implement the helpers:

```python
MAX_TELEGRAM_ID = 2**63 - 1
MAX_CLIENT_NAME_LENGTH = 32


def parse_new_client_tg_id(raw: str) -> int | None:
    value = raw.strip()
    if not value.isdigit():
        return None
    value_as_int = int(value)
    return value_as_int if 1 <= value_as_int <= MAX_TELEGRAM_ID else None


def normalize_new_client_name(raw: str) -> str | None:
    value = raw.strip()
    return value if 1 <= len(value) <= MAX_CLIENT_NAME_LENGTH else None
```

The confirmation handler reads both stored FSM values, clears state before `create_admin_trial`, and handles every `AdminTrialStatus`. For `CREATED`, audit with current config values, fetch `services.vpn.get_key`, and render `html.escape(key)` with a back-to-card button. For no key, render a provisioning-success warning without inventing a URL. For `PARTIAL_PROVISION`, retain the user, open its card, and warn not to retry. Register a fallback confirm handler after the state-bound handler to show a stale-flow popup. `SHOW_USER_KEY` loads a target and shows the escaped URL; `_render_card` enables its button only when the server has a subscription URL.

- [x] **Step 4: Run all focused tests**

Run: `poetry run python -m unittest tests.test_admin_trial_creation -v`

Expected: eight passing tests. Inspect that the state-bound confirmation calls `await state.set_state(None)` before the service call.

- [x] **Step 5: Commit the router unit**

Run: `git add app/bot/utils/navigation.py app/bot/routers/admin_tools/keyboard.py app/bot/routers/admin_tools/user_handler.py tests/test_admin_trial_creation.py && git commit -m "feat: add admin flow for trial clients" -m "Co-authored-by: Codex <noreply@openai.com>"`

### Task 4: Localize and verify the completed feature

**Files:**

- Modify: `app/locales/ru/LC_MESSAGES/bot.po`
- Modify: `app/locales/en/LC_MESSAGES/bot.po`

**Interfaces:**

- Consumes literal gettext keys from Task 3.
- Produces translated copy for the create button, ID/name prompts and validation, confirmation, all result statuses, stale state, key screen, and missing key.

- [x] **Step 1: Extract new gettext keys and fill both languages**

Run `poetry run bash scripts/manage_translations.sh --update`, then fill every new `msgstr` in Russian and English. Escape interpolated `name` and `key` in Python, not translated HTML.

- [x] **Step 2: Compile catalogs and run final checks**

```bash
poetry run pybabel compile -d app/locales -D bot
poetry run python -m unittest discover -s tests -v
poetry check
poetry run python -m compileall -q app
git diff --check
```

Expected: catalogs compile; all tests, Poetry validation, syntax compilation, and whitespace validation pass.

- [x] **Step 3: Commit final localized behavior**

Run: `git add app/locales/ru/LC_MESSAGES/bot.po app/locales/en/LC_MESSAGES/bot.po && git commit -m "feat: localize admin trial client flow" -m "Co-authored-by: Codex <noreply@openai.com>" && git status --short --branch`
