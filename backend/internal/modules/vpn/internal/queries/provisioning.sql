-- name: OperationByID :one
SELECT * FROM trial_operations WHERE id=$1;
-- name: BindTrialServer :execrows
UPDATE trial_operations SET panel_id=$2 WHERE id=$1 AND panel_id='' AND target IS NULL AND first_started_at IS NULL AND status='pending';
-- name: AccountOperation :one
SELECT * FROM trial_operations WHERE account_id=$1 ORDER BY created_at DESC LIMIT 1;
-- name: LeaseOperation :one
UPDATE trial_operations SET status='provisioning',first_started_at=coalesce(first_started_at,$2),attempts=attempts+1,lease_hash=$3,lease_expires_at=clock_timestamp()+interval '3 minutes',worker_pid=pg_backend_pid() WHERE id=$1 AND status IN ('pending','provisioning') RETURNING *;
-- name: SaveProvisionTarget :exec
UPDATE trial_operations SET target=$2 WHERE id=$1 AND target IS NULL;
-- name: MarkPanelWrite :execrows
UPDATE trial_operations SET write_started=true WHERE id=$1 AND lease_hash=$2 AND lease_expires_at>clock_timestamp() AND status='provisioning';
-- name: ApplyOperation :execrows
UPDATE trial_operations SET status='applied',lease_hash=NULL,lease_expires_at=NULL,worker_pid=NULL WHERE id=$1 AND lease_hash=$2 AND lease_expires_at>clock_timestamp() AND status='provisioning';
-- name: ReviewOperation :execrows
UPDATE trial_operations SET status='needs_review',lease_hash=NULL,lease_expires_at=NULL,worker_pid=NULL WHERE id=$1 AND ((status='applied' AND sqlc.arg(include_applied)::boolean) OR (status IN ('pending','provisioning') AND lease_hash=sqlc.narg(expected_lease)::bytea));
-- name: RetryOperation :exec
UPDATE trial_operations SET status='pending',lease_hash=NULL,lease_expires_at=NULL,worker_pid=NULL WHERE id=$1 AND status='provisioning' AND lease_hash=$2;
-- name: RequeueOperation :exec
UPDATE trial_operations SET status='pending',attempts=0 WHERE id=$1 AND status='needs_review';
-- name: ObserveTraffic :exec
UPDATE trial_operations SET traffic_used_bytes=$2,observed_at=$3 WHERE id=$1 AND status='applied';
-- name: ObserveProfileTraffic :exec
UPDATE trial_operations
SET traffic_up_bytes=$2,traffic_down_bytes=$3,traffic_used_bytes=$2::bigint+$3::bigint,observed_at=$4,profile_snapshot=sqlc.arg(snapshot)::jsonb
WHERE id=$1 AND status='applied' AND (traffic_up_bytes IS NULL OR observed_at <= $4::timestamptz);
-- name: AddOperation :exec
INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at) VALUES($1,$2,$3,'pending',true,$4,$5,$6,$7,$8);

-- name: TrialMetadata :many
SELECT id,account_id,request_id,status,created_at FROM trial_operations WHERE id=ANY(sqlc.arg(ids)::uuid[]);
