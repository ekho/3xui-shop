-- name: OperatorAuditPage :many
SELECT * FROM audit_events
WHERE (sqlc.narg(account_id)::uuid IS NULL OR account_id=sqlc.narg(account_id)::uuid)
 AND (sqlc.narg(before_created_at)::timestamptz IS NULL OR
      (created_at,id)<(sqlc.narg(before_created_at)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT 51;

-- name: LegacyAuditPage :many
SELECT source_id,created_at,action,target_tg_id,actor_type,actor_id,actor_name,source
FROM legacy_audit_events
WHERE (sqlc.narg(target_tg_id)::bigint IS NULL OR target_tg_id=sqlc.narg(target_tg_id)::bigint)
 AND (sqlc.narg(before_created_at)::timestamptz IS NULL OR
      (created_at,source_id)<(sqlc.narg(before_created_at)::timestamptz,sqlc.arg(before_source_id)::bigint))
ORDER BY created_at DESC,source_id DESC LIMIT 51;

-- name: SystemAuditPage :many
SELECT * FROM audit_system_events
WHERE (sqlc.narg(before_created_at)::timestamptz IS NULL OR
 (created_at,id)<(sqlc.narg(before_created_at)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT 51;

-- name: LegacyAuditDigest :one
SELECT source_hash FROM legacy_audit_imports WHERE source_id=$1;

-- name: InsertLegacyAuditDigest :execrows
INSERT INTO legacy_audit_imports(source_id,source_hash) VALUES($1,$2) ON CONFLICT DO NOTHING;

-- name: InsertLegacyAudit :exec
INSERT INTO legacy_audit_events(source_id,created_at,action,target_tg_id,actor_type,actor_id,actor_name,source,payload_json)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: InsertSystemAudit :exec
INSERT INTO audit_system_events(id,created_at,action,period_day,cutoff,retention_days,native_count,legacy_count,system_count)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: InsertSupportTelegramAudit :exec
INSERT INTO audit_system_events(id,created_at,action,native_count,legacy_count,system_count,support_telegram)
VALUES($1,transaction_timestamp(),'support.telegram',0,0,0,$2);

-- name: AuditDatabaseTime :one
SELECT transaction_timestamp()::timestamptz;

-- name: LockAuditPrune :exec
SELECT pg_advisory_xact_lock(hashtextextended('audit-retention-v1',0));

-- name: AuditDayPruned :one
SELECT EXISTS(SELECT 1 FROM audit_system_events WHERE action='audit.pruned'
 AND (period_day=sqlc.arg(period_day)::date OR (created_at AT TIME ZONE sqlc.arg(timezone)::text)::date=sqlc.arg(period_day)::date));

-- name: PruneNativeAudit :execrows
DELETE FROM audit_events WHERE created_at<$1;

-- name: PruneLegacyAudit :execrows
DELETE FROM legacy_audit_events WHERE created_at<$1;

-- name: PruneSystemAudit :execrows
DELETE FROM audit_system_events WHERE created_at<$1;

-- name: MuteNativeAuditMirror :exec
UPDATE audit_events SET mirror_attempted_at=transaction_timestamp() WHERE mirror_attempted_at IS NULL;

-- name: MuteSystemAuditMirror :exec
UPDATE audit_system_events SET mirror_attempted_at=transaction_timestamp() WHERE mirror_attempted_at IS NULL;

-- name: ClaimNativeAuditMirror :one
UPDATE audit_events SET mirror_attempted_at=transaction_timestamp()
WHERE id=(SELECT id FROM audit_events WHERE mirror_attempted_at IS NULL ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING *;

-- name: ClaimSystemAuditMirror :one
UPDATE audit_system_events SET mirror_attempted_at=transaction_timestamp()
WHERE id=(SELECT id FROM audit_system_events WHERE mirror_attempted_at IS NULL ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING *;
