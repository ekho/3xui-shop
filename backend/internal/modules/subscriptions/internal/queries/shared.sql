-- name: IdempotencyByKey :one
SELECT * FROM idempotency_records WHERE principal=$1 AND operation=$2 AND key=$3;
-- name: AddIdempotency :exec
INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: LockDecisionCallback :exec
SELECT pg_advisory_xact_lock(hashtextextended('callback:'||$1::text,0));
-- name: LockIdempotency :exec
SELECT pg_advisory_xact_lock(hashtextextended('idem:'||sqlc.arg(principal)::text||':'||sqlc.arg(operation)::text||':'||sqlc.arg(key)::uuid::text,0));
-- name: LockIdempotencySession :exec
SELECT pg_advisory_lock(hashtextextended('idem:'||sqlc.arg(principal)::text||':'||sqlc.arg(operation)::text||':'||sqlc.arg(key)::uuid::text,0));
