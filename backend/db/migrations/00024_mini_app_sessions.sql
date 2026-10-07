-- +goose Up
ALTER TABLE sessions ADD COLUMN auth_source text NOT NULL DEFAULT 'web', ADD COLUMN telegram_id bigint;
ALTER TABLE sessions ADD CONSTRAINT session_source CHECK (
 (auth_source='web' AND telegram_id IS NULL) OR (auth_source='telegram' AND telegram_id>0 AND telegram_id IS NOT NULL)
);
ALTER TABLE accounts ADD COLUMN policy_accepted_at timestamptz,
 ADD COLUMN telegram_start_param text CHECK (telegram_start_param IS NULL OR (char_length(telegram_start_param) BETWEEN 1 AND 512 AND telegram_start_param ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE accounts DROP CONSTRAINT account_source;
ALTER TABLE accounts ADD CONSTRAINT account_source CHECK (
 (kind='web' AND email_key IS NOT NULL AND email_key<>'' AND password_hash IS NOT NULL AND password_hash<>''
  AND verified_at IS NOT NULL AND terms_version IS NOT NULL AND terms_version<>'' AND privacy_version IS NOT NULL AND privacy_version<>'')
 OR (kind='telegram' AND email_key IS NULL AND password_hash IS NULL AND verified_at IS NULL
  AND telegram_id IS NOT NULL AND telegram_id>0 AND display_name IS NOT NULL AND char_length(display_name) BETWEEN 1 AND 128
  AND ((terms_version IS NULL AND privacy_version IS NULL AND policy_accepted_at IS NULL)
   OR (terms_version IS NOT NULL AND terms_version<>'' AND privacy_version IS NOT NULL AND privacy_version<>'' AND policy_accepted_at IS NOT NULL)))
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM sessions WHERE auth_source='telegram')
 OR EXISTS(SELECT 1 FROM accounts WHERE policy_accepted_at IS NOT NULL OR telegram_start_param IS NOT NULL) THEN
  RAISE EXCEPTION 'Mini App downgrade blocked: consent, attribution or Telegram sessions exist';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE accounts DROP CONSTRAINT account_source;
ALTER TABLE accounts ADD CONSTRAINT account_source CHECK (
 (kind='web' AND email_key IS NOT NULL AND email_key<>'' AND password_hash IS NOT NULL AND password_hash<>''
  AND verified_at IS NOT NULL AND terms_version IS NOT NULL AND terms_version<>'' AND privacy_version IS NOT NULL AND privacy_version<>'')
 OR (kind='telegram' AND email_key IS NULL AND password_hash IS NULL AND verified_at IS NULL
  AND terms_version IS NULL AND privacy_version IS NULL AND telegram_id IS NOT NULL AND telegram_id>0
  AND display_name IS NOT NULL AND char_length(display_name) BETWEEN 1 AND 128)
);
ALTER TABLE accounts DROP COLUMN policy_accepted_at, DROP COLUMN telegram_start_param;
ALTER TABLE sessions DROP CONSTRAINT session_source, DROP COLUMN auth_source, DROP COLUMN telegram_id;
