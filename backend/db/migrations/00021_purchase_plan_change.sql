-- +goose Up
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_action_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_action_check
 CHECK(action IN ('purchase','renew','change_plan'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM purchase_orders WHERE action='change_plan') THEN
  RAISE EXCEPTION 'plan change history requires compatible application';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_action_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_action_check
 CHECK(action IN ('purchase','renew'));
