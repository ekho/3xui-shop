# Euru Inbound Group Design

## Goal

Add `euru` as a fixed, bot-managed inbound group and make it an exclusive access profile alongside `regular` and `unlimited`.

## Group Invariants

- The fixed group set is `banned`, `regular`, `unlimited`, and `euru`.
- Exactly one access profile is stored for a user: `regular`, `unlimited`, or `euru`.
- `banned` is an independent overlay and may coexist with any access profile.
- A user may never be persisted without an access profile.
- Assigning `euru` removes `regular` and `unlimited`.
- Assigning `regular` removes `euru` and `unlimited`.
- Assigning `unlimited` removes `regular` and `euru`.
- Existing `unlimited` inheritance remains unchanged: it resolves both `unlimited` and `regular` inbounds.
- `euru` does not inherit another group's inbounds.

## Inbound Resolution

`euru` is added to the hard-coded known group set. Enabled inbounds whose hyphen-delimited tag contains the `euru` segment are managed as `euru` inbounds. For example, `euru-n2-in-8443-tcp` belongs to `euru`.

Unknown-tag inbounds remain outside bot management. Empty inbound resolution continues to fail safely without detaching the client's current memberships.

## State Transitions

Group selection is represented by one shared transition helper that receives the current groups and the selected access profile. It returns a sorted canonical list containing the selected profile and preserves `banned` when present. Callers do not implement their own conflict-removal rules.

### Regular to Euru

Selecting `euru` for a regular user replaces `regular` with `euru`. The VPN service attaches `euru` inbounds, detaches managed `regular` inbounds, and persists the canonical group list.

### Unlimited to Euru

Selecting `euru` for an unlimited user revokes the hidden unlimited tariff using the existing starter-trial reset semantics. Trial duration, traffic limit, and device limit are applied, but the resulting access profile is `euru` instead of `regular`. `banned` is preserved.

### Euru to Unlimited

Selecting `unlimited` uses the existing hidden-plan grant flow. The resulting group list contains `unlimited` instead of `euru`; `banned` is preserved.

### Euru to Regular

Selecting `regular`, starting a regular trial, or applying a regular paid plan replaces `euru` with `regular`. Plan replacement remains authoritative for access-profile selection.

### Removing Euru

Removing `euru` is refused when it would leave no access profile. The administrator must select `regular` or `unlimited` instead.

## Persistence Boundaries

Canonicalization is applied before VPN membership changes and before persisting user groups. Admin toggles, plan application, trial reset, unlimited grant/revoke, client creation, and reconciliation all consume canonical groups or use the shared transition helper.

No database migration is required because `User.inbound_groups` and `Plan.inbound_groups` are JSON columns. There is no legacy `euru` data to migrate.

## Admin Behavior

The existing user-groups editor displays `euru` with the other fixed groups. Selecting an access profile performs a replacement rather than an additive toggle. `banned` keeps its existing independent toggle behavior.

The transition must remain safe for users who do not yet have a panel client: the canonical group list is persisted and applied when the client is created.

## Error Handling

- If the selected profile resolves to no enabled inbounds on the user's server, the operation fails and the previous groups and panel memberships remain unchanged.
- If a panel API operation fails, the new canonical groups are not persisted.
- Reconciliation never detaches current memberships after an empty resolution.

## Tests

Tests cover:

- `euru` appears in the fixed known group set and resolves `euru-*` tags.
- `regular -> euru` removes `regular` and preserves `banned`.
- `unlimited -> euru` removes `unlimited`, applies starter-trial parameters, and preserves `banned`.
- `euru -> regular` removes `euru`.
- `euru -> unlimited` removes `euru` and grants the hidden unlimited plan.
- Removing the sole `euru` access profile is refused.
- Regular trial and paid-plan replacement remove `euru`.
- Reconciliation attaches only the canonical profile's managed inbounds.
- Empty resolution and panel API failures leave stored groups unchanged.

## Out of Scope

- A purchasable or separately configurable `euru` tariff.
- Inheritance between `euru` and another access profile.
- Dynamic synchronization of group names from 3X-UI.
- Database constraints for the JSON group columns.
