package bonuses

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/bonuses/internal/store"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func activationError(err error) error {
	var access *subscriptions.Error
	if errors.As(err, &access) {
		return failure(access.Status, access.Code)
	}
	return err
}

func (s *Service) lockActivationAccount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (accounts.Snapshot, error) {
	a, err := s.authority.Lock(ctx, tx, id)
	if err != nil {
		return a, err
	}
	if a.Restricted {
		return a, failure(403, "ACCOUNT_RESTRICTED")
	}
	if !accounts.SourceEligible(a) || a.TermsVersion == nil || a.PrivacyVersion == nil || a.Kind == "telegram" && a.TelegramLoginDisabled {
		return a, failure(403, "INVALID_CREDENTIALS")
	}
	if a.VpnBanned || a.AccessProfile != nil && *a.AccessProfile == "unlimited" {
		return a, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	return a, nil
}

func (s *Service) activationReplay(ctx context.Context, tx pgx.Tx, account, key uuid.UUID, hash [32]byte) (PromocodeActivation, bool, error) {
	var prior, result []byte
	err := tx.QueryRow(ctx, "SELECT body_hash,result FROM idempotency_records WHERE principal=$1 AND operation='activatePromocode' AND key=$2", "account:"+account.String(), key).Scan(&prior, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return PromocodeActivation{}, false, nil
	}
	if err != nil {
		return PromocodeActivation{}, false, unavailable()
	}
	if !bytes.Equal(prior, hash[:]) {
		return PromocodeActivation{}, true, failure(409, "IDEMPOTENCY_CONFLICT")
	}
	var saved PromocodeActivation
	if json.Unmarshal(result, &saved) != nil {
		return saved, true, unavailable()
	}
	return saved, true, nil
}

func (s *Service) ActivatePromocode(ctx context.Context, account, key uuid.UUID, in ActivatePromocodeInput) (PromocodeActivation, error) {
	var out PromocodeActivation
	code := strings.TrimSpace(in.Code)
	if account == uuid.Nil || key == uuid.Nil || !utf8.ValidString(code) || utf8.RuneCountInString(code) < 1 || utf8.RuneCountInString(code) > 32 || strings.IndexFunc(code, unicode.IsControl) >= 0 {
		return out, failure(400, "INVALID_INPUT")
	}
	if s.subscriptions == nil {
		return out, unavailable()
	}
	hash := sha256.Sum256([]byte(code))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.lockActivationAccount(ctx, tx, account); err != nil {
		return out, err
	}
	if saved, found, err := s.activationReplay(ctx, tx, account, key, hash); found || err != nil {
		return saved, err
	}
	row, err := store.New(tx).LockPromocodeByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "PROMOCODE_INVALID")
	}
	if err != nil {
		return out, unavailable()
	}
	if err = availableActivation(row); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	reason := "promocode_id=" + row.ID.String()
	op, err := s.subscriptions.GrantBonusDays(ctx, account, key, int(row.DurationDays), reason, func(ctx context.Context, tx pgx.Tx, op subscriptions.AccessOperation) error {
		// Subscriptions already holds the account lock; the code always comes second.
		a, err := s.lockActivationAccount(ctx, tx, account)
		if err != nil {
			return err
		}
		q := store.New(tx)
		current, err := q.LockPromocode(ctx, row.ID)
		if err != nil {
			return unavailable()
		}
		if err = availableActivation(current); err != nil {
			return err
		}
		if current.Revision != row.Revision || current.DurationDays != row.DurationDays {
			return failure(409, "PROMOCODE_REVISION_CONFLICT")
		}
		before, _ := json.Marshal(promocodeMetadata(publicPromocode(current)))
		now := s.now().UTC().Truncate(time.Microsecond)
		telegramID := pgtype.Int8{}
		if a.TelegramID != nil {
			telegramID = pgtype.Int8{Int64: *a.TelegramID, Valid: true}
		}
		current, err = q.ActivatePromocode(ctx, store.ActivatePromocodeParams{ID: row.ID, ActivatedAccountID: &account, ActivatedByTgID: telegramID, ActivatedAt: pgtype.Timestamptz{Time: now, Valid: true}})
		if err != nil {
			return unavailable()
		}
		after, _ := json.Marshal(struct {
			PromocodeMetadata
			AccessOperationID uuid.UUID `json:"access_operation_id"`
		}{promocodeMetadata(publicPromocode(current)), op.OperationId})
		event := uuid.New()
		if err = q.AddPromocodeEvent(ctx, store.AddPromocodeEventParams{ID: event, PromocodeID: row.ID, ActorAccountID: &account, Action: "activate", CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, Reason: pgtype.Text{String: reason, Valid: true}, BeforeSnapshot: before, AfterSnapshot: after}); err != nil {
			return unavailable()
		}
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: event, AccountID: account, CreatedAt: now, Action: "promocode.activate", Reason: &reason, AccessOperationID: &op.OperationId}); err != nil {
			return unavailable()
		}
		out = PromocodeActivation{PromocodeID: row.ID, DurationDays: int(row.DurationDays), OperationID: op.OperationId, Status: string(op.Status)}
		result, _ := json.Marshal(out)
		if _, err = tx.Exec(ctx, "INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,'activatePromocode',$2,$3,$4,$5)", "account:"+account.String(), key, hash[:], result, now); err != nil {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return PromocodeActivation{}, activationError(err)
	}
	return PromocodeActivation{PromocodeID: row.ID, DurationDays: int(row.DurationDays), OperationID: op.OperationId, Status: string(op.Status)}, nil
}

func availableActivation(row store.Promocode) error {
	if row.DeletedAt.Valid {
		return failure(404, "PROMOCODE_INVALID")
	}
	if row.IsActivated {
		return failure(409, "PROMOCODE_USED")
	}
	if row.DurationDays < 1 || row.DurationDays > 365 {
		return failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	return nil
}

func (s *Service) GetPromocodeActivation(ctx context.Context, account, id uuid.UUID) (PromocodeActivation, error) {
	if account == uuid.Nil || id == uuid.Nil {
		return PromocodeActivation{}, failure(400, "INVALID_INPUT")
	}
	if s.subscriptions == nil {
		return PromocodeActivation{}, unavailable()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PromocodeActivation{}, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.lockActivationAccount(ctx, tx, account); err != nil {
		return PromocodeActivation{}, err
	}
	row, err := store.New(tx).ReadPromocodeActivation(ctx, store.ReadPromocodeActivationParams{ActorAccountID: &account, OperationID: id.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return PromocodeActivation{}, failure(404, "PROMOCODE_ACTIVATION_NOT_FOUND")
	}
	if err != nil {
		return PromocodeActivation{}, unavailable()
	}
	var meta PromocodeMetadata
	if json.Unmarshal(row.AfterSnapshot, &meta) != nil {
		return PromocodeActivation{}, unavailable()
	}
	if tx.Commit(ctx) != nil {
		return PromocodeActivation{}, unavailable()
	}
	op, err := s.subscriptions.GetBonusOperation(ctx, account, id)
	if err != nil {
		return PromocodeActivation{}, activationError(err)
	}
	return PromocodeActivation{PromocodeID: row.PromocodeID, DurationDays: meta.DurationDays, OperationID: op.OperationId, Status: string(op.Status)}, nil
}
