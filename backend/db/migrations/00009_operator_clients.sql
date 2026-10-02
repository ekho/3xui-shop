-- +goose Up
ALTER TABLE accounts
 ADD COLUMN kind text NOT NULL DEFAULT 'web',
 ADD COLUMN display_name text,
 ADD COLUMN created_at timestamptz;
ALTER TABLE accounts ALTER COLUMN created_at SET DEFAULT now();
ALTER TABLE accounts
 ALTER COLUMN email_key DROP NOT NULL,
 ALTER COLUMN password_hash DROP NOT NULL,
 ALTER COLUMN verified_at DROP NOT NULL,
 ALTER COLUMN terms_version DROP NOT NULL,
 ALTER COLUMN privacy_version DROP NOT NULL;
ALTER TABLE accounts
 ADD CONSTRAINT account_source CHECK (
  (kind='web' AND email_key IS NOT NULL AND email_key<>''
   AND password_hash IS NOT NULL AND password_hash<>''
   AND verified_at IS NOT NULL AND terms_version IS NOT NULL AND terms_version<>''
   AND privacy_version IS NOT NULL AND privacy_version<>'')
  OR
  (kind='telegram' AND email_key IS NULL AND password_hash IS NULL
   AND verified_at IS NULL AND terms_version IS NULL AND privacy_version IS NULL
   AND telegram_id IS NOT NULL AND telegram_id > 0 AND display_name IS NOT NULL
   AND char_length(display_name) BETWEEN 1 AND 128)
 ),
 ADD CONSTRAINT account_telegram_id_positive CHECK (telegram_id IS NULL OR telegram_id > 0);
ALTER TABLE trial_requests
 ADD COLUMN operator_account_id uuid REFERENCES accounts(id),
 DROP CONSTRAINT trial_requests_check;
ALTER TABLE trial_requests ADD CONSTRAINT trial_decision_actor CHECK (
 (status='pending' AND decided_at IS NULL AND operator_tg_id IS NULL
  AND operator_account_id IS NULL AND operation_id IS NULL)
 OR (status='rejected' AND decided_at IS NOT NULL AND operation_id IS NULL
  AND ((operator_tg_id IS NOT NULL AND operator_tg_id > 0 AND operator_account_id IS NULL)
       OR (operator_tg_id IS NULL AND operator_account_id IS NOT NULL)))
 OR (status='approved' AND decided_at IS NOT NULL AND operation_id IS NOT NULL
  AND ((operator_tg_id IS NOT NULL AND operator_tg_id > 0 AND operator_account_id IS NULL)
       OR (operator_tg_id IS NULL AND operator_account_id IS NOT NULL)))
);
CREATE INDEX operator_client_search ON accounts(created_at DESC NULLS LAST,id DESC);
CREATE INDEX operator_trial_history ON trial_requests(account_id,created_at DESC,id DESC);
CREATE INDEX operator_audit_history ON audit_events(account_id,created_at DESC,id DESC);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM accounts WHERE kind='telegram')
    OR EXISTS(SELECT 1 FROM trial_requests WHERE operator_account_id IS NOT NULL) THEN
  RAISE EXCEPTION 'operator client downgrade blocked: source or web decision data exists';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX operator_audit_history,operator_trial_history,operator_client_search;
ALTER TABLE trial_requests DROP CONSTRAINT trial_decision_actor, DROP COLUMN operator_account_id;
ALTER TABLE trial_requests ADD CONSTRAINT trial_requests_check CHECK (
 (status='pending' AND decided_at IS NULL AND operator_tg_id IS NULL AND operation_id IS NULL)
 OR (status='rejected' AND decided_at IS NOT NULL AND operator_tg_id>0 AND operation_id IS NULL)
 OR (status='approved' AND decided_at IS NOT NULL AND operator_tg_id>0 AND operation_id IS NOT NULL)
);
ALTER TABLE accounts DROP CONSTRAINT account_telegram_id_positive, DROP CONSTRAINT account_source;
ALTER TABLE accounts
 ALTER COLUMN email_key SET NOT NULL,
 ALTER COLUMN password_hash SET NOT NULL,
 ALTER COLUMN verified_at SET NOT NULL,
 ALTER COLUMN terms_version SET NOT NULL,
 ALTER COLUMN privacy_version SET NOT NULL;
ALTER TABLE accounts DROP COLUMN kind, DROP COLUMN display_name, DROP COLUMN created_at;
