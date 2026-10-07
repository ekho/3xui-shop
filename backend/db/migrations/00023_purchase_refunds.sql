-- +goose Up
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_receipt_order_key UNIQUE(order_id,operation_id);
CREATE TABLE purchase_refunds (
 id uuid PRIMARY KEY,
 order_id uuid NOT NULL REFERENCES purchase_orders(id),
 receipt_operation_id text NOT NULL UNIQUE,
 payment_method text NOT NULL CHECK(payment_method IN ('yoomoney','manual','yookassa','cryptomus','heleket')),
 reference text NOT NULL CHECK(reference ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
 returned_amount text NOT NULL CHECK(returned_amount ~ '^[0-9]{1,40}(\.[0-9]{1,40})?$' AND returned_amount::numeric>0),
 returned_currency text NOT NULL CHECK(returned_currency ~ '^[A-Z0-9]{1,16}$'),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 1 AND 1000 AND char_length(btrim(reason))>0),
 operator_account_id uuid NOT NULL REFERENCES accounts(id),
 created_at timestamptz NOT NULL,
 FOREIGN KEY(order_id,receipt_operation_id) REFERENCES purchase_receipts(order_id,operation_id),
 UNIQUE(payment_method,reference)
);
CREATE INDEX purchase_refunds_history ON purchase_refunds(order_id,created_at DESC,id DESC);
-- +goose StatementBegin
CREATE FUNCTION purchase_refund_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'confirmed refund history is immutable';
END $$;
-- +goose StatementEnd
CREATE TRIGGER purchase_refund_immutable BEFORE UPDATE OR DELETE ON purchase_refunds FOR EACH ROW EXECUTE FUNCTION purchase_refund_immutable();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM purchase_refunds) THEN
  RAISE EXCEPTION 'refund downgrade blocked: retained confirmed refunds';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE purchase_refunds;
DROP FUNCTION purchase_refund_immutable();
ALTER TABLE purchase_receipts DROP CONSTRAINT purchase_receipt_order_key;
