package payments

import (
	"context"
	"crypto/sha256"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type LegacyPaymentPackage struct {
	Version      int                        `json:"version"`
	Users        []LegacyPaymentUser        `json:"users"`
	Transactions []LegacyPaymentTransaction `json:"transactions"`
}
type LegacyPaymentUser struct {
	SourceLegacyUserID int64 `json:"source_legacy_user_id"`
	SourceTgID         int64 `json:"source_tg_id"`
}
type LegacyPaymentTransaction struct {
	SourceID     int64     `json:"source_id"`
	SourceTgID   int64     `json:"source_tg_id"`
	PaymentID    string    `json:"payment_id"`
	Subscription string    `json:"subscription"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type LegacyPaymentImportResult struct {
	Users        int `json:"users"`
	Transactions int `json:"transactions"`
	Inserted     int `json:"inserted"`
}

func (s *Service) ImportLegacyPayments(ctx context.Context, p LegacyPaymentPackage, dryRun bool) (LegacyPaymentImportResult, error) {
	var result LegacyPaymentImportResult
	if err := validateLegacyPayments(p); err != nil {
		return result, err
	}
	opts := pgx.TxOptions{}
	if dryRun {
		opts.AccessMode = pgx.ReadOnly
	}
	tx, err := s.pool.BeginTx(ctx, opts)
	if err != nil {
		return result, unavailable()
	}
	defer tx.Rollback(ctx)
	result, err = s.ImportLegacyPaymentsTx(ctx, tx, p, dryRun)
	if err != nil || dryRun {
		return result, err
	}
	if tx.Commit(ctx) != nil {
		return result, unavailable()
	}
	return result, nil
}

// ImportLegacyPaymentsTx resolves identities on the caller's transaction.
func (s *Service) ImportLegacyPaymentsTx(ctx context.Context, tx pgx.Tx, p LegacyPaymentPackage, dryRun bool) (LegacyPaymentImportResult, error) {
	var result LegacyPaymentImportResult
	if tx == nil {
		return result, unavailable()
	}
	if err := validateLegacyPayments(p); err != nil {
		return result, err
	}
	users := make(map[int64]LegacyPaymentUser, len(p.Users))
	tgIDs := make([]int64, 0, len(p.Users))
	for _, u := range p.Users {
		users[u.SourceTgID] = u
		tgIDs = append(tgIDs, u.SourceTgID)
	}
	locatedUsers, err := s.authority.LegacyIdentitiesTx(ctx, tx, tgIDs, !dryRun)
	if err != nil {
		return result, unavailable()
	}
	if len(locatedUsers) != len(p.Users) {
		return result, failure(409, "IMPORT_IDENTITY_CONFLICT")
	}
	ids := make(map[int64]uuid.UUID, len(p.Users))
	for _, a := range locatedUsers {
		if a.TelegramID == nil {
			return result, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		u := users[*a.TelegramID]
		if *a.TelegramID != u.SourceTgID || a.LegacyUserID == nil || *a.LegacyUserID != u.SourceLegacyUserID {
			return result, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		ids[u.SourceTgID] = a.ID
	}
	result.Users = len(p.Users)
	result.Transactions = len(p.Transactions)
	changed := make(map[uuid.UUID]int)
	now := s.now()
	for _, source := range p.Transactions {
		u, id := users[source.SourceTgID], ids[source.SourceTgID]
		hash := sha256.Sum256([]byte(source.PaymentID))
		rows, err := tx.Query(ctx, `SELECT source_id,account_id,source_legacy_user_id,source_tg_id,source_payment_id,subscription,status,created_at,updated_at FROM legacy_payment_transactions WHERE source_id=$1 OR payment_id_hash=$2`, source.SourceID, hash[:])
		if err != nil {
			return result, unavailable()
		}
		found := false
		conflict := false
		for rows.Next() {
			var previous LegacyPaymentTransaction
			var account uuid.UUID
			var legacy int64
			if rows.Scan(&previous.SourceID, &account, &legacy, &previous.SourceTgID, &previous.PaymentID, &previous.Subscription, &previous.Status, &previous.CreatedAt, &previous.UpdatedAt) != nil {
				rows.Close()
				return result, unavailable()
			}
			if found || previous.SourceID != source.SourceID || account != id || legacy != u.SourceLegacyUserID || previous.SourceTgID != source.SourceTgID || previous.PaymentID != source.PaymentID || previous.Subscription != source.Subscription || previous.Status != source.Status || !previous.CreatedAt.Equal(source.CreatedAt) || !previous.UpdatedAt.Equal(source.UpdatedAt) {
				conflict = true
			}
			found = true
		}
		readErr := rows.Err()
		rows.Close()
		if readErr != nil {
			return result, unavailable()
		}
		if conflict {
			return result, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		if found {
			continue
		}
		// In dry-run Inserted is the number that apply would insert, not writes.
		result.Inserted++
		changed[id]++
		if !dryRun {
			if _, err = tx.Exec(ctx, `INSERT INTO legacy_payment_transactions(source_id,account_id,source_legacy_user_id,source_tg_id,source_payment_id,payment_id_hash,subscription,status,created_at,updated_at,imported_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, source.SourceID, id, u.SourceLegacyUserID, source.SourceTgID, source.PaymentID, hash[:], source.Subscription, source.Status, source.CreatedAt, source.UpdatedAt, now); err != nil {
				return result, failure(409, "IMPORT_SOURCE_CONFLICT")
			}
		}
	}
	if !dryRun {
		for _, item := range locatedUsers {
			if count := changed[item.ID]; count > 0 {
				reason := strconv.Itoa(count) + " legacy payment transactions imported"
				system := true
				if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: item.ID, CreatedAt: now, Action: "legacy_payment_history_imported", SystemActor: &system, Reason: &reason}) != nil {
					return result, unavailable()
				}
			}
		}
	}
	return result, nil
}

func validateLegacyPayments(p LegacyPaymentPackage) error {
	if p.Version != 1 || p.Users == nil || p.Transactions == nil {
		return failure(400, "IMPORT_INVALID_PACKAGE")
	}
	legacyIDs, tgIDs, sourceIDs := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	paymentIDs := map[string]bool{}
	for _, u := range p.Users {
		if u.SourceLegacyUserID <= 0 || u.SourceTgID <= 0 || legacyIDs[u.SourceLegacyUserID] || tgIDs[u.SourceTgID] {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		legacyIDs[u.SourceLegacyUserID] = true
		tgIDs[u.SourceTgID] = true
	}
	validTime := func(t time.Time) bool {
		return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.Nanosecond()%1000 == 0
	}
	validRaw := func(text string) bool {
		return len(text) <= 16384 && utf8.ValidString(text) && !strings.ContainsRune(text, '\x00')
	}
	for _, r := range p.Transactions {
		if r.SourceID <= 0 || !tgIDs[r.SourceTgID] || sourceIDs[r.SourceID] || paymentIDs[r.PaymentID] || !validRaw(r.PaymentID) || !validRaw(r.Subscription) || !validTime(r.CreatedAt) || !validTime(r.UpdatedAt) || (r.Status != "pending" && r.Status != "completed" && r.Status != "canceled" && r.Status != "refunded") {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		sourceIDs[r.SourceID] = true
		paymentIDs[r.PaymentID] = true
	}
	return nil
}
