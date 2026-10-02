-- +goose Up
ALTER TABLE accounts ADD COLUMN credential_version bigint NOT NULL DEFAULT 0 CHECK (credential_version >= 0);
CREATE TABLE credential_challenges (
  id uuid PRIMARY KEY,
  purpose text NOT NULL CHECK (purpose IN ('password_reset','email_change_old','email_change_new')),
  account_id uuid REFERENCES accounts(id),
  change_id uuid,
  original_email text NOT NULL,
  target_email text NOT NULL,
  credential_version bigint NOT NULL CHECK (credential_version >= 0),
  token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
  code_hash bytea NOT NULL CHECK (octet_length(code_hash)=32),
  created_at timestamptz NOT NULL,
  token_expires_at timestamptz NOT NULL,
  code_expires_at timestamptz NOT NULL,
  failed_guesses integer NOT NULL DEFAULT 0 CHECK (failed_guesses BETWEEN 0 AND 5),
  confirmed_at timestamptz,
  used_at timestamptz,
  revoked boolean NOT NULL DEFAULT false,
  CHECK (account_id IS NOT NULL OR purpose='password_reset'),
  CHECK ((purpose='password_reset' AND change_id IS NULL AND original_email=target_email) OR (purpose IN ('email_change_old','email_change_new') AND change_id IS NOT NULL AND original_email<>target_email)),
  CHECK (created_at < code_expires_at AND code_expires_at <= token_expires_at)
);
CREATE UNIQUE INDEX one_live_credential_purpose ON credential_challenges(account_id,purpose) WHERE NOT revoked AND used_at IS NULL;
CREATE UNIQUE INDEX one_change_purpose ON credential_challenges(change_id,purpose) WHERE change_id IS NOT NULL;
CREATE INDEX credential_target ON credential_challenges(target_email);
ALTER TABLE mail_deliveries ADD COLUMN kind text NOT NULL DEFAULT 'registration' CHECK (kind IN ('registration','credential','security_notice'));
ALTER TABLE mail_deliveries ADD COLUMN credential_challenge_id uuid REFERENCES credential_challenges(id);
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_proof_kind CHECK (
 (kind='registration' AND credential_challenge_id IS NULL) OR
 (kind='credential' AND challenge_id IS NULL AND credential_challenge_id IS NOT NULL) OR
 (kind='security_notice' AND challenge_id IS NULL AND credential_challenge_id IS NULL)
);

-- +goose Down
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_proof_kind;
ALTER TABLE mail_deliveries DROP COLUMN credential_challenge_id;
ALTER TABLE mail_deliveries DROP COLUMN kind;
DROP TABLE credential_challenges;
ALTER TABLE accounts DROP COLUMN credential_version;
