-- name: AddTelegramSession :exec
INSERT INTO sessions(id_hash,account_id,csrf_token,created_at,last_seen,absolute_expires_at,auth_source,telegram_id)
VALUES($1,$2,$3,$4,$5,$6,'telegram',$7);
-- name: AcceptTelegramPolicies :exec
UPDATE accounts SET terms_version=$2,privacy_version=$3,policy_accepted_at=$4 WHERE id=$1;
-- name: PreserveTelegramStartParam :exec
UPDATE accounts SET telegram_start_param=$2 WHERE id=$1 AND telegram_start_param IS NULL;
-- name: DeleteTelegramSession :execrows
DELETE FROM sessions WHERE id_hash=$1 AND auth_source='telegram';
