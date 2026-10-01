-- name: LeaseTelegram :one
WITH candidate AS (
 SELECT id FROM telegram_deliveries WHERE state='pending' AND available_at<=clock_timestamp() AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp()) AND chat_id=ANY(sqlc.arg(operator_ids)::bigint[]) ORDER BY sequence LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE telegram_deliveries SET lease_hash=sqlc.arg(lease_hash),lease_expires_at=clock_timestamp()+interval '60 seconds',attempts=attempts+1 WHERE id=(SELECT id FROM candidate) RETURNING *;
-- name: LockTelegram :one
SELECT *,lease_expires_at>clock_timestamp() AS lease_valid FROM telegram_deliveries WHERE id=$1 FOR UPDATE;
-- name: FinishTelegram :exec
UPDATE telegram_deliveries SET state=$2,message_id=$3,failure_code=$4,completed_at=clock_timestamp(),result_hash=$5 WHERE id=$1;
