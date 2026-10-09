-- name: AccountTelegramTopic :one
SELECT * FROM support_telegram_topics WHERE bot_id=$1 AND group_id=$2 AND account_id=$3 AND status<>'retired';
-- name: GuestTelegramTopic :one
SELECT * FROM support_telegram_topics WHERE bot_id=$1 AND group_id=$2 AND guest_tg_id=$3 AND status<>'retired';
-- name: TelegramTopicByThread :one
SELECT * FROM support_telegram_topics WHERE bot_id=$1 AND group_id=$2 AND thread_id=$3 AND status='ready';
-- name: TelegramTopicByID :one
SELECT * FROM support_telegram_topics WHERE id=$1;
-- name: LockTelegramTopic :one
SELECT * FROM support_telegram_topics WHERE id=$1 FOR UPDATE;
-- name: AddTelegramTopic :one
INSERT INTO support_telegram_topics(id,bot_id,group_id,kind,account_id,guest_tg_id,status) VALUES($1,$2,$3,$4,$5,$6,'pending') RETURNING *;
-- name: SetTelegramTopicState :exec
UPDATE support_telegram_topics SET status=$2,thread_id=$3 WHERE id=$1;
-- name: RetireTelegramTopic :exec
WITH retired AS(UPDATE support_telegram_topics t SET status='retired' WHERE t.id=$1 RETURNING t.id)
UPDATE support_telegram_deliveries d SET status=CASE WHEN d.kind='message' THEN CASE WHEN d.status='sending' THEN 'unknown' ELSE 'failed' END ELSE 'skipped' END,
code='TOPIC_RETIRED',lease=NULL,lease_expires_at=NULL,
parts=(SELECT jsonb_agg(CASE WHEN p->>'status'='sending' THEN jsonb_set(p,'{status}',CASE WHEN d.kind='message' THEN '"unknown"'::jsonb ELSE '"skipped"'::jsonb END)
 WHEN p->>'status'='queued' AND d.kind<>'message' THEN jsonb_set(p,'{status}','"skipped"') ELSE p END ORDER BY n)
 FROM jsonb_array_elements(d.parts) WITH ORDINALITY AS a(p,n))
WHERE d.topic_id IN(SELECT r.id FROM retired r) AND d.status IN('queued','sending');
-- name: AddReplacementTelegramTopic :one
INSERT INTO support_telegram_topics(id,bot_id,group_id,kind,account_id,guest_tg_id,thread_id,status,closed,support_banned,source_id)
SELECT sqlc.arg(new_id)::uuid,bot_id,group_id,kind,account_id,guest_tg_id,sqlc.arg(new_thread)::bigint,'ready',false,support_banned,source_id
FROM support_telegram_topics WHERE id=sqlc.arg(old_id)::uuid RETURNING *;
-- name: TelegramGuestBanned :one
SELECT EXISTS(SELECT 1 FROM support_telegram_guest_bans WHERE telegram_id=$1 AND banned);
-- name: SetTelegramGuestBan :exec
INSERT INTO support_telegram_guest_bans(telegram_id,banned) VALUES($1,$2)
ON CONFLICT(telegram_id) DO UPDATE SET banned=EXCLUDED.banned,updated_at=clock_timestamp();
-- name: SetTelegramTopicClosed :exec
UPDATE support_telegram_topics SET closed=$2 WHERE id=$1;
-- name: SetTelegramTopicBan :exec
UPDATE support_telegram_topics SET support_banned=$2 WHERE id=$1;
-- name: SetTelegramAccountBan :exec
UPDATE support_telegram_topics SET support_banned=$2 WHERE account_id=$1 AND status<>'retired';
-- name: HasTelegramTopicCreator :one
SELECT EXISTS(SELECT 1 FROM support_telegram_deliveries WHERE topic_id=$1 AND kind='topic_create');
-- name: PendingTelegramTopics :many
SELECT id FROM support_telegram_topics WHERE bot_id=$1 AND group_id=$2 AND status IN('pending','sending','failed','unknown') ORDER BY created_at,id LIMIT 21;
-- name: LatestTopicTelegramDelivery :one
SELECT id,status,code FROM support_telegram_deliveries WHERE topic_id=$1 ORDER BY sequence DESC LIMIT 1;
-- name: LegacySupportDigest :one
SELECT source_hash FROM legacy_support_imports WHERE bot_id=$1 AND group_id=$2 AND source_id=$3;
-- name: LegacySupportSource :one
SELECT * FROM legacy_support_imports WHERE bot_id=$1 AND group_id=$2 AND source_id=$3;
-- name: LegacyTelegramTopic :one
SELECT * FROM support_telegram_topics WHERE bot_id=$1 AND group_id=$2 AND source_id=$3 AND status<>'retired';
-- name: BindLegacyTelegramTopic :exec
UPDATE support_telegram_topics SET kind='account',account_id=$2 WHERE id=$1 AND kind='orphan';
-- name: InsertLegacySupport :execrows
INSERT INTO legacy_support_imports(bot_id,group_id,source_id,source_hash,source_json) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING;
-- name: AddLegacyTelegramTopic :exec
INSERT INTO support_telegram_topics(id,bot_id,group_id,kind,account_id,thread_id,status,closed,support_banned,created_at,source_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11);
-- name: LockTelegramReceipt :one
SELECT * FROM support_telegram_receipts WHERE id=$1 FOR UPDATE;
-- name: TelegramConfirmationDelivered :one
SELECT EXISTS(SELECT 1 FROM support_telegram_deliveries d, jsonb_array_elements(d.parts) p
 WHERE d.receipt_id=$1 AND d.kind='ack' AND d.status='sent' AND p->>'kind'='confirmation'
 AND p->>'confirmation_id'=$1::uuid::text AND p->>'status'='sent'
 AND p->>'confirmed_message_id'=$2::bigint::text);
-- name: TelegramThreadInUse :one
SELECT EXISTS(SELECT 1 FROM support_telegram_topics WHERE bot_id=$1 AND group_id=$2 AND thread_id=$3 AND id<>$4 AND status<>'retired');
-- name: TelegramReceipt :one
SELECT * FROM support_telegram_receipts WHERE id=$1;
-- name: AddTelegramReceipt :exec
INSERT INTO support_telegram_receipts(id,bot_id,group_id,chat_id,message_id,update_id,actor_tg_id,thread_id,action,callback_id,digest,actor_account_id,target_account_id,topic_id,support_message_id,owner_key,result)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17);
-- name: CompleteTelegramReceipt :exec
UPDATE support_telegram_receipts SET result=$2,support_message_id=$3,topic_id=$4 WHERE id=$1;
-- name: MessageTelegramDeliveries :many
SELECT DISTINCT ON(d.message_id) d.id,d.message_id,d.status,d.code,t.status AS topic_status
FROM support_telegram_deliveries d JOIN support_telegram_topics t ON t.id=d.topic_id
WHERE d.message_id=ANY(sqlc.arg(message_ids)::uuid[]) ORDER BY d.message_id,d.sequence DESC;
-- name: AddTelegramDelivery :exec
INSERT INTO support_telegram_deliveries(id,topic_id,message_id,receipt_id,kind,status,parts,prior_delivery_id) VALUES($1,$2,$3,$4,$5,'queued',$6,$7);
-- name: TelegramDeliveryByID :one
SELECT * FROM support_telegram_deliveries WHERE id=$1;
-- name: LockTelegramDelivery :one
SELECT * FROM support_telegram_deliveries WHERE id=$1 FOR UPDATE;
-- name: LatestMessageTelegramDelivery :one
SELECT d.*,t.status AS topic_status FROM support_telegram_deliveries d JOIN support_telegram_topics t ON t.id=d.topic_id
WHERE d.message_id=$1 ORDER BY d.sequence DESC LIMIT 1;
-- name: ActiveMessageTelegramDelivery :one
SELECT EXISTS(SELECT 1 FROM support_telegram_deliveries WHERE message_id=$1 AND status IN ('queued','sending'));
-- name: ExpireTelegramSending :exec
WITH expired AS (
 UPDATE support_telegram_deliveries d SET status='unknown',code='ACK_UNKNOWN',lease=NULL,lease_expires_at=NULL,
 parts=(SELECT jsonb_agg(CASE WHEN p->>'status'='sending' THEN jsonb_set(p,'{status}','"unknown"') ELSE p END ORDER BY n)
 FROM jsonb_array_elements(d.parts) WITH ORDINALITY AS a(p,n))
 WHERE d.status='sending' AND d.lease_expires_at<=clock_timestamp() RETURNING topic_id,kind
)
UPDATE support_telegram_topics SET status='unknown' WHERE id IN (SELECT topic_id FROM expired WHERE kind='topic_create');
-- name: ClaimTelegramDelivery :one
WITH candidate AS (
 SELECT d.id FROM support_telegram_deliveries d LEFT JOIN support_telegram_topics t ON t.id=d.topic_id LEFT JOIN support_telegram_receipts r ON r.id=d.receipt_id
 WHERE d.status='queued' AND d.available_at<=clock_timestamp() AND (d.lease_expires_at IS NULL OR d.lease_expires_at<=clock_timestamp())
 AND ((t.bot_id=$2 AND t.group_id=$3 AND (d.kind='topic_create' AND t.status='pending' OR d.kind<>'topic_create' AND t.status='ready'))
 OR (d.topic_id IS NULL AND d.kind='ack' AND r.bot_id=$2 AND r.group_id=$3))
 ORDER BY d.sequence LIMIT 1 FOR UPDATE OF d SKIP LOCKED
)
UPDATE support_telegram_deliveries SET lease=$1,lease_expires_at=clock_timestamp()+interval '60 seconds'
WHERE id=(SELECT id FROM candidate) RETURNING *;
-- name: SaveTelegramDelivery :execrows
UPDATE support_telegram_deliveries SET status=$2,code=$3,parts=$4,available_at=clock_timestamp()+sqlc.arg(delay_seconds)::integer*interval '1 second',
lease=CASE WHEN $2='sending' THEN lease ELSE NULL END,lease_expires_at=CASE WHEN $2='sending' THEN clock_timestamp()+interval '60 seconds' ELSE NULL END
WHERE id=$1 AND lease=sqlc.arg(expected_lease)::uuid AND lease_expires_at>clock_timestamp();
-- name: UnknownTelegramDelivery :exec
WITH uncertain AS (
 UPDATE support_telegram_deliveries d SET status='unknown',code='ACK_UNKNOWN',lease=NULL,lease_expires_at=NULL,
 parts=(SELECT jsonb_agg(CASE WHEN p->>'status'='sending' THEN jsonb_set(p,'{status}','"unknown"') ELSE p END ORDER BY n)
 FROM jsonb_array_elements(d.parts) WITH ORDINALITY AS a(p,n))
 WHERE d.id=$1 AND d.lease=$2 AND d.status='sending' RETURNING topic_id,kind
)
UPDATE support_telegram_topics SET status='unknown' WHERE id IN(SELECT topic_id FROM uncertain WHERE kind='topic_create');
