-- name: RevokeRegistrationMail :exec
UPDATE mail_deliveries SET ciphertext = NULL WHERE email_key = $1 AND kind='registration' AND delivered_at IS NULL;
-- name: AddMail :exec
INSERT INTO mail_deliveries(id,challenge_id,email_key,ciphertext,created_at) VALUES($1,$2,$3,$4,$5);
-- name: MailByID :one
SELECT * FROM mail_deliveries WHERE id=$1 FOR UPDATE;
-- name: CompleteMail :exec
UPDATE mail_deliveries SET ciphertext=NULL, delivered_at=$2 WHERE id=$1;
-- name: LookupMail :one
SELECT * FROM mail_deliveries WHERE id=$1;
-- name: AddCredentialMail :exec
INSERT INTO mail_deliveries(id,credential_challenge_id,email_key,ciphertext,created_at,kind) VALUES($1,$2,$3,$4,$5,$6);
-- name: ClearCredentialMail :exec
UPDATE mail_deliveries SET ciphertext=NULL WHERE credential_challenge_id=ANY(sqlc.arg(proof_ids)::uuid[]) AND kind='credential';
-- name: AddReminderMail :exec
INSERT INTO mail_deliveries(id,reminder_id,email_key,ciphertext,created_at,kind) VALUES($1,$2,$3,$4,$5,'reminder');
-- name: AddNoticeMail :exec
INSERT INTO mail_deliveries(id,notice_action_id,email_key,ciphertext,created_at,kind) VALUES($1,$2,$3,$4,$5,'operator_notice');
