package accounts

import (
	"context"
	"errors"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) InfrastructureAlertRecipientsTx(ctx context.Context, tx pgx.Tx) ([]notifications.ReminderRecipient, error) {
	rows, err := tx.Query(ctx, `SELECT a.id FROM accounts a JOIN infrastructure_operators i ON i.account_id=a.id JOIN operator_accounts o ON o.account_id=a.id
 WHERE a.kind='web' AND a.verified_at IS NOT NULL AND a.password_hash IS NOT NULL AND a.email_key IS NOT NULL AND NOT a.restricted ORDER BY a.id`)
	if err != nil {
		return nil, unavailable()
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, unavailable()
	}
	out := []notifications.ReminderRecipient{}
	for _, id := range ids {
		r, err := s.ReminderRecipientTx(ctx, tx, id, false)
		if err != nil {
			return nil, err
		}
		if r.Eligible && r.TelegramID > 0 && r.TelegramID <= 1<<52-1 {
			out = append(out, r)
		}
	}
	return out, nil
}

// The binding guard holds the account while the narrower grant is checked
// and the bounded send completes; revocation cannot race the send.
func (s *Service) WithInfrastructureDelivery(ctx context.Context, id uuid.UUID, tg, version int64, work func(pgx.Tx) error) (bool, error) {
	denied := false
	valid, err := s.WithTelegramDelivery(ctx, id, tg, version, func(tx pgx.Tx) error {
		if err := s.RequireInfrastructureTx(ctx, tx, id); err != nil {
			var auth *Error
			denied = errors.As(err, &auth) && (auth.Status == 401 || auth.Status == 403)
			return err
		}
		return work(tx)
	})
	if denied {
		return false, nil
	}
	return valid, err
}

// RequireInfrastructureTx checks current account, source proof, operator role,
// and the narrower explicit grant while holding the account row until commit.
func (s *Service) RequireInfrastructureTx(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	if actor == uuid.Nil {
		return failure(401, "INVALID_CREDENTIALS")
	}
	if source, ok := TelegramActor(ctx); ok && source.ID != actor {
		return failure(403, "INVALID_CREDENTIALS")
	}
	a, err := s.Lock(ctx, tx, actor)
	if errors.Is(err, ErrNotFound) {
		return failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return err
	}
	if a.Restricted {
		return failure(403, "ACCOUNT_RESTRICTED")
	}
	if a.Kind != "web" || a.VerifiedAt == nil || a.EmailKey == nil || !a.PasswordSet {
		return failure(403, "INVALID_CREDENTIALS")
	}
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM infrastructure_operators i JOIN operator_accounts o ON o.account_id=i.account_id WHERE i.account_id=$1)`, actor).Scan(&allowed); err != nil {
		return unavailable()
	}
	if !allowed {
		return failure(403, "INVALID_CREDENTIALS")
	}
	return nil
}

func (s *Service) RequireInfrastructure(ctx context.Context, actor uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	if err = s.RequireInfrastructureTx(ctx, tx, actor); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) ChangeInfrastructureRole(ctx context.Context, target uuid.UUID, grant bool) error {
	if target == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.Lock(ctx, tx, target)
	if errors.Is(err, ErrNotFound) {
		return failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return err
	}
	var changed int64
	if grant {
		if a.Kind != "web" || a.Restricted || a.VerifiedAt == nil || a.EmailKey == nil || !a.PasswordSet {
			return failure(403, "INVALID_CREDENTIALS")
		}
		err = tx.QueryRow(ctx, `SELECT account_id FROM operator_accounts WHERE account_id=$1 FOR SHARE`, target).Scan(new(uuid.UUID))
		if errors.Is(err, pgx.ErrNoRows) {
			return failure(403, "INVALID_CREDENTIALS")
		}
		if err != nil {
			return unavailable()
		}
		tag, e := tx.Exec(ctx, `INSERT INTO infrastructure_operators(account_id,granted_at) VALUES($1,$2) ON CONFLICT DO NOTHING`, target, s.now())
		err = e
		changed = tag.RowsAffected()
	} else {
		tag, e := tx.Exec(ctx, `DELETE FROM infrastructure_operators WHERE account_id=$1`, target)
		err = e
		changed = tag.RowsAffected()
	}
	if err != nil {
		return unavailable()
	}
	if changed > 0 {
		action := "infrastructure_revoked"
		if grant {
			action = "infrastructure_granted"
		}
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: action, AccountID: target}); err != nil {
			return unavailable()
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}
