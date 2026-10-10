package payments

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type legacyReceipt struct {
	provider, kind, sourceID, reference, currency string
	account                                       *uuid.UUID
	amount                                        *int64
	at                                            time.Time
	proof                                         any
}

// A verified legacy event is only a review fact. It never funds a native order.
func (s *Service) retainLegacyReceipt(ctx context.Context, r legacyReceipt) error {
	proof, err := json.Marshal(r.proof)
	if err != nil || r.sourceID == "" || r.at.IsZero() {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	var source *int64
	var linked uuid.UUID
	hash := sha256.Sum256([]byte(r.reference))
	err = tx.QueryRow(ctx, `SELECT source_id,account_id FROM legacy_payment_transactions WHERE payment_id_hash=$1`, hash[:]).Scan(&source, &linked)
	if err == nil {
		if r.account != nil && *r.account != linked {
			return failure(409, "PAYMENT_IDENTITY_CONFLICT")
		}
		r.account = &linked
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return unavailable()
	}
	id := uuid.New()
	result, err := tx.Exec(ctx, `INSERT INTO legacy_payment_receipts(id,provider,event_kind,source_id,source_reference,account_id,source_transaction_id,amount_minor,currency,occurred_at,proof,state)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,'review') ON CONFLICT(provider,event_kind,source_id) DO NOTHING`,
		id, r.provider, r.kind, r.sourceID, r.reference, r.account, source, r.amount, r.currency, r.at.UTC().Truncate(time.Microsecond), proof)
	if err != nil {
		return unavailable()
	}
	if result.RowsAffected() == 0 {
		var oldReference, oldCurrency string
		var oldAccount *uuid.UUID
		var oldSource *int64
		var oldAmount *int64
		var oldAt time.Time
		var sameProof bool
		err = tx.QueryRow(ctx, `SELECT source_reference,account_id,source_transaction_id,amount_minor,COALESCE(currency,''),occurred_at,proof=$4::jsonb FROM legacy_payment_receipts WHERE provider=$1 AND event_kind=$2 AND source_id=$3 FOR UPDATE`, r.provider, r.kind, r.sourceID, proof).
			Scan(&oldReference, &oldAccount, &oldSource, &oldAmount, &oldCurrency, &oldAt, &sameProof)
		if err != nil {
			return unavailable()
		}
		if oldReference != r.reference || !sameUUID(oldAccount, r.account) || !sameInt64(oldSource, source) || !sameInt64(oldAmount, r.amount) || oldCurrency != r.currency || !oldAt.Equal(r.at.UTC().Truncate(time.Microsecond)) || !sameProof {
			if _, err = tx.Exec(ctx, `UPDATE legacy_payment_receipts SET state='conflict' WHERE provider=$1 AND event_kind=$2 AND source_id=$3`, r.provider, r.kind, r.sourceID); err != nil {
				return unavailable()
			}
		}
	} else if r.account != nil {
		reason := "Verified legacy payment retained for review"
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: *r.account, CreatedAt: s.now(), Action: "legacy_payment_receipt_received", Reason: &reason}); err != nil {
			return unavailable()
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}

func sameUUID(a, b *uuid.UUID) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func sameInt64(a, b *int64) bool    { return a == nil && b == nil || a != nil && b != nil && *a == *b }

type LegacyReceiptReportRow struct {
	ID          uuid.UUID  `json:"id"`
	Provider    string     `json:"provider"`
	Kind        string     `json:"kind"`
	AmountMinor *int64     `json:"amount_minor"`
	Currency    *string    `json:"currency"`
	State       string     `json:"state"`
	AccountID   *uuid.UUID `json:"account_id"`
}

// ListLegacyReceipts is an offline/operator port; it exposes no provider IDs or proof.
func (s *Service) ListLegacyReceipts(ctx context.Context, actor uuid.UUID) ([]LegacyReceiptReportRow, error) {
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,provider,event_kind,amount_minor,currency,state,account_id FROM legacy_payment_receipts ORDER BY created_at,id`)
	if err != nil {
		return nil, unavailable()
	}
	defer rows.Close()
	out := []LegacyReceiptReportRow{}
	for rows.Next() {
		var item LegacyReceiptReportRow
		if rows.Scan(&item.ID, &item.Provider, &item.Kind, &item.AmountMinor, &item.Currency, &item.State, &item.AccountID) != nil {
			return nil, unavailable()
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		return nil, unavailable()
	}
	return out, nil
}
