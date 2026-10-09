-- name: LockAccount :one
SELECT * FROM accounts WHERE id=$1 FOR UPDATE;
-- name: IdempotencyByKey :one
SELECT * FROM idempotency_records WHERE principal=$1 AND operation=$2 AND key=$3;
-- name: AddIdempotency :exec
INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: LockIdempotency :exec
SELECT pg_advisory_xact_lock(hashtextextended('idem:'||sqlc.arg(principal)::text||':'||sqlc.arg(operation)::text||':'||sqlc.arg(key)::uuid::text,0));

-- name: AccountsByIDs :many
SELECT * FROM accounts WHERE id=ANY(sqlc.arg(ids)::uuid[]) ORDER BY id;

-- name: PanelLoads :many
SELECT assigned_panel_id, count(*) AS clients FROM accounts
WHERE assigned_panel_id IS NOT NULL GROUP BY assigned_panel_id;
