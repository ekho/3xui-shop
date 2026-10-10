-- +goose Up
CREATE TABLE legacy_payment_receipts (
 id uuid PRIMARY KEY,
 provider text NOT NULL CHECK(provider IN ('yoomoney','yookassa','cryptomus','heleket','telegram_stars')),
 event_kind text NOT NULL CHECK(event_kind IN ('paid','recurring','refunded','refund_observed')),
 source_id text NOT NULL CHECK(octet_length(source_id) BETWEEN 1 AND 4096),
 source_reference text NOT NULL CHECK(octet_length(source_reference) <= 16384),
 account_id uuid REFERENCES accounts(id),
 source_transaction_id bigint REFERENCES legacy_payment_transactions(source_id),
 amount_minor bigint CHECK(amount_minor IS NULL OR amount_minor > 0),
 currency text CHECK(currency IS NULL OR currency IN ('RUB','USD','XTR')),
 occurred_at timestamptz NOT NULL,
 proof jsonb NOT NULL CHECK(jsonb_typeof(proof)='object'),
 state text NOT NULL CHECK(state IN ('review','conflict')),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(provider,event_kind,source_id),
 CHECK ((amount_minor IS NULL) = (currency IS NULL))
);
CREATE INDEX legacy_payment_receipts_review ON legacy_payment_receipts(created_at,id);
-- +goose StatementBegin
CREATE FUNCTION legacy_payment_receipt_proof_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR ROW(NEW.provider,NEW.event_kind,NEW.source_id,NEW.source_reference,NEW.account_id,NEW.source_transaction_id,NEW.amount_minor,NEW.currency,NEW.occurred_at,NEW.proof,NEW.created_at)
   IS DISTINCT FROM ROW(OLD.provider,OLD.event_kind,OLD.source_id,OLD.source_reference,OLD.account_id,OLD.source_transaction_id,OLD.amount_minor,OLD.currency,OLD.occurred_at,OLD.proof,OLD.created_at) THEN
  RAISE EXCEPTION 'legacy payment receipt proof is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER legacy_payment_receipt_proof_guard BEFORE UPDATE OR DELETE ON legacy_payment_receipts
 FOR EACH ROW EXECUTE FUNCTION legacy_payment_receipt_proof_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM legacy_payment_receipts) THEN
  RAISE EXCEPTION 'legacy payment receipt downgrade blocked: retained financial records';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE legacy_payment_receipts;
DROP FUNCTION legacy_payment_receipt_proof_guard();
