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
-- name: ReserveGrant :exec
INSERT INTO trial_grants(account_id,request_id,operation_id,status,created_at) VALUES($1,$2,$3,'reserved',$4);
-- name: CallbackByID :one
SELECT * FROM decision_callbacks WHERE id=$1;
-- name: AddCallback :exec
INSERT INTO decision_callbacks(id,request_id,operator_tg_id,body_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: DecideTrialWeb :one
UPDATE trial_requests SET status=$2,decided_at=$3,operator_account_id=$4,reason=$5,operation_id=$6
WHERE id=$1 AND status='pending' RETURNING *;

-- name: DecideTrialAutomatic :one
UPDATE trial_requests SET status='approved',decision_source='telegram_auto',decided_at=$2,operation_id=$3
WHERE id=$1 AND status='pending' RETURNING *;

-- name: GrantApplied :exec
UPDATE trial_grants SET status='granted',granted_at=coalesce(granted_at,$2) WHERE operation_id=$1;

-- name: GrantStatus :one
SELECT status FROM trial_grants WHERE operation_id=$1 AND account_id=$2;

-- name: OperatorTrialPage :many
SELECT * FROM trial_requests WHERE account_id=$1
 AND (sqlc.arg(before_created_at)::timestamptz IS NULL OR
      (created_at,id)<(sqlc.arg(before_created_at)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT 51;
-- name: PendingOperatorTrials :many
SELECT account_id,id,created_at FROM trial_requests WHERE status='pending' ORDER BY created_at,id LIMIT 51;
