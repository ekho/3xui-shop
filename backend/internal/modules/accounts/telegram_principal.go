package accounts

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Only this owner can construct the source proof. It is a snapshot, not a grant.
type telegramActorKey struct{}
type telegramActorProof struct {
	id                  uuid.UUID
	telegramID, version int64
}

func TelegramActor(ctx context.Context) (Snapshot, bool) {
	p, ok := ctx.Value(telegramActorKey{}).(telegramActorProof)
	if !ok {
		return Snapshot{}, false
	}
	// Return a copy: callers cannot modify the proof through a pointer.
	tg := p.telegramID
	return Snapshot{ID: p.id, TelegramID: &tg, CredentialVersion: p.version}, true
}

func telegramPrincipal(ctx context.Context, a Snapshot) error {
	p, ok := ctx.Value(telegramActorKey{}).(telegramActorProof)
	if !ok {
		return nil
	}
	if a.Restricted {
		return failure(403, "ACCOUNT_RESTRICTED")
	}
	if a.ID != p.id || a.TelegramID == nil || *a.TelegramID != p.telegramID || a.CredentialVersion != p.version || a.TelegramLoginDisabled || !SourceEligible(a) || a.TermsVersion == nil || a.PrivacyVersion == nil {
		return failure(403, "INVALID_CREDENTIALS")
	}
	return nil
}

func lockTelegramPrincipal(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	p, ok := ctx.Value(telegramActorKey{}).(telegramActorProof)
	if !ok {
		return nil
	}
	if p.id != actor {
		return failure(403, "INVALID_CREDENTIALS")
	}
	return lockTelegramIdentity(ctx, tx, p.telegramID)
}

func (s *Service) ResolveTelegramContext(ctx context.Context, tg int64) (context.Context, *Snapshot, error) {
	if tg <= 0 || tg > 1<<52-1 {
		return ctx, nil, failure(400, "INVALID_INPUT")
	}
	a, err := s.LookupTelegram(ctx, tg)
	if errors.Is(err, ErrNotFound) {
		tx, e := s.pool.Begin(ctx)
		if e != nil {
			return ctx, nil, unavailable()
		}
		defer tx.Rollback(ctx)
		if e = s.CheckTelegramAvailable(ctx, tx, tg); e != nil {
			return ctx, nil, e
		}
		return ctx, nil, nil
	}
	if err != nil {
		return ctx, nil, err
	}
	ctx = context.WithValue(ctx, telegramActorKey{}, telegramActorProof{a.ID, tg, a.CredentialVersion})
	if err = telegramPrincipal(ctx, a); err != nil {
		return ctx, nil, err
	}
	return ctx, &a, nil
}

// Guest moderation locks both identities before any account or role row. It
// intentionally permits clearing a retained guest ban after registration.
func (s *Service) LockSupportGuestOperatorTx(ctx context.Context, tx pgx.Tx, actor uuid.UUID, guestTG int64) error {
	p, ok := ctx.Value(telegramActorKey{}).(telegramActorProof)
	if !ok || p.id != actor || guestTG <= 0 || guestTG > 1<<52-1 {
		return failure(403, "INVALID_CREDENTIALS")
	}
	first, second := p.telegramID, guestTG
	if first > second {
		first, second = second, first
	}
	if err := lockTelegramIdentity(ctx, tx, first); err != nil {
		return err
	}
	if second != first {
		if err := lockTelegramIdentity(ctx, tx, second); err != nil {
			return err
		}
	}
	_, err := s.LockOperatorPair(ctx, tx, actor, actor)
	return err
}
