-- name: AccountVPNBan :one
SELECT vpn_banned FROM accounts WHERE id=$1;
-- name: AssignPanel :exec
UPDATE accounts SET assigned_panel_id=$2,had_subscription=true WHERE id=$1;
-- name: SetAccessMetadata :exec
UPDATE accounts SET access_profile=sqlc.narg(access_profile)::text,vpn_banned=sqlc.arg(vpn_banned)::boolean WHERE id=sqlc.arg(id)::uuid;
-- name: UnlimitedAccounts :many
SELECT id FROM accounts WHERE access_profile='unlimited' ORDER BY id;
