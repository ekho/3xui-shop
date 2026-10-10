-- +goose Up
ALTER TABLE access_operations DROP CONSTRAINT access_operations_kind_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_kind_check CHECK(kind IN ('compensate','assign_plan','starter_trial','reset_traffic','set_profile','set_vpn_ban','monthly_reset','purchase','group_reconcile'));
ALTER TABLE client_telegram_deliveries ADD COLUMN vpn_alert_code text,
 ADD COLUMN vpn_alert_account_id uuid REFERENCES accounts(id);
ALTER TABLE client_telegram_deliveries ADD CONSTRAINT vpn_alert_payload CHECK(
 (vpn_alert_code IS NULL AND vpn_alert_account_id IS NULL) OR
 (vpn_alert_code IS NOT NULL AND vpn_alert_code IN ('panel_unavailable','identity_mismatch','missing_client','unknown_profile','empty_membership','operation_needs_review') AND vpn_alert_account_id IS NOT NULL
  AND reminder_id IS NULL AND notice_action_id IS NULL AND route='cabinet'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM access_operations WHERE kind='group_reconcile')
 OR EXISTS(SELECT 1 FROM client_telegram_deliveries WHERE vpn_alert_code IS NOT NULL) THEN
  RAISE EXCEPTION 'group reconciliation downgrade blocked: retained operations or alerts';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE client_telegram_deliveries DROP CONSTRAINT vpn_alert_payload;
ALTER TABLE client_telegram_deliveries DROP COLUMN vpn_alert_code, DROP COLUMN vpn_alert_account_id;
ALTER TABLE access_operations DROP CONSTRAINT access_operations_kind_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_kind_check CHECK(kind IN ('compensate','assign_plan','starter_trial','reset_traffic','set_profile','set_vpn_ban','monthly_reset','purchase'));
