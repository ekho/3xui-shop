package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAccountsRestrictionReplay(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	actor := verified(t, s, e, cfg, "operator@example.test")
	target := verified(t, s, e, cfg, "customer@example.test")
	if err := s.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	// The pre-M02 serializer used these exact property names and order.
	body := fmt.Sprintf(`{"Target":"%s","Input":{"reason":" reviewed ","restricted":true}}`, target)
	hash := sha256.Sum256([]byte(body))
	changed := e.Clock().UTC().Truncate(time.Microsecond)
	prior := fmt.Sprintf(`{"changed_at":"%s","operator_account_id":"%s","restricted":true}`, changed.Format(time.RFC3339Nano), actor)
	if _, err := e.Pool.Exec(ctx, `INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,'setOperatorRestriction',$2,$3,$4,$5)`, "operator-account:"+actor.String(), key, hash[:], json.RawMessage(prior), changed); err != nil {
		t.Fatal(err)
	}
	in := OperatorRestrictionInput{Reason: " reviewed ", Restricted: true}
	result, err := s.SetOperatorRestriction(ctx, actor, target, key, in)
	if err != nil || !result.Restricted || result.ChangedAt == nil || !result.ChangedAt.Equal(changed) || result.OperatorAccountId == nil || *result.OperatorAccountId != actor {
		t.Fatalf("legacy replay changed result: %v", err)
	}
	in.Reason = "changed"
	if _, err = s.SetOperatorRestriction(ctx, actor, target, key, in); !hasStatus(err, 409) {
		t.Fatal("legacy body hash was ignored")
	}
	if err = s.ChangeOperatorRole(ctx, target, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetOperatorRestriction(ctx, actor, target, uuid.New(), in); !hasStatus(err, 403) {
		t.Fatal("operator target was not protected")
	}
	if err = s.ChangeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetOperatorRestriction(ctx, actor, target, uuid.New(), in); !hasStatus(err, 403) {
		t.Fatal("revoked actor changed account")
	}
	var restricted bool
	var audits int
	if err = e.Pool.QueryRow(ctx, `SELECT restricted FROM accounts WHERE id=$1`, target).Scan(&restricted); err != nil || restricted {
		t.Fatal("replay or denied operation mutated target", err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='account_restricted'`, target).Scan(&audits); err != nil || audits != 0 {
		t.Fatal("replay or denied operation added audit", err)
	}
}
