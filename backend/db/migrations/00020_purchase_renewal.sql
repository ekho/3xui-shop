-- +goose Up
ALTER TABLE purchase_orders ADD COLUMN action text NOT NULL DEFAULT 'purchase'
 CHECK(action IN ('purchase','renew'));

-- +goose StatementBegin
CREATE FUNCTION purchase_action_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.action IS DISTINCT FROM OLD.action THEN
  RAISE EXCEPTION 'purchase action is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER purchase_action_immutable BEFORE UPDATE ON purchase_orders
 FOR EACH ROW EXECUTE FUNCTION purchase_action_immutable();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM purchase_orders WHERE action='renew') THEN
  RAISE EXCEPTION 'renewal history requires compatible application';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER purchase_action_immutable ON purchase_orders;
DROP FUNCTION purchase_action_immutable();
ALTER TABLE purchase_orders DROP COLUMN action;
