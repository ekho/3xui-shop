package s01

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"time"
)

func (s *Service) revokeEmailChange(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	q := store.New(tx)
	if q.RevokeEmailChangeProofs(ctx, &id) != nil || q.ClearRevokedCredentialMail(ctx, &id) != nil {
		return unavailable()
	}
	return nil
}
func validEmailPair(proofs []store.CredentialChallenge, account store.Account, now time.Time) bool {
	if len(proofs) != 2 || proofs[0].Purpose != "email_change_new" || proofs[1].Purpose != "email_change_old" {
		return false
	}
	first := proofs[0]
	if first.ChangeID == nil || first.TargetEmail == first.OriginalEmail {
		return false
	}
	for _, p := range proofs {
		if p.AccountID == nil || *p.AccountID != account.ID || p.ChangeID == nil || *p.ChangeID != *first.ChangeID || p.OriginalEmail != account.EmailKey || p.TargetEmail != first.TargetEmail || p.CredentialVersion != account.CredentialVersion || p.Revoked || p.UsedAt.Valid || !now.Before(p.TokenExpiresAt.Time) || !p.CreatedAt.Time.Equal(first.CreatedAt.Time) || !p.TokenExpiresAt.Time.Equal(first.TokenExpiresAt.Time) || !p.CodeExpiresAt.Time.Equal(first.CodeExpiresAt.Time) {
			return false
		}
	}
	return true
}
func (s *Service) pendingEmailChange(ctx context.Context, q *store.Queries, account store.Account) (*wire.PendingEmailChange, error) {
	proofs, err := q.ActiveEmailChange(ctx, store.ActiveEmailChangeParams{AccountID: &account.ID, Now: stamp(s.now())})
	if err != nil {
		return nil, unavailable()
	}
	if !validEmailPair(proofs, account, s.now()) {
		return nil, nil
	}
	return &wire.PendingEmailChange{NewEmail: openapi_types.Email(proofs[0].TargetEmail), ExpiresAt: proofs[0].TokenExpiresAt.Time, CurrentEmailConfirmed: proofs[1].ConfirmedAt.Valid, NewEmailConfirmed: proofs[0].ConfirmedAt.Valid}, nil
}
func (s *Service) RequestEmailChange(ctx context.Context, raw string, in wire.EmailChangeInput, ip string) (wire.EmailChangeAccepted, error) {
	account, _, err := s.authenticateCurrentPassword(ctx, raw, in.CurrentPassword, ip)
	if err != nil {
		return wire.EmailChangeAccepted{}, err
	}
	target, err := normalizeEmail(string(in.NewEmail))
	if err != nil {
		return wire.EmailChangeAccepted{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wire.EmailChangeAccepted{}, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	account, _, err = s.revalidateCredentialSession(ctx, q, raw, account)
	if err != nil {
		return wire.EmailChangeAccepted{}, err
	}
	emails, err := s.credentialEmails(ctx, tx, account)
	if err != nil {
		return wire.EmailChangeAccepted{}, err
	}
	if err = s.lockCredentialEmails(ctx, tx, append(emails, target)); err != nil {
		return wire.EmailChangeAccepted{}, err
	}
	_, err = q.AccountByEmail(ctx, target)
	if err == nil {
		return wire.EmailChangeAccepted{}, failure(409, "EMAIL_CHANGE_UNAVAILABLE")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return wire.EmailChangeAccepted{}, unavailable()
	}
	if err = s.limitMails(ctx, []string{account.EmailKey, target}); err != nil {
		return wire.EmailChangeAccepted{}, err
	}
	if err = s.revokeEmailChange(ctx, tx, account.ID); err != nil {
		return wire.EmailChangeAccepted{}, err
	}
	now := s.now()
	change := uuid.New()
	out := wire.EmailChangeAccepted{ChangeId: change, ExpiresAt: now.Add(30 * time.Minute), ResendAfter: 60}
	for _, purpose := range []string{"email_change_old", "email_change_new"} {
		if err = s.addCredentialProof(ctx, tx, uuid.New(), purpose, &account.ID, &change, account.EmailKey, target, account.CredentialVersion, account.Locale, now); err != nil {
			return wire.EmailChangeAccepted{}, err
		}
	}
	if tx.Commit(ctx) != nil {
		return wire.EmailChangeAccepted{}, unavailable()
	}
	return out, nil
}
func (s *Service) ConfirmEmailChange(ctx context.Context, in wire.EmailChangeConfirmInput, ip string) (wire.EmailChangeResult, error) {
	out := wire.EmailChangeResult{}
	if err := s.limitCredentialIP(ctx, ip); err != nil {
		return out, err
	}
	proof, byToken, err := s.lookupCredential(ctx, in.Token, in.ChallengeId, in.Code)
	if err != nil {
		return out, err
	}
	if proof.AccountID == nil || proof.ChangeID == nil || (proof.Purpose != "email_change_old" && proof.Purpose != "email_change_new") {
		return out, failure(400, "INVALID_VERIFICATION")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	account, err := q.LockAccount(ctx, *proof.AccountID)
	if err != nil {
		return out, unavailable()
	}
	if account.Restricted {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	emails, err := s.credentialEmails(ctx, tx, account)
	if err != nil {
		return out, err
	}
	if err = s.lockCredentialEmails(ctx, tx, append(emails, proof.OriginalEmail, proof.TargetEmail)); err != nil {
		return out, err
	}
	pair, err := q.LockEmailChangePair(ctx, proof.ChangeID)
	if err != nil {
		return out, unavailable()
	}
	if !validEmailPair(pair, account, s.now()) {
		return out, failure(400, "INVALID_VERIFICATION")
	}
	selected := -1
	for i, p := range pair {
		if p.ID == proof.ID {
			selected = i
		}
	}
	if selected < 0 || pair[selected].ConfirmedAt.Valid {
		return out, failure(400, "INVALID_VERIFICATION")
	}
	proof = pair[selected]
	if err = s.checkCredentialProof(ctx, tx, proof, account, byToken, in.Code); err != nil {
		return out, err
	}
	now := s.now()
	if q.ConfirmCredentialProof(ctx, store.ConfirmCredentialProofParams{ID: proof.ID, ConfirmedAt: stamp(now)}) != nil || q.ClearCredentialMail(ctx, &proof.ID) != nil {
		return out, unavailable()
	}
	if !pair[1-selected].ConfirmedAt.Valid {
		if tx.Commit(ctx) != nil {
			return out, unavailable()
		}
		return out, nil
	}
	conflict := func() (wire.EmailChangeResult, error) {
		if s.revokeEmailChange(ctx, tx, account.ID) != nil || tx.Commit(ctx) != nil {
			return out, unavailable()
		}
		return out, failure(409, "EMAIL_CHANGE_UNAVAILABLE")
	}
	if _, err = q.AccountByEmail(ctx, proof.TargetEmail); err == nil {
		return conflict()
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	if _, err = tx.Exec(ctx, "SAVEPOINT apply_email_change"); err != nil {
		return out, unavailable()
	}
	err = q.SetAccountEmail(ctx, store.SetAccountEmailParams{ID: account.ID, EmailKey: proof.TargetEmail, VerifiedAt: stamp(now)})
	if err != nil {
		var constraint *pgconn.PgError
		if errors.As(err, &constraint) && constraint.Code == "23505" {
			if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT apply_email_change"); err != nil {
				return out, unavailable()
			}
			return conflict()
		}
		return out, unavailable()
	}
	for _, p := range pair {
		if q.ConsumeCredentialProof(ctx, store.ConsumeCredentialProofParams{ID: p.ID, UsedAt: stamp(now)}) != nil {
			return out, unavailable()
		}
	}
	if q.DeleteAccountSessions(ctx, account.ID) != nil || s.revokeCredentialProofs(ctx, tx, account.ID) != nil || s.credentialAudit(ctx, tx, account.ID, "email_change") != nil {
		return out, unavailable()
	}
	for _, email := range []string{account.EmailKey, proof.TargetEmail} {
		if err = s.enqueueSecurityNotice(ctx, tx, email, mailPayload{Type: "security_notice", Locale: account.Locale}); err != nil {
			return out, err
		}
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	out.Completed = true
	return out, nil
}
func (s *Service) CancelEmailChange(ctx context.Context, raw string) error {
	q := store.New(s.pool)
	session, err := s.sessionByRaw(ctx, q, raw)
	if err != nil {
		return err
	}
	snapshot, err := q.AccountByID(ctx, session.AccountID)
	if err != nil {
		return unavailable()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q = store.New(tx)
	account, _, err := s.revalidateCredentialSession(ctx, q, raw, snapshot)
	if err != nil {
		return err
	}
	emails, err := s.credentialEmails(ctx, tx, account)
	if err != nil {
		return err
	}
	if err = s.lockCredentialEmails(ctx, tx, emails); err != nil {
		return err
	}
	pending, err := s.pendingEmailChange(ctx, q, account)
	if err != nil {
		return err
	}
	if pending == nil {
		return nil
	}
	if s.revokeEmailChange(ctx, tx, account.ID) != nil || s.credentialAudit(ctx, tx, account.ID, "email_change_cancel") != nil || tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
