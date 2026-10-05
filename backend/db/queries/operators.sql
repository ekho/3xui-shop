-- name: OperatorAuditPage :many
SELECT * FROM audit_events
WHERE account_id=$1
 AND (sqlc.arg(before_created_at)::timestamptz IS NULL OR
      (created_at,id)<(sqlc.arg(before_created_at)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT 51;

-- name: AddOperatorAudit :exec
INSERT INTO audit_events(id,created_at,action,account_id,request_id,operation_id,operator_account_id,reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8);
