-- name: OperatorTrialPage :many
SELECT t.*,o.status AS operation_status,o.created_at AS operation_created_at
FROM trial_requests t LEFT JOIN trial_operations o ON o.id=t.operation_id
WHERE t.account_id=$1
 AND (sqlc.arg(before_created_at)::timestamptz IS NULL OR
      (t.created_at,t.id)<(sqlc.arg(before_created_at)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY t.created_at DESC,t.id DESC LIMIT 51;

-- name: OperatorAuditPage :many
SELECT * FROM audit_events
WHERE account_id=$1
 AND (sqlc.arg(before_created_at)::timestamptz IS NULL OR
      (created_at,id)<(sqlc.arg(before_created_at)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT 51;

-- name: DecideTrialWeb :one
UPDATE trial_requests SET status=$2,decided_at=$3,operator_account_id=$4,reason=$5,operation_id=$6
WHERE id=$1 AND status='pending' RETURNING *;

-- name: AddOperatorAudit :exec
INSERT INTO audit_events(id,created_at,action,account_id,request_id,operation_id,operator_account_id,reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8);
