-- name: LockAccount :one
SELECT * FROM accounts WHERE id=$1 FOR UPDATE;
-- name: AddAudit :exec
INSERT INTO audit_events(id,created_at,action,account_id,request_id,operation_id,operator_tg_id,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8);
