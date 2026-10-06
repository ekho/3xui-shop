-- name: LeaseTelegram :one
WITH candidate AS (
 SELECT id FROM telegram_deliveries WHERE state='pending' AND available_at<=clock_timestamp() AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp()) AND chat_id=ANY(sqlc.arg(operator_ids)::bigint[]) ORDER BY sequence LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE telegram_deliveries SET lease_hash=sqlc.arg(lease_hash),lease_expires_at=clock_timestamp()+interval '60 seconds',attempts=attempts+1 WHERE id=(SELECT id FROM candidate) RETURNING *;
-- name: LockTelegram :one
SELECT *,lease_expires_at>clock_timestamp() AS lease_valid FROM telegram_deliveries WHERE id=$1 FOR UPDATE;
-- name: FinishTelegram :exec
UPDATE telegram_deliveries SET state=$2,message_id=$3,failure_code=$4,completed_at=clock_timestamp(),result_hash=$5 WHERE id=$1;
-- name: AddTelegramDelivery :exec
INSERT INTO telegram_deliveries(id,request_id,operation_id,chat_id,kind,payload,state,created_at,available_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$7);
-- name: LatestTelegramMessage :one
SELECT message_id FROM telegram_deliveries WHERE request_id=$1 AND chat_id=$2 AND state='sent' AND message_id IS NOT NULL ORDER BY sequence DESC LIMIT 1;
-- name: LatestTelegramState :one
SELECT state FROM telegram_deliveries WHERE request_id=$1 AND chat_id=$2 ORDER BY sequence DESC LIMIT 1;
