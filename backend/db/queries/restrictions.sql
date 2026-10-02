-- name: SetAccountRestriction :exec
UPDATE accounts SET restricted=$2 WHERE id=$1;

-- name: SetOperatorAccountRestriction :exec
UPDATE accounts SET restricted=$2, restriction_changed_at=$3, restriction_operator_account_id=$4 WHERE id=$1;

-- name: OperatorRoleExists :one
SELECT EXISTS(SELECT 1 FROM operator_accounts WHERE account_id=$1);

-- name: LegacyApprovalByAccount :one
SELECT * FROM legacy_approval_snapshots WHERE account_id=$1;

-- name: LegacyApprovalBySourceID :one
SELECT * FROM legacy_approval_snapshots WHERE source_legacy_user_id=$1;

-- name: InsertLegacyApproval :execrows
INSERT INTO legacy_approval_snapshots(account_id,source_legacy_user_id,source_tg_id,status,requested_at,decided_at,decided_by)
VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING;

-- name: LegacyApprovalEventByID :one
SELECT * FROM legacy_approval_events WHERE source_id=$1;

-- name: InsertLegacyApprovalEvent :execrows
INSERT INTO legacy_approval_events(source_id,account_id,target_tg_id,created_at,action,actor_type,actor_id,actor_name,source)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING;

-- name: LegacyApprovalPage :many
SELECT * FROM legacy_approval_events
WHERE account_id=$1 AND (sqlc.arg(before_created_at)::timestamptz IS NULL OR
 (created_at,source_id)<(sqlc.arg(before_created_at)::timestamptz,sqlc.arg(before_source_id)::bigint))
ORDER BY created_at DESC,source_id DESC LIMIT 51;
