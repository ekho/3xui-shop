-- +goose Up
CREATE TABLE purchase_orders (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 idempotency_key uuid NOT NULL,
 body_hash bytea NOT NULL,
 quote jsonb NOT NULL CHECK(jsonb_typeof(quote)='object'),
 amount_minor bigint NOT NULL CHECK(amount_minor>0),
 payment_type text NOT NULL CHECK(payment_type IN ('AC','PC')),
 payment_status text NOT NULL DEFAULT 'pending' CHECK(payment_status IN ('pending','paid','canceled')),
 fulfillment_status text NOT NULL DEFAULT 'not_started' CHECK(fulfillment_status IN ('not_started','queued','running','applied','needs_review')),
 active boolean NOT NULL DEFAULT true,
 review_required boolean NOT NULL DEFAULT false,
 review_reason text,
 access_operation_id uuid UNIQUE REFERENCES access_operations(id),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 paid_at timestamptz,
 UNIQUE(account_id,idempotency_key),
 CHECK(expires_at>created_at),
 CHECK(payment_status<>'paid' OR paid_at IS NOT NULL)
);
CREATE UNIQUE INDEX purchase_one_open_account ON purchase_orders(account_id) WHERE active;
CREATE INDEX purchase_current_account ON purchase_orders(account_id,created_at DESC,id DESC);
CREATE TABLE purchase_receipts (
 operation_id text PRIMARY KEY CHECK(char_length(operation_id) BETWEEN 1 AND 128),
 order_id uuid NOT NULL REFERENCES purchase_orders(id),
 occurred_at timestamptz NOT NULL,
 gross_minor bigint NOT NULL CHECK(gross_minor>=0),
 net_minor bigint NOT NULL CHECK(net_minor>=0),
 currency text NOT NULL CHECK(char_length(currency) BETWEEN 1 AND 16),
 notification_type text NOT NULL CHECK(char_length(notification_type) BETWEEN 1 AND 64),
 codepro boolean NOT NULL,
 unaccepted boolean NOT NULL,
 review_reason text,
 created_at timestamptz NOT NULL
);
CREATE INDEX purchase_receipts_order ON purchase_receipts(order_id);
ALTER TABLE purchase_orders ADD COLUMN funding_operation_id text UNIQUE REFERENCES purchase_receipts(operation_id);
-- +goose StatementBegin
CREATE FUNCTION purchase_quote_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.account_id,NEW.idempotency_key,NEW.body_hash,NEW.quote,NEW.amount_minor,NEW.payment_type,NEW.created_at,NEW.expires_at)
    IS DISTINCT FROM (OLD.account_id,OLD.idempotency_key,OLD.body_hash,OLD.quote,OLD.amount_minor,OLD.payment_type,OLD.created_at,OLD.expires_at)
    OR (OLD.funding_operation_id IS NOT NULL AND NEW.funding_operation_id IS DISTINCT FROM OLD.funding_operation_id)
    OR (OLD.payment_status='paid' AND NEW.payment_status<>'paid') THEN
  RAISE EXCEPTION 'purchase quote or funding is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER purchase_quote_immutable BEFORE UPDATE ON purchase_orders FOR EACH ROW EXECUTE FUNCTION purchase_quote_immutable();
-- +goose StatementBegin
CREATE FUNCTION purchase_receipt_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.operation_id,NEW.order_id,NEW.occurred_at,NEW.gross_minor,NEW.net_minor,NEW.currency,NEW.notification_type,NEW.codepro,NEW.unaccepted,NEW.created_at)
    IS DISTINCT FROM (OLD.operation_id,OLD.order_id,OLD.occurred_at,OLD.gross_minor,OLD.net_minor,OLD.currency,OLD.notification_type,OLD.codepro,OLD.unaccepted,OLD.created_at) THEN
  RAISE EXCEPTION 'purchase receipt is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER purchase_receipt_immutable BEFORE UPDATE ON purchase_receipts FOR EACH ROW EXECUTE FUNCTION purchase_receipt_immutable();
ALTER TABLE access_operations DROP CONSTRAINT access_operations_kind_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_kind_check CHECK(kind IN ('compensate','assign_plan','starter_trial','reset_traffic','set_profile','set_vpn_ban','monthly_reset','purchase'));
ALTER TABLE access_operations DROP CONSTRAINT access_operations_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_check CHECK (
 (kind IN ('assign_plan','purchase') AND plan_id IS NOT NULL AND plan_revision>0 AND period_days>0)
 OR (kind='set_profile' AND ((plan_id IS NULL AND plan_revision IS NULL AND period_days IS NULL) OR (plan_id IS NOT NULL AND plan_revision>0 AND period_days IS NULL)))
 OR (kind NOT IN ('assign_plan','purchase','set_profile') AND plan_id IS NULL AND plan_revision IS NULL AND period_days IS NULL));
ALTER TABLE access_operations ADD COLUMN purchase_order_id uuid UNIQUE REFERENCES purchase_orders(id);
ALTER TABLE access_operations ADD CONSTRAINT access_purchase_provenance CHECK((kind='purchase')=(purchase_order_id IS NOT NULL));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM purchase_orders) OR EXISTS(SELECT 1 FROM purchase_receipts) OR EXISTS(SELECT 1 FROM access_operations WHERE kind='purchase') THEN
  RAISE EXCEPTION 'purchase downgrade blocked: retained money or access history';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE access_operations DROP CONSTRAINT access_purchase_provenance;
ALTER TABLE access_operations DROP COLUMN purchase_order_id;
ALTER TABLE access_operations DROP CONSTRAINT access_operations_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_check CHECK (
 (kind='assign_plan' AND plan_id IS NOT NULL AND plan_revision>0 AND period_days>0)
 OR (kind='set_profile' AND ((plan_id IS NULL AND plan_revision IS NULL AND period_days IS NULL) OR (plan_id IS NOT NULL AND plan_revision>0 AND period_days IS NULL)))
 OR (kind NOT IN ('assign_plan','set_profile') AND plan_id IS NULL AND plan_revision IS NULL AND period_days IS NULL));
ALTER TABLE access_operations DROP CONSTRAINT access_operations_kind_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_kind_check CHECK(kind IN ('compensate','assign_plan','starter_trial','reset_traffic','set_profile','set_vpn_ban','monthly_reset'));
ALTER TABLE purchase_orders DROP COLUMN funding_operation_id;
DROP FUNCTION purchase_quote_immutable() CASCADE;
DROP FUNCTION purchase_receipt_immutable() CASCADE;
DROP TABLE purchase_receipts;
DROP TABLE purchase_orders;
