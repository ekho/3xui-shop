-- name: LockAccount :one
SELECT * FROM accounts WHERE id=$1 FOR UPDATE;
-- name: TrialByID :one
SELECT * FROM trial_requests WHERE id=$1;
-- name: LockTrial :one
SELECT * FROM trial_requests WHERE id=$1 FOR UPDATE;
-- name: CurrentTrial :one
SELECT * FROM trial_requests WHERE account_id=$1 ORDER BY sequence DESC LIMIT 1;
-- name: HasGrant :one
SELECT EXISTS(SELECT 1 FROM trial_grants WHERE account_id=$1);
-- name: AddTrial :one
INSERT INTO trial_requests(id,account_id,status,comment,created_at,previous_request_id) VALUES($1,$2,'pending',$3,$4,$5) RETURNING *;
-- name: DecideTrial :one
UPDATE trial_requests SET status=$2,decided_at=$3,operator_tg_id=$4,reason=$5,operation_id=$6 WHERE id=$1 AND status='pending' RETURNING *;
-- name: AddOperation :exec
INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at) VALUES($1,$2,$3,'pending',true,$4,$5,$6,$7,$8);
-- name: ReserveGrant :exec
INSERT INTO trial_grants(account_id,request_id,operation_id,status,created_at) VALUES($1,$2,$3,'reserved',$4);
-- name: IdempotencyByKey :one
SELECT * FROM idempotency_records WHERE principal=$1 AND operation=$2 AND key=$3;
-- name: AddIdempotency :exec
INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: CallbackByID :one
SELECT * FROM decision_callbacks WHERE id=$1;
-- name: AddCallback :exec
INSERT INTO decision_callbacks(id,request_id,operator_tg_id,body_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6);
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
