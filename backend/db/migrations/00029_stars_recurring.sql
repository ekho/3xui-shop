-- +goose Up
ALTER TABLE stars_checkouts ADD COLUMN subscription_period bigint NOT NULL DEFAULT 0 CHECK(subscription_period IN (0,2592000));
ALTER TABLE stars_checkouts ADD COLUMN pre_checkout_id text CHECK(pre_checkout_id IS NULL OR octet_length(pre_checkout_id) BETWEEN 1 AND 128);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION stars_checkout_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' OR (NEW.order_id,NEW.bot_id,NEW.payer_id,NEW.payload,NEW.subscription_period)
 IS DISTINCT FROM (OLD.order_id,OLD.bot_id,OLD.payer_id,OLD.payload,OLD.subscription_period)
 OR OLD.invoice_url IS NOT NULL AND NEW.invoice_url IS DISTINCT FROM OLD.invoice_url
 OR OLD.pre_checkout_id IS NOT NULL AND NEW.pre_checkout_id IS DISTINCT FROM OLD.pre_checkout_id THEN
  RAISE EXCEPTION 'Stars invoice provenance is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TABLE stars_subscriptions (
 first_receipt_id text PRIMARY KEY REFERENCES purchase_receipts(operation_id),
 root_order_id uuid NOT NULL REFERENCES stars_checkouts(order_id),
 bot_id bigint NOT NULL CHECK(bot_id>0),
 payer_id bigint NOT NULL CHECK(payer_id>0 AND payer_id<=4503599627370495),
 first_charge_id text NOT NULL CHECK(octet_length(first_charge_id) BETWEEN 1 AND 4096),
 canonical boolean NOT NULL,
 provider_state text NOT NULL CHECK(provider_state IN ('active','canceled','failed','unknown')),
 paid_until timestamptz,
 current_cycle_order_id uuid REFERENCES purchase_orders(id),
 desired_action text NOT NULL DEFAULT 'none' CHECK(desired_action IN ('none','cancel','resume')),
 control_state text NOT NULL DEFAULT 'none' CHECK(control_state IN ('none','pending','uncertain','confirmed','rejected')),
 bot_canceled boolean NOT NULL DEFAULT false,
 latest_control_id uuid,
 native_proof jsonb CHECK(native_proof IS NULL OR jsonb_typeof(native_proof)='object'),
 created_at timestamptz NOT NULL,
 UNIQUE(bot_id,first_charge_id),
 CHECK(first_receipt_id='stars:'||encode(sha256(convert_to(bot_id::text||':'||first_charge_id,'UTF8')),'hex'))
);
CREATE UNIQUE INDEX stars_subscription_canonical ON stars_subscriptions(root_order_id) WHERE canonical;
CREATE INDEX stars_subscription_root ON stars_subscriptions(root_order_id);
CREATE TABLE stars_subscription_cycles (
 order_id uuid PRIMARY KEY REFERENCES purchase_orders(id),
 root_order_id uuid NOT NULL REFERENCES stars_checkouts(order_id),
 subscription_receipt_id text NOT NULL REFERENCES stars_subscriptions(first_receipt_id),
 receipt_id text NOT NULL UNIQUE REFERENCES purchase_receipts(operation_id),
 paid_until timestamptz NOT NULL,
 previous_access_operation_id uuid REFERENCES access_operations(id),
 UNIQUE(root_order_id,paid_until)
);
CREATE TABLE stars_subscription_controls (
 id uuid PRIMARY KEY,
 subscription_receipt_id text NOT NULL REFERENCES stars_subscriptions(first_receipt_id),
 idempotency_key uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('cancel','resume')),
 reason text NOT NULL CHECK(char_length(btrim(reason)) BETWEEN 1 AND 1000),
 source text NOT NULL CHECK(source IN ('client','policy')),
 actor_id uuid REFERENCES accounts(id),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','uncertain','confirmed','rejected')),
 proof jsonb CHECK(proof IS NULL OR jsonb_typeof(proof)='object'),
 error_code text CHECK(error_code IS NULL OR error_code ~ '^[A-Z_]{1,64}$'),
 created_at timestamptz NOT NULL,
 finished_at timestamptz,
 UNIQUE(subscription_receipt_id,idempotency_key),
 CHECK((state='confirmed' AND proof IS NOT NULL AND finished_at IS NOT NULL) OR (state<>'confirmed' AND proof IS NULL))
);
ALTER TABLE stars_subscriptions ADD CONSTRAINT stars_subscription_latest_control FOREIGN KEY(latest_control_id) REFERENCES stars_subscription_controls(id);
-- +goose StatementBegin
CREATE FUNCTION stars_subscription_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' OR (NEW.first_receipt_id,NEW.root_order_id,NEW.bot_id,NEW.payer_id,NEW.first_charge_id,NEW.canonical,NEW.created_at)
 IS DISTINCT FROM (OLD.first_receipt_id,OLD.root_order_id,OLD.bot_id,OLD.payer_id,OLD.first_charge_id,OLD.canonical,OLD.created_at) THEN
  RAISE EXCEPTION 'Stars subscription provenance is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION stars_cycle_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 RAISE EXCEPTION 'Stars cycle provenance is immutable';
END $$;
CREATE FUNCTION stars_control_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' OR (NEW.id,NEW.subscription_receipt_id,NEW.idempotency_key,NEW.action,NEW.reason,NEW.source,NEW.actor_id,NEW.created_at)
 IS DISTINCT FROM (OLD.id,OLD.subscription_receipt_id,OLD.idempotency_key,OLD.action,OLD.reason,OLD.source,OLD.actor_id,OLD.created_at)
 OR OLD.state='confirmed' AND (NEW.state,NEW.proof,NEW.error_code,NEW.finished_at) IS DISTINCT FROM (OLD.state,OLD.proof,OLD.error_code,OLD.finished_at) THEN
  RAISE EXCEPTION 'Stars control provenance is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stars_subscription_immutable BEFORE UPDATE OR DELETE ON stars_subscriptions FOR EACH ROW EXECUTE FUNCTION stars_subscription_immutable();
CREATE TRIGGER stars_cycle_immutable BEFORE UPDATE OR DELETE ON stars_subscription_cycles FOR EACH ROW EXECUTE FUNCTION stars_cycle_immutable();
CREATE TRIGGER stars_control_immutable BEFORE UPDATE OR DELETE ON stars_subscription_controls FOR EACH ROW EXECUTE FUNCTION stars_control_immutable();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stars_checkouts WHERE subscription_period<>0 OR pre_checkout_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM stars_subscriptions) OR EXISTS(SELECT 1 FROM stars_subscription_cycles)
 OR EXISTS(SELECT 1 FROM stars_subscription_controls) THEN
  RAISE EXCEPTION 'Stars recurring downgrade blocked: retained billing provenance';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION stars_checkout_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' OR (NEW.order_id,NEW.bot_id,NEW.payer_id,NEW.payload)
 IS DISTINCT FROM (OLD.order_id,OLD.bot_id,OLD.payer_id,OLD.payload)
 OR OLD.invoice_url IS NOT NULL AND NEW.invoice_url IS DISTINCT FROM OLD.invoice_url THEN
  RAISE EXCEPTION 'Stars invoice provenance is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE stars_subscriptions DROP CONSTRAINT stars_subscription_latest_control;
DROP TABLE stars_subscription_controls,stars_subscription_cycles,stars_subscriptions;
DROP FUNCTION stars_control_immutable(),stars_cycle_immutable(),stars_subscription_immutable();
ALTER TABLE stars_checkouts DROP COLUMN subscription_period,DROP COLUMN pre_checkout_id;

