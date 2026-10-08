-- +goose Up
ALTER TABLE trial_requests
 ADD COLUMN decision_source text NOT NULL DEFAULT 'operator'
 CHECK (decision_source IN ('operator','telegram_auto')),
 DROP CONSTRAINT trial_decision_actor;
ALTER TABLE trial_requests ADD CONSTRAINT trial_decision_actor CHECK (
 (decision_source='operator' AND (
  (status='pending' AND decided_at IS NULL AND operator_tg_id IS NULL
   AND operator_account_id IS NULL AND operation_id IS NULL)
  OR (status IN ('approved','rejected') AND decided_at IS NOT NULL
   AND ((status='approved' AND operation_id IS NOT NULL)
        OR (status='rejected' AND operation_id IS NULL))
   AND ((operator_tg_id IS NOT NULL AND operator_tg_id>0 AND operator_account_id IS NULL)
        OR (operator_tg_id IS NULL AND operator_account_id IS NOT NULL)))
 ))
 OR (decision_source='telegram_auto' AND status='approved' AND decided_at IS NOT NULL
  AND operation_id IS NOT NULL AND operator_tg_id IS NULL AND operator_account_id IS NULL)
);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM trial_requests WHERE decision_source='telegram_auto') THEN
  RAISE EXCEPTION 'trial downgrade blocked: automatic trial facts exist';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE trial_requests DROP CONSTRAINT trial_decision_actor,DROP COLUMN decision_source;
ALTER TABLE trial_requests ADD CONSTRAINT trial_decision_actor CHECK (
 (status='pending' AND decided_at IS NULL AND operator_tg_id IS NULL
  AND operator_account_id IS NULL AND operation_id IS NULL)
 OR (status='rejected' AND decided_at IS NOT NULL AND operation_id IS NULL
  AND ((operator_tg_id IS NOT NULL AND operator_tg_id>0 AND operator_account_id IS NULL)
       OR (operator_tg_id IS NULL AND operator_account_id IS NOT NULL)))
 OR (status='approved' AND decided_at IS NOT NULL AND operation_id IS NOT NULL
  AND ((operator_tg_id IS NOT NULL AND operator_tg_id>0 AND operator_account_id IS NULL)
       OR (operator_tg_id IS NULL AND operator_account_id IS NOT NULL)))
);
