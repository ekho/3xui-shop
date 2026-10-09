-- name: SupportByAccount :one
SELECT * FROM support_conversations WHERE account_id=$1;
-- name: LockSupportByAccount :one
SELECT * FROM support_conversations WHERE account_id=$1 FOR UPDATE;
-- name: CreateSupportConversation :one
INSERT INTO support_conversations(id,account_id,status,created_at,updated_at) VALUES($1,$2,'open',$3,$3) RETURNING *;
-- name: SupportPage :many
SELECT id,sequence,sender_kind,text,created_at,attachment_name,COALESCE(octet_length(attachment_bytes),0)::bigint AS attachment_size,telegram_only
FROM support_messages WHERE conversation_id=$1 AND (sqlc.arg(before_sequence)::bigint=0 OR sequence<sqlc.arg(before_sequence)::bigint)
ORDER BY sequence DESC LIMIT 51;
-- name: SupportMessageByID :one
SELECT * FROM support_messages WHERE id=$1;
-- name: SupportAttachmentLookup :one
SELECT c.account_id,m.attachment_name FROM support_messages m JOIN support_conversations c ON c.id=m.conversation_id WHERE m.id=$1;
-- name: SupportMessageBySequence :one
SELECT sender_kind FROM support_messages WHERE conversation_id=$1 AND sequence=$2;
-- name: SupportFileBytes :one
SELECT COALESCE(SUM(octet_length(attachment_bytes)),0)::bigint FROM support_messages WHERE conversation_id=$1;
-- name: AddSupportMessage :one
INSERT INTO support_messages(id,conversation_id,sender_account_id,sender_kind,text,created_at,attachment_name,attachment_bytes)
VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;
-- name: UpdateSupportState :exec
UPDATE support_conversations SET status=$2,updated_at=$3 WHERE id=$1;
-- name: UpdateSupportBan :exec
UPDATE support_conversations SET support_banned=$2,updated_at=$3 WHERE id=$1;
-- name: AckSupportCustomer :exec
UPDATE support_conversations SET customer_received_sequence=GREATEST(customer_received_sequence,$2) WHERE id=$1;
-- name: AckSupportOperator :exec
UPDATE support_conversations SET operator_received_sequence=GREATEST(operator_received_sequence,$2) WHERE id=$1;
