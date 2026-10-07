package accounts

import (
	"context"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type mailPayload = notifications.MailPayload

func (s *Service) enqueueMail(ctx context.Context, tx pgx.Tx, email string, challengeID *uuid.UUID, payload mailPayload) error {
	return s.enqueueMailDelivery(ctx, tx, email, challengeID, nil, "registration", payload)
}
func (s *Service) enqueueCredentialMail(ctx context.Context, tx pgx.Tx, email string, proofID uuid.UUID, payload mailPayload) error {
	return s.enqueueMailDelivery(ctx, tx, email, nil, &proofID, "credential", payload)
}
func (s *Service) enqueueSecurityNotice(ctx context.Context, tx pgx.Tx, email string, payload mailPayload) error {
	return s.enqueueMailDelivery(ctx, tx, email, nil, nil, "security_notice", payload)
}
func (s *Service) enqueueMailDelivery(ctx context.Context, tx pgx.Tx, email string, registrationID, credentialID *uuid.UUID, kind string, payload mailPayload) error {
	if s.mail.EnqueueMailTx(ctx, tx, email, registrationID, credentialID, kind, payload, s.now()) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) clearRevokedCredentialMail(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) error {
	ids, err := store.New(tx).RevokedCredentialMailProofs(ctx, &accountID)
	if err != nil {
		return unavailable()
	}
	if s.mail.ClearCredentialMailTx(ctx, tx, ids) != nil {
		return unavailable()
	}
	return nil
}

// Work uses this same dedicated connection for its short transactions, including MaxConns1.
func (s *Service) WithMailGuard(ctx context.Context, email string, work func(*pgxpool.Conn) error) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return unavailable()
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, "SELECT pg_advisory_unlock_all()"); err != nil {
			conn.Conn().Close(cleanup)
		}
		conn.Release()
	}()
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1::text,0))`, email); err != nil {
		return unavailable()
	}
	return work(conn)
}

// The email guard linearizes proof changes; do not lock the account row here.
func (s *Service) MailProofValidTx(ctx context.Context, tx pgx.Tx, registrationID, credentialID *uuid.UUID) (bool, error) {
	q := store.New(tx)
	if credentialID != nil {
		proof, err := q.LockCredentialProof(ctx, *credentialID)
		if err != nil {
			return false, unavailable()
		}
		if proof.AccountID == nil || proof.Revoked || proof.UsedAt.Valid || proof.ConfirmedAt.Valid || !s.now().Before(proof.TokenExpiresAt.Time) {
			return false, nil
		}
		account, err := q.AccountByID(ctx, *proof.AccountID)
		if err != nil {
			return false, unavailable()
		}
		if proof.CredentialVersion != account.CredentialVersion || proof.OriginalEmail != account.EmailKey.String {
			return false, nil
		}
		if proof.Purpose == "initial_email" {
			if account.Kind != "telegram" || account.Restricted || account.TelegramLoginDisabled || !s.now().Before(proof.CodeExpiresAt.Time) {
				return false, nil
			}
			return identityEmailAvailable(ctx, q, proof.TargetEmail)
		}
		if proof.Purpose == "identity_recovery" {
			if account.Kind != "telegram" || account.EmailKey.Valid || !account.TelegramID.Valid || !account.TelegramLoginDisabled || proof.RequestedBy == nil {
				return false, nil
			}
			allowed, err := recoveryActorAllowed(ctx, q, *proof.RequestedBy)
			if err != nil || !allowed {
				return false, err
			}
			protected, err := q.OperatorRoleExists(ctx, account.ID)
			if err != nil {
				return false, unavailable()
			}
			if protected {
				return false, nil
			}
			return identityEmailAvailable(ctx, q, proof.TargetEmail)
		}
	}
	if registrationID != nil {
		challenge, err := q.ChallengeByID(ctx, *registrationID)
		if err != nil {
			return false, unavailable()
		}
		if challenge.Revoked || !s.now().Before(challenge.TokenExpiresAt.Time) {
			return false, nil
		}
	}
	return true, nil
}
