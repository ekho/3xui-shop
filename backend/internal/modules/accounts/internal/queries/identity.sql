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
 policy_accepted_at=sqlc.arg(verified_at)::timestamptz,credential_version=credential_version+1
WHERE id=sqlc.arg(id)::uuid;
