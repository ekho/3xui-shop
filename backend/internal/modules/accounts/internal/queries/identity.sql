-- name: PendingIdentityEmail :one
SELECT * FROM credential_challenges WHERE account_id=$1 AND purpose='initial_email'
 AND NOT revoked AND used_at IS NULL AND code_expires_at>sqlc.arg(now)::timestamptz;

-- name: RevokeIdentityPurpose :exec
UPDATE credential_challenges SET revoked=true WHERE account_id=$1 AND purpose=$2 AND NOT revoked AND used_at IS NULL;

-- name: GrantIndependentCredentials :exec
UPDATE accounts SET original_kind=COALESCE(original_kind,kind),kind='web',
 email_key=sqlc.arg(email_key)::text,password_hash=sqlc.arg(password_hash)::text,
 verified_at=sqlc.arg(verified_at)::timestamptz,
 terms_version=sqlc.arg(terms_version)::text,privacy_version=sqlc.arg(privacy_version)::text,
 policy_accepted_at=sqlc.arg(verified_at)::timestamptz,credential_version=credential_version+1,telegram_login_disabled=false
WHERE id=sqlc.arg(id)::uuid;

-- name: AddTelegramLinkProof :exec
INSERT INTO credential_challenges(id,purpose,account_id,original_email,target_email,credential_version,token_hash,code_hash,created_at,token_expires_at,code_expires_at)
VALUES(sqlc.arg(id)::uuid,'telegram_link',sqlc.arg(account_id)::uuid,sqlc.arg(email)::text,sqlc.arg(email)::text,
 sqlc.arg(credential_version)::bigint,sqlc.arg(token_hash)::bytea,sqlc.arg(code_hash)::bytea,sqlc.arg(created_at)::timestamptz,sqlc.arg(expires_at)::timestamptz,sqlc.arg(expires_at)::timestamptz);

-- name: TelegramReservationOwner :one
SELECT account_id FROM telegram_identity_reservations WHERE telegram_id=$1;

-- name: RetireTelegramIdentity :execrows
INSERT INTO telegram_identity_reservations(telegram_id,account_id,retired_at) VALUES($1,$2,$3)
ON CONFLICT(telegram_id) DO UPDATE SET retired_at=EXCLUDED.retired_at
WHERE telegram_identity_reservations.account_id=EXCLUDED.account_id;

-- name: ReactivateTelegramIdentity :exec
DELETE FROM telegram_identity_reservations WHERE telegram_id=$1 AND account_id=$2;

-- name: BindTelegramIdentity :exec
UPDATE accounts SET telegram_id=sqlc.arg(telegram_id)::bigint,
 display_name=COALESCE(display_name,sqlc.arg(display_name)::text),
 terms_version=sqlc.arg(terms_version)::text,privacy_version=sqlc.arg(privacy_version)::text,
 policy_accepted_at=sqlc.arg(now)::timestamptz,credential_version=credential_version+1
WHERE id=sqlc.arg(id)::uuid;

-- name: ClearTelegramIdentity :exec
UPDATE accounts SET telegram_id=NULL WHERE id=$1;

-- name: BumpCredentialVersion :exec
UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1;

-- name: QuarantineTelegramIdentity :exec
UPDATE accounts SET telegram_login_disabled=true,credential_version=credential_version+1 WHERE id=$1;

-- name: IssuedRecoveryProofs :many
SELECT id,target_email FROM credential_challenges WHERE requested_by=$1 AND purpose='identity_recovery' AND NOT revoked AND used_at IS NULL;

-- name: RevokeIssuedRecoveryProofs :exec
UPDATE credential_challenges SET revoked=true WHERE requested_by=$1 AND purpose='identity_recovery' AND NOT revoked AND used_at IS NULL;
