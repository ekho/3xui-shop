-- name: AddCredentialProof :exec
INSERT INTO credential_challenges(id,purpose,account_id,change_id,original_email,target_email,credential_version,token_hash,code_hash,created_at,token_expires_at,code_expires_at,requested_by)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13);
-- name: LookupCredentialByID :one
SELECT * FROM credential_challenges WHERE id=$1;
-- name: LookupCredentialByToken :one
SELECT * FROM credential_challenges WHERE token_hash=$1;
-- name: LockCredentialProof :one
SELECT * FROM credential_challenges WHERE id=$1 FOR UPDATE;
-- name: CredentialRecipients :many
SELECT DISTINCT (CASE WHEN purpose='email_change_old' THEN original_email ELSE target_email END)::text AS email FROM credential_challenges WHERE account_id=$1 AND NOT revoked AND used_at IS NULL;
-- name: RevokeCredentialProofs :exec
UPDATE credential_challenges SET revoked=true WHERE account_id=$1 AND NOT revoked AND used_at IS NULL;
-- name: RevokeResetProofs :exec
UPDATE credential_challenges SET revoked=true WHERE target_email=$1 AND purpose='password_reset' AND NOT revoked AND used_at IS NULL;
-- name: RevokedCredentialMailProofs :many
SELECT id FROM credential_challenges WHERE account_id=$1 AND (revoked OR used_at IS NOT NULL);
-- name: ResetMailProofs :many
SELECT id FROM credential_challenges WHERE target_email=$1 AND purpose='password_reset' AND (revoked OR used_at IS NOT NULL);
-- name: FailCredentialCode :exec
UPDATE credential_challenges SET failed_guesses=LEAST(failed_guesses+1,5) WHERE id=$1;
-- name: ConsumeCredentialProof :exec
UPDATE credential_challenges SET used_at=$2 WHERE id=$1;
-- name: SetAccountPassword :exec
UPDATE accounts SET password_hash=sqlc.arg(password_hash)::text,credential_version=credential_version+1 WHERE id=sqlc.arg(id)::uuid;
-- name: DeleteAccountSessions :exec
DELETE FROM sessions WHERE account_id=$1;
-- name: ActiveEmailChange :many
SELECT * FROM credential_challenges WHERE account_id=$1 AND purpose IN ('email_change_old','email_change_new') AND NOT revoked AND used_at IS NULL AND token_expires_at>sqlc.arg(now)::timestamptz ORDER BY purpose;
-- name: LockEmailChangePair :many
SELECT * FROM credential_challenges WHERE change_id=$1 AND purpose IN ('email_change_old','email_change_new') ORDER BY purpose FOR UPDATE;
-- name: RevokeEmailChangeProofs :exec
UPDATE credential_challenges SET revoked=true WHERE account_id=$1 AND purpose IN ('email_change_old','email_change_new') AND NOT revoked AND used_at IS NULL;
-- name: ConfirmCredentialProof :exec
UPDATE credential_challenges SET confirmed_at=$2 WHERE id=$1;
-- name: SetAccountEmail :exec
UPDATE accounts SET email_key=sqlc.arg(email_key)::text,verified_at=sqlc.arg(verified_at)::timestamptz,credential_version=credential_version+1 WHERE id=sqlc.arg(id)::uuid;
