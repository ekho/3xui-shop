package accounts

import (
	"context"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

func (s *Service) RequestOperatorRecovery(ctx context.Context, raw string, actor, target uuid.UUID, keyText string, in OperatorRecoveryInput, ip string) (IdentityRecoveryAccepted, error) {
	var out IdentityRecoveryAccepted
	key, err := uuid.Parse(keyText)
	reason := strings.TrimSpace(in.Reason)
	if err != nil || key == uuid.Nil || actor == uuid.Nil || target == uuid.Nil || !in.Confirmed || !validText(reason, 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	if actor == target {
		return out, failure(403, "OPERATOR_ACCOUNT_PROTECTED")
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return out, err
	}
	operator, _, err := s.authenticateCurrentPassword(ctx, raw, in.CurrentPassword, ip)
	if err != nil {
		return out, err
	}
	if operator.ID != actor {
		return out, failure(401, "INVALID_CREDENTIALS")
	}
	if err = s.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	if err = s.limitCredentialIP(ctx, ip); err != nil {
		return out, err
	}
	known, err := store.New(s.pool).AccountByID(ctx, target)
	if err != nil {
		return out, failure(404, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if known.TelegramID.Valid {
		if err = lockTelegramIdentity(ctx, tx, known.TelegramID.Int64); err != nil {
			return out, err
		}
	}
	a, err := s.lockOperatorPair(ctx, tx, actor, target)
	if err != nil {
		return out, err
	}
	q := store.New(tx)
	if _, _, err = s.revalidateCredentialSession(ctx, q, raw, operator); err != nil {
		return out, err
	}
	principal := "operator-account:" + actor.String()
	const operation = "requestOperatorRecovery"
	fingerprint := bodyHash(struct {
		Target        uuid.UUID
		Email, Reason string
		Confirmed     bool
	}{target, email, reason, in.Confirmed})
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: operation, Key: key}) != nil {
		return out, unavailable()
	}
	if prior, found, replayErr := replay[IdentityRecoveryAccepted](ctx, q, principal, operation, key, fingerprint); found || replayErr != nil {
		return prior, replayErr
	}
	protected, err := q.OperatorRoleExists(ctx, target)
	if err != nil {
		return out, unavailable()
	}
	if protected {
		return out, failure(403, "OPERATOR_ACCOUNT_PROTECTED")
	}
	if a.Kind != "telegram" || a.EmailKey.Valid || a.VerifiedAt.Valid || a.PasswordHash.Valid || !a.TelegramID.Valid || a.TelegramID != known.TelegramID {
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
	free, err := identityEmailAvailable(ctx, q, email)
	if err != nil {
		return out, err
	}
	if !free {
		return out, failure(409, "IDENTITY_CONFLICT")
	}
	if err = s.limitMail(ctx, email); err != nil {
		return out, err
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	proofID := uuid.New()
	if err = s.revokeCredentialProofs(ctx, tx, target); err != nil {
		return out, err
	}
	if q.QuarantineTelegramIdentity(ctx, target) != nil || q.DeleteAccountSessions(ctx, target) != nil {
		return out, unavailable()
	}
	if s.cfg.RequireStarsCancellation != nil {
		if err = s.cfg.RequireStarsCancellation(ctx, tx, target, "Telegram identity quarantined"); err != nil {
			return out, unavailable()
		}
	}
	if err = s.addCredentialProofBy(ctx, tx, proofID, "identity_recovery", &target, nil, "", email, a.CredentialVersion+1, a.Locale, now, &actor); err != nil {
		return out, err
	}
	if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: now, AccountID: target, Action: "identity_recovery_requested", OperatorAccountID: &actor, Reason: &reason}) != nil {
		return out, unavailable()
	}
	out = IdentityRecoveryAccepted{ChallengeId: proofID, ExpiresAt: now.Add(30 * time.Minute), ResendAfter: 60}
	if err = s.saveIdempotency(ctx, q, principal, operation, key, fingerprint, out); err != nil {
		return out, err
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) CompleteIdentityRecovery(ctx context.Context, in IdentityRecoveryCompleteInput, ip string) (VerifyResult, error) {
	var out VerifyResult
	if err := s.identityConsent(in.AcceptedTermsVersion, in.AcceptedPrivacyVersion); err != nil {
		return out, err
	}
	if err := s.limitCredentialIP(ctx, ip); err != nil {
		return out, err
	}
	proof, byToken, err := s.lookupCredential(ctx, in.Token, in.ChallengeId, in.Code)
	if err != nil {
		return out, err
	}
	if proof.Purpose != "identity_recovery" || proof.AccountID == nil || proof.RequestedBy == nil {
		return out, failure(400, "INVALID_VERIFICATION")
	}
	if err = validatePassword(in.NewPassword); err != nil {
		return out, err
	}
	hash, err := s.hashPassword(ctx, in.NewPassword)
	if err != nil {
		return out, err
	}
	q := store.New(s.pool)
	known, err := q.AccountByID(ctx, *proof.AccountID)
	if err != nil || !known.TelegramID.Valid {
		return out, failure(400, "INVALID_VERIFICATION")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = lockTelegramIdentity(ctx, tx, known.TelegramID.Int64); err != nil {
		return out, err
	}
	actor, target := *proof.RequestedBy, *proof.AccountID
	if actor == target {
		return out, failure(400, "INVALID_VERIFICATION")
	}
	a, err := s.lockOperatorPair(ctx, tx, actor, target)
	if err != nil {
		return out, err
	}
	q = store.New(tx)
	protected, err := q.OperatorRoleExists(ctx, target)
	if err != nil {
		return out, unavailable()
	}
	if protected || a.Kind != "telegram" || a.EmailKey.Valid || a.VerifiedAt.Valid || a.PasswordHash.Valid || !a.TelegramLoginDisabled || a.TelegramID != known.TelegramID {
		return out, failure(400, "INVALID_VERIFICATION")
	}
	emails, err := s.credentialEmails(ctx, tx, a)
	if err != nil {
		return out, err
	}
	emails = append(emails, proof.TargetEmail)
	if err = s.lockCredentialEmails(ctx, tx, emails); err != nil {
		return out, err
	}
	proof, err = q.LockCredentialProof(ctx, proof.ID)
	if err != nil {
		return out, unavailable()
	}
	if proof.RequestedBy == nil || *proof.RequestedBy != actor {
		return out, failure(400, "INVALID_VERIFICATION")
	}
	if err = s.checkCredentialProof(ctx, tx, proof, a, byToken, in.Code); err != nil {
		return out, err
	}
	free, err := identityEmailAvailable(ctx, q, proof.TargetEmail)
	if err != nil {
		return out, err
	}
	if !free {
		return out, failure(409, "IDENTITY_CONFLICT")
	}
	now := s.now()
	changed, err := q.RetireTelegramIdentity(ctx, store.RetireTelegramIdentityParams{TelegramID: a.TelegramID.Int64, AccountID: target, RetiredAt: stamp(now)})
	if err != nil || changed != 1 {
		return out, unavailable()
	}
	if q.GrantIndependentCredentials(ctx, store.GrantIndependentCredentialsParams{ID: target, EmailKey: proof.TargetEmail, PasswordHash: hash, VerifiedAt: stamp(now), TermsVersion: in.AcceptedTermsVersion, PrivacyVersion: in.AcceptedPrivacyVersion}) != nil || q.ClearTelegramIdentity(ctx, target) != nil || q.ConsumeCredentialProof(ctx, store.ConsumeCredentialProofParams{ID: proof.ID, UsedAt: stamp(now)}) != nil || q.DeleteAccountSessions(ctx, target) != nil {
		return out, unavailable()
	}
	if err = s.revokeCredentialProofs(ctx, tx, target); err != nil {
		return out, err
	}
	reason := "terms=" + in.AcceptedTermsVersion + " privacy=" + in.AcceptedPrivacyVersion
	if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: now, AccountID: target, Action: "identity_recovery_confirmed", OperatorAccountID: &actor, Reason: &reason}) != nil || tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	out.Verified = true
	return out, nil
}

// Called with the actor account locked; preserve the mail guard's email-before-proof order.
func (s *Service) revokeIssuedRecoveries(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	q := store.New(tx)
	proofs, err := q.IssuedRecoveryProofs(ctx, &actor)
	if err != nil {
		return unavailable()
	}
	emails := make([]string, 0, len(proofs))
	ids := make([]uuid.UUID, 0, len(proofs))
	for _, p := range proofs {
		emails = append(emails, p.TargetEmail)
		ids = append(ids, p.ID)
	}
	if err = s.lockCredentialEmails(ctx, tx, emails); err != nil {
		return err
	}
	if q.RevokeIssuedRecoveryProofs(ctx, &actor) != nil || s.mail.ClearCredentialMailTx(ctx, tx, ids) != nil {
		return unavailable()
	}
	return nil
}

func recoveryActorAllowed(ctx context.Context, q *store.Queries, actor uuid.UUID) (bool, error) {
	a, err := q.AccountByID(ctx, actor)
	if err != nil {
		return false, unavailable()
	}
	if a.Restricted || a.Kind != "web" || !a.VerifiedAt.Valid || !a.PasswordHash.Valid {
		return false, nil
	}
	allowed, err := q.OperatorExists(ctx, actor)
	if err != nil {
		return false, unavailable()
	}
	return allowed, nil
}
