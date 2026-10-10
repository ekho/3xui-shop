-- name: AddPromocode :one
INSERT INTO promocodes(id,code,duration_days,created_at) VALUES($1,$2,$3,$4) RETURNING *;

-- name: LockPromocode :one
SELECT * FROM promocodes WHERE id=$1 FOR UPDATE;

-- name: ReadPromocode :one
SELECT * FROM promocodes WHERE id=$1;

-- name: EditPromocode :one
UPDATE promocodes SET duration_days=$2,revision=revision+1 WHERE id=$1 RETURNING *;

-- name: DeletePromocode :one
UPDATE promocodes SET deleted_at=$2,revision=revision+1 WHERE id=$1 RETURNING *;

-- name: CountPromocodes :one
SELECT count(*) FROM promocodes;

-- name: ListPromocodes :many
SELECT * FROM promocodes ORDER BY created_at DESC NULLS LAST,id DESC
 LIMIT sqlc.arg(per_page)::integer OFFSET sqlc.arg(offset_rows)::bigint;

-- name: AddPromocodeEvent :exec
INSERT INTO promocode_events(id,promocode_id,actor_account_id,action,created_at,reason,before_snapshot,after_snapshot)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8);

-- name: ReadPromocodeEvents :many
SELECT * FROM promocode_events WHERE promocode_id=$1 ORDER BY created_at DESC,id DESC LIMIT 51;

-- name: LockPromocodeByCode :one
SELECT * FROM promocodes WHERE code=$1 FOR UPDATE;

-- name: ActivatePromocode :one
UPDATE promocodes SET is_activated=true,activated_account_id=$2,activated_by_tg_id=$3,
 activated_at=$4,revision=revision+1 WHERE id=$1 RETURNING *;

-- name: ReadPromocodeActivation :one
SELECT promocode_id,after_snapshot FROM promocode_events
 WHERE action='activate' AND actor_account_id=$1 AND after_snapshot->>'access_operation_id'=sqlc.arg(operation_id)::text;
