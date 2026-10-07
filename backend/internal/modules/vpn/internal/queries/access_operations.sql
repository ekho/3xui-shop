-- name: AccessOperationByID :one
SELECT * FROM access_operations WHERE id=$1;
-- name: AccessOperationForAccount :one
SELECT * FROM access_operations WHERE id=$1 AND account_id=$2;
-- name: LatestAccessOperation :one
SELECT * FROM access_operations WHERE account_id=$1 AND status<>'skipped' ORDER BY sequence DESC LIMIT 1;
-- name: LatestAppliedAccess :one
SELECT * FROM access_operations WHERE account_id=$1 AND status='applied' ORDER BY updated_at DESC,sequence DESC LIMIT 1;
-- name: CurrentAccessPlanSource :one
SELECT id,plan_id FROM access_operations WHERE account_id=$1 AND status='applied'
 AND (kind IN ('purchase','assign_plan','starter_trial')
      OR (kind='set_profile' AND (plan_id IS NOT NULL OR desired->>'reset_traffic'='true')))
 ORDER BY sequence DESC LIMIT 1;
-- name: UnresolvedAccessExists :one
SELECT EXISTS(SELECT 1 FROM access_operations WHERE account_id=$1 AND status IN ('pending','provisioning','needs_review'));
-- name: UnresolvedTrialExists :one
SELECT EXISTS(SELECT 1 FROM trial_operations WHERE account_id=$1 AND status IN ('pending','provisioning','needs_review'));
-- name: InsertAccessOperation :exec
INSERT INTO access_operations(id,account_id,operator_account_id,execution_actor_id,kind,status,reason,plan_id,plan_revision,period_days,desired,target,completed_steps,monthly_period,purchase_order_id,created_at,updated_at)
VALUES($1,$2,$3,$3,$4,'pending',$5,$6,$7,$8,$9,$10,'["prepared"]'::jsonb,sqlc.narg(monthly_period)::text,sqlc.narg(purchase_order_id)::uuid,$11,$11);
-- name: LeaseAccessOperation :one
UPDATE access_operations SET status='provisioning',attempts=attempts+1,lease_hash=$2,lease_expires_at=clock_timestamp()+interval '3 minutes',updated_at=$3
WHERE id=$1 AND status IN ('pending','provisioning') RETURNING *;
-- name: MarkAccessWrite :execrows
UPDATE access_operations SET write_started=true,updated_at=$3 WHERE id=$1 AND lease_hash=$2 AND status='provisioning' AND lease_expires_at>clock_timestamp();
-- name: AppendAccessStep :execrows
UPDATE access_operations SET completed_steps=CASE WHEN completed_steps ? sqlc.arg(step)::text THEN completed_steps ELSE completed_steps||jsonb_build_array(sqlc.arg(step)::text) END,updated_at=sqlc.arg(updated_at)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND lease_hash=sqlc.arg(lease_hash)::bytea AND status='provisioning' AND lease_expires_at>clock_timestamp();
-- name: MarkAccessReset :execrows
UPDATE access_operations SET reset_started=true,reset_acknowledged=false,completed_steps=completed_steps||'["reset_started"]'::jsonb,updated_at=$3 WHERE id=$1 AND lease_hash=$2 AND status='provisioning' AND lease_expires_at>clock_timestamp();
-- name: AccessNeedsReview :execrows
UPDATE access_operations SET status='needs_review',lease_hash=NULL,lease_expires_at=NULL,review_reason=$3,updated_at=$4
WHERE id=$1 AND lease_hash=$2 AND status='provisioning';
-- name: AccessRetry :execrows
UPDATE access_operations SET status='pending',lease_hash=NULL,lease_expires_at=NULL,updated_at=$3 WHERE id=$1 AND lease_hash=$2 AND status='provisioning';
-- name: AccessApplied :execrows
UPDATE access_operations SET status='applied',lease_hash=NULL,lease_expires_at=NULL,review_reason=NULL,completed_steps=completed_steps||'["readback_confirmed"]'::jsonb,updated_at=$3
WHERE id=$1 AND lease_hash=$2 AND status='provisioning' AND lease_expires_at>clock_timestamp();
-- name: RequeueAccess :execrows
UPDATE access_operations SET status='pending',attempts=0,reset_acknowledged=$2,execution_actor_id=CASE WHEN kind='purchase' THEN NULL ELSE sqlc.arg(execution_actor_id)::uuid END,review_reason=NULL,updated_at=$3 WHERE id=$1 AND status='needs_review';
