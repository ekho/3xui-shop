-- name: AccountByEmail :one
SELECT * FROM accounts WHERE email_key = $1;
-- name: AccountByID :one
SELECT * FROM accounts WHERE id = $1;
-- name: RevokeChallenges :exec
UPDATE registration_challenges SET revoked = true WHERE email_key = $1 AND NOT revoked;
-- name: RevokeMail :exec
UPDATE mail_deliveries SET ciphertext = NULL WHERE email_key = $1 AND delivered_at IS NULL;
-- name: ChallengeByID :one
SELECT * FROM registration_challenges WHERE id = $1 FOR UPDATE;
-- name: ChallengeByToken :one
SELECT * FROM registration_challenges WHERE token_hash = $1 FOR UPDATE;
-- name: AddChallenge :exec
INSERT INTO registration_challenges(id,email_key,locale,terms_version,privacy_version,token_hash,code_hash,created_at,token_expires_at,code_expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);
-- name: FailChallenge :exec
UPDATE registration_challenges SET failed_guesses = LEAST(failed_guesses+1,5) WHERE id=$1;
-- name: ConsumeChallenge :exec
UPDATE registration_challenges SET revoked=true WHERE id=$1;
-- name: AddAccount :exec
INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);
-- name: AddMail :exec
INSERT INTO mail_deliveries(id,challenge_id,email_key,ciphertext,created_at) VALUES($1,$2,$3,$4,$5);
-- name: MailByID :one
SELECT * FROM mail_deliveries WHERE id=$1 FOR UPDATE;
-- name: CompleteMail :exec
UPDATE mail_deliveries SET ciphertext=NULL, delivered_at=$2 WHERE id=$1;
-- name: LockRegistrationEmail :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0));
-- name: ChallengeEmailByID :one
SELECT email_key FROM registration_challenges WHERE id=$1;
-- name: ChallengeEmailByToken :one
SELECT email_key FROM registration_challenges WHERE token_hash=$1;
-- name: MailEmailByID :one
SELECT email_key FROM mail_deliveries WHERE id=$1;
