-- name: IdempotencyByKey :one
SELECT * FROM idempotency_records WHERE principal=$1 AND operation=$2 AND key=$3;
-- name: AddIdempotency :exec
INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: AddAudit :exec
INSERT INTO audit_events(id,created_at,action,account_id,request_id,operation_id,operator_tg_id,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8);
-- name: AddTelegramDelivery :exec
INSERT INTO telegram_deliveries(id,request_id,operation_id,chat_id,kind,payload,state,created_at,available_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$7);
-- name: LatestTelegramMessage :one
SELECT message_id FROM telegram_deliveries WHERE request_id=$1 AND chat_id=$2 AND state='sent' AND message_id IS NOT NULL ORDER BY sequence DESC LIMIT 1;
-- name: LatestTelegramState :one
SELECT state FROM telegram_deliveries WHERE request_id=$1 AND chat_id=$2 ORDER BY sequence DESC LIMIT 1;
-- name: LockDecisionCallback :exec
SELECT pg_advisory_xact_lock(hashtextextended('callback:'||$1::text,0));
-- name: LockIdempotency :exec
SELECT pg_advisory_xact_lock(hashtextextended('idem:'||sqlc.arg(principal)::text||':'||sqlc.arg(operation)::text||':'||sqlc.arg(key)::uuid::text,0));
-- name: AddOperatorAudit :exec
INSERT INTO audit_events(id,created_at,action,account_id,request_id,operation_id,operator_account_id,reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8);

-- name: LockIdempotencySession :exec
SELECT pg_advisory_lock(hashtextextended('idem:'||sqlc.arg(principal)::text||':'||sqlc.arg(operation)::text||':'||sqlc.arg(key)::uuid::text,0));
