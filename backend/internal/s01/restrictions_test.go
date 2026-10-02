package s01

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

// Removing the restriction write, audit, or idempotency replay breaks this test.
func TestRestrictionReplayAndProtectedOperator(t *testing.T) {
	s, customer, actor := operatorActors(t)
	ctx := context.Background()
	key := uuid.New()
	in := wire.OperatorRestrictionInput{Restricted: true, Reason: " reviewed "}
	first, err := s.SetOperatorRestriction(ctx, actor, customer, key, in)
	if err != nil || !first.Restricted || first.OperatorAccountId == nil || *first.OperatorAccountId != actor || first.ChangedAt == nil {
		t.Fatal("restrict", err)
	}
	replay, err := s.SetOperatorRestriction(ctx, actor, customer, key, in)
	if err != nil || !replay.Restricted || replay.OperatorAccountId == nil || *replay.OperatorAccountId != actor || replay.ChangedAt == nil || !replay.ChangedAt.Equal(*first.ChangedAt) {
		t.Fatal("replay", err)
	}
	in.Reason = "changed"
	if _, err := s.SetOperatorRestriction(ctx, actor, customer, key, in); status(err) != 409 {
		t.Fatal("changed body", err)
	}
	if _, err := s.SetOperatorRestriction(ctx, actor, actor, uuid.New(), in); status(err) != 403 || restrictionCode(err) != "OPERATOR_ACCOUNT_PROTECTED" {
		t.Fatal("protected operator", err)
	}
	var restricted bool
	var events int
	if err := s.pool.QueryRow(ctx, `SELECT restricted FROM accounts WHERE id=$1`, customer).Scan(&restricted); err != nil || !restricted {
		t.Fatal("state", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='account_restricted'`, customer).Scan(&events); err != nil || events != 1 {
		t.Fatal("audit once", err)
	}
}

func restrictionCode(err error) string {
	var domain *Error
	if errors.As(err, &domain) {
		return domain.Code
	}
	return ""
}

func TestRestrictionNoopDoesNotInventChange(t *testing.T) {
	s, customer, actor := operatorActors(t)
	s.now = func() time.Time { return time.Date(2026, 10, 2, 3, 4, 5, 123456789, time.UTC) }
	out, err := s.SetOperatorRestriction(context.Background(), actor, customer, uuid.New(), wire.OperatorRestrictionInput{Restricted: false, Reason: "no change"})
	if err != nil || out.Restricted || out.ChangedAt != nil || out.OperatorAccountId != nil {
		t.Fatal("invented transition", err)
	}
	changed, err := s.SetOperatorRestriction(context.Background(), actor, customer, uuid.New(), wire.OperatorRestrictionInput{Restricted: true, Reason: "reviewed"})
	if err != nil || changed.ChangedAt == nil {
		t.Fatal("transition", err)
	}
	same, err := s.SetOperatorRestriction(context.Background(), actor, customer, uuid.New(), wire.OperatorRestrictionInput{Restricted: true, Reason: "still reviewed"})
	if err != nil || same.ChangedAt == nil || !same.ChangedAt.Equal(*changed.ChangedAt) || same.OperatorAccountId == nil || *same.OperatorAccountId != actor {
		t.Fatal("no-op lost transition metadata", err)
	}
}

func TestRestrictionReplayAfterTargetGetsOperatorRole(t *testing.T) {
	s, target, actor := operatorActors(t)
	key := uuid.New()
	in := wire.OperatorRestrictionInput{Restricted: false, Reason: "no change"}
	first, err := s.SetOperatorRestriction(context.Background(), actor, target, key, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeOperatorRole(context.Background(), target, true); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.SetOperatorRestriction(context.Background(), actor, target, key, in)
	if err != nil || replayed.Restricted != first.Restricted || replayed.ChangedAt != nil {
		t.Fatal("same-key replay after role change", err)
	}
	if _, err := s.SetOperatorRestriction(context.Background(), actor, target, uuid.New(), in); restrictionCode(err) != "OPERATOR_ACCOUNT_PROTECTED" {
		t.Fatal("new-key protected target", err)
	}
}

// A failed audit insert must roll back the state change and every authorization revocation.
func TestRestrictionRollbackAndAuthorizationRevocation(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	customer := verified(t, s, e, "restrict-customer@example.test")
	actor := verified(t, s, e, "restrict-operator@example.test")
	if err := s.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	_, raw, err := s.Login(ctx, wire.LoginInput{Email: "restrict-customer@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	proof, mail, _, _ := resetProof(t, s, e, "restrict-customer@example.test")
	_, err = s.pool.Exec(ctx, `CREATE OR REPLACE FUNCTION reject_restriction_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='account_restricted' THEN RAISE EXCEPTION 'fixture rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_restriction BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_restriction_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetOperatorRestriction(ctx, actor, customer, uuid.New(), wire.OperatorRestrictionInput{Restricted: true, Reason: "reviewed"})
	if status(err) != 503 {
		t.Fatal("audit failure accepted", err)
	}
	var restricted, revoked bool
	var cipher []byte
	if err = s.pool.QueryRow(ctx, `SELECT restricted FROM accounts WHERE id=$1`, customer).Scan(&restricted); err != nil || restricted {
		t.Fatal("state survived rollback", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT revoked FROM credential_challenges WHERE id=$1`, proof).Scan(&revoked); err != nil || revoked {
		t.Fatal("proof revoked after rollback", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT ciphertext FROM mail_deliveries WHERE id=$1`, mail).Scan(&cipher); err != nil || len(cipher) == 0 {
		t.Fatal("ciphertext cleared after rollback", err)
	}
	if _, err = s.Authenticate(ctx, raw); err != nil {
		t.Fatal("session lost after rollback", err)
	}
	if _, err = s.pool.Exec(ctx, `DROP TRIGGER reject_restriction ON audit_events`); err != nil {
		t.Fatal(err)
	}
	result, err := s.SetOperatorRestriction(ctx, actor, customer, uuid.New(), wire.OperatorRestrictionInput{Restricted: true, Reason: "reviewed"})
	if err != nil || !result.Restricted {
		t.Fatal("restrict", err)
	}
	if _, err = s.Authenticate(ctx, raw); status(err) != 401 {
		t.Fatal("session survived restriction", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT revoked FROM credential_challenges WHERE id=$1`, proof).Scan(&revoked); err != nil || !revoked {
		t.Fatal("proof survived", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT ciphertext FROM mail_deliveries WHERE id=$1`, mail).Scan(&cipher); err != nil || len(cipher) != 0 {
		t.Fatal("ciphertext survived", err)
	}
	if _, _, err = s.Login(ctx, wire.LoginInput{Email: "restrict-customer@example.test", Password: "my long safe password ✨"}, "127.0.0.1"); status(err) != 403 {
		t.Fatal("restricted login", err)
	}
}

func TestRestrictionReasonLengthAfterTrim(t *testing.T) {
	s, customer, actor := operatorActors(t)
	_, err := s.SetOperatorRestriction(context.Background(), actor, customer, uuid.New(), wire.OperatorRestrictionInput{Restricted: true, Reason: "  " + strings.Repeat("x", 1000) + "  "})
	if err != nil {
		t.Fatal("trimmed 1000-rune reason rejected", err)
	}
}
