-- +goose Up
ALTER TABLE accounts
 ADD COLUMN original_kind text CHECK (original_kind IS NULL OR (original_kind='telegram' AND kind='web')),
 ADD COLUMN telegram_login_disabled boolean NOT NULL DEFAULT false,
 ADD CONSTRAINT account_telegram_quarantine CHECK (NOT telegram_login_disabled OR kind='telegram');

CREATE TABLE telegram_identity_reservations (
 telegram_id bigint PRIMARY KEY CHECK (telegram_id>0),
 account_id uuid NOT NULL REFERENCES accounts(id),
 retired_at timestamptz NOT NULL
);
CREATE INDEX telegram_reservation_account ON telegram_identity_reservations(account_id);

ALTER TABLE credential_challenges
 DROP CONSTRAINT credential_challenges_purpose_check,
 DROP CONSTRAINT credential_challenges_check1,
 ADD COLUMN requested_by uuid REFERENCES accounts(id),
 ADD CONSTRAINT credential_challenges_purpose_check CHECK (purpose IN
  ('password_reset','email_change_old','email_change_new','initial_email','telegram_link','identity_recovery')),
 ADD CONSTRAINT credential_identity_actor CHECK ((purpose='identity_recovery')=(requested_by IS NOT NULL)),
 ADD CONSTRAINT credential_purpose_pair CHECK (
  (purpose IN ('password_reset','telegram_link') AND change_id IS NULL AND original_email=target_email)
  OR (purpose IN ('email_change_old','email_change_new') AND change_id IS NOT NULL AND original_email<>target_email)
  OR (purpose IN ('initial_email','identity_recovery') AND change_id IS NULL AND original_email='' AND target_email<>'')
 );

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM accounts WHERE original_kind IS NOT NULL OR telegram_login_disabled)
  OR EXISTS(SELECT 1 FROM telegram_identity_reservations)
  OR EXISTS(SELECT 1 FROM credential_challenges WHERE purpose IN ('initial_email','telegram_link','identity_recovery')) THEN
  RAISE EXCEPTION 'account identity downgrade blocked: identity data exists';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE credential_challenges
 DROP CONSTRAINT credential_purpose_pair,
 DROP CONSTRAINT credential_identity_actor,
 DROP CONSTRAINT credential_challenges_purpose_check,
 DROP COLUMN requested_by,
 ADD CONSTRAINT credential_challenges_purpose_check CHECK (purpose IN ('password_reset','email_change_old','email_change_new')),
 ADD CONSTRAINT credential_challenges_check1 CHECK (
  (purpose='password_reset' AND change_id IS NULL AND original_email=target_email)
  OR (purpose IN ('email_change_old','email_change_new') AND change_id IS NOT NULL AND original_email<>target_email)
 );
DROP TABLE telegram_identity_reservations;
ALTER TABLE accounts DROP CONSTRAINT account_telegram_quarantine, DROP COLUMN original_kind, DROP COLUMN telegram_login_disabled;
