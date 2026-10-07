package accounts

import (
	"context"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"github.com/google/uuid"
)

func (s *Service) RequestInitialEmail(ctx context.Context, rawMini string, in InitialEmailInput, ip string) (RegistrationAccepted, error) {
	out := RegistrationAccepted{ChallengeId: uuid.New(), ResendAfter: 60}
	auth, err := s.AuthenticateTelegram(ctx, rawMini, false)
	if err != nil {
		return out, err
	}
	if auth.Account.Kind != "telegram" {
		return out, failure(409, "INDEPENDENT_LOGIN_EXISTS")
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return out, err
	}
	if err = s.limitCredentialIP(ctx, ip); err != nil {
		return out, err
	}
	if err = s.limitMail(ctx, email); err != nil {
		return out, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.lockIdentityMini(ctx, tx, rawMini, auth.Account)
	if err != nil {
		return out, err
	}
	if a.Kind != "telegram" {
		return out, failure(409, "INDEPENDENT_LOGIN_EXISTS")
	}
	emails, err := s.credentialEmails(ctx, tx, a)
	if err != nil {
		return out, err
	}
	emails = append(emails, email)
	if err = s.lockCredentialEmails(ctx, tx, emails); err != nil {
		return out, err
	}
	q := store.New(tx)
	if q.RevokeIdentityPurpose(ctx, store.RevokeIdentityPurposeParams{AccountID: &a.ID, Purpose: "initial_email"}) != nil || s.clearRevokedCredentialMail(ctx, tx, a.ID) != nil {
		return out, unavailable()
	}
	if err = s.addCredentialProof(ctx, tx, out.ChallengeId, "initial_email", &a.ID, nil, "", email, a.CredentialVersion, a.Locale, s.now()); err != nil {
		return out, err
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) CompleteInitialEmail(ctx context.Context, rawMini string, in InitialEmailCompleteInput, ip string) (VerifyResult, error) {
	empty := VerifyResult{}
	auth, err := s.AuthenticateTelegram(ctx, rawMini, false)
	if err != nil {
		return empty, err
	}
	if auth.Account.Kind != "telegram" {
		return empty, failure(409, "INDEPENDENT_LOGIN_EXISTS")
	}
	if err = s.identityConsent(in.AcceptedTermsVersion, in.AcceptedPrivacyVersion); err != nil {
		return empty, err
	}
	if err = s.limitCredentialIP(ctx, ip); err != nil {
		return empty, err
	}
	proof, _, err := s.lookupCredential(ctx, nil, &in.ChallengeId, &in.Code)
	if err != nil {
		return empty, err
	}
	if proof.Purpose != "initial_email" || proof.AccountID == nil || *proof.AccountID != auth.Account.ID {
		return empty, failure(400, "INVALID_VERIFICATION")
	}
	if err = validatePassword(in.NewPassword); err != nil {
		return empty, err
	}
	hash, err := s.hashPassword(ctx, in.NewPassword)
	if err != nil {
		return empty, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.lockIdentityMini(ctx, tx, rawMini, auth.Account)
	if err != nil {
		return empty, err
	}
	if a.Kind != "telegram" {
		return empty, failure(409, "INDEPENDENT_LOGIN_EXISTS")
	}
	emails, err := s.credentialEmails(ctx, tx, a)
	if err != nil {
		return empty, err
	}
	if err = s.lockCredentialEmails(ctx, tx, append(emails, proof.TargetEmail)); err != nil {
		return empty, err
	}
	q := store.New(tx)
	proof, err = q.LockCredentialProof(ctx, proof.ID)
	if err != nil {
		return empty, unavailable()
	}
	if err = s.checkCredentialProof(ctx, tx, proof, a, false, &in.Code); err != nil {
		return empty, err
	}
	available, err := identityEmailAvailable(ctx, q, proof.TargetEmail)
	if err != nil {
		return empty, err
	}
	if !available {
		return empty, failure(400, "INVALID_VERIFICATION")
	}
	if q.GrantIndependentCredentials(ctx, store.GrantIndependentCredentialsParams{ID: a.ID, EmailKey: proof.TargetEmail, PasswordHash: hash, VerifiedAt: stamp(s.now()), TermsVersion: in.AcceptedTermsVersion, PrivacyVersion: in.AcceptedPrivacyVersion}) != nil || q.ConsumeCredentialProof(ctx, store.ConsumeCredentialProofParams{ID: proof.ID, UsedAt: stamp(s.now())}) != nil || q.DeleteAccountSessions(ctx, a.ID) != nil {
		return empty, unavailable()
	}
	if err = s.revokeCredentialProofs(ctx, tx, a.ID); err != nil {
		return empty, err
	}
	if err = s.credentialAudit(ctx, tx, a.ID, "initial_email_confirmed"); err != nil {
		return empty, err
	}
	if tx.Commit(ctx) != nil {
		return empty, unavailable()
	}
	return VerifyResult{Verified: true}, nil
}
