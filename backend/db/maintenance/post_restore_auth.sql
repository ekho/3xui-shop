-- Restore + migrate first. Keep ingress closed and all writers/mail workers stopped.
-- On any error, do not start reconcile/serve or reopen ingress.
BEGIN;
WITH removed AS (DELETE FROM sessions RETURNING 1)
SELECT count(*) AS sessions_removed FROM removed;
WITH revoked AS (
  UPDATE credential_challenges SET revoked=true
  WHERE NOT revoked AND used_at IS NULL RETURNING 1
)
SELECT count(*) AS credential_proofs_revoked FROM revoked;
WITH cleared AS (
  UPDATE mail_deliveries m SET ciphertext=NULL
  FROM credential_challenges c
  WHERE m.kind='credential' AND m.credential_challenge_id=c.id
    AND (c.revoked OR c.used_at IS NOT NULL) AND m.ciphertext IS NOT NULL
  RETURNING 1
)
SELECT count(*) AS credential_payloads_cleared FROM cleared;
COMMIT;
