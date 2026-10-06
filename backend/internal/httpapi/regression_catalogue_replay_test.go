package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"testing"
)

// Reordering DTO JSON or hashing trimmed/canonical input breaks pre-extraction records.
func TestRegressionCataloguePersistedReplay(t *testing.T) {
	s, _, actor := operatorActors(t)
	ctx := context.Background()
	plan := uuid.New()
	conditions := catalogueTerms(3)
	conditions.Prices[0], conditions.Prices[2] = conditions.Prices[2], conditions.Prices[0]
	create := wire.CataloguePlanCreateInput{Terms: conditions, Reason: "  original ✨  "}
	revise := wire.CataloguePlanRevisionInput{Terms: conditions, ExpectedRevision: 1, Reason: "  original ✨  "}
	archive := wire.CataloguePlanArchiveInput{ExpectedRevision: 2, Reason: "  original ✨  "}
	legacyWrapper := func(expected int64, value *wire.CatalogueTerms) any {
		return struct {
			ID       uuid.UUID
			Expected int64
			Reason   string
			Terms    *wire.CatalogueTerms
		}{plan, expected, "  original ✨  ", value}
	}
	cases := []struct {
		operation string
		input     any
		revision  int64
		archived  bool
		call      func(uuid.UUID) (wire.OperatorCataloguePlan, error)
	}{
		{"createCataloguePlan", create, 1, false, func(key uuid.UUID) (wire.OperatorCataloguePlan, error) {
			return s.createCataloguePlan(ctx, actor, key, create)
		}},
		{"reviseCataloguePlan", legacyWrapper(1, &conditions), 2, false, func(key uuid.UUID) (wire.OperatorCataloguePlan, error) {
			return s.reviseCataloguePlan(ctx, actor, plan, key, revise)
		}},
		{"archiveCataloguePlan", legacyWrapper(2, nil), 3, true, func(key uuid.UUID) (wire.OperatorCataloguePlan, error) {
			return s.archiveCataloguePlan(ctx, actor, plan, key, archive)
		}},
	}
	for _, c := range cases {
		t.Run(c.operation, func(t *testing.T) {
			key := uuid.New()
			reason := "original ✨"
			old := wire.OperatorCataloguePlan{PlanId: plan, Revision: c.revision, Archived: c.archived, ActorAccountId: &actor, ChangedAt: s.now(), Reason: &reason, Source: "operator", Devices: 3, TrafficGb: 100, Profile: "regular", Periods: []int64{30}, Prices: []wire.CataloguePrice{{PeriodDays: 30, Currency: "RUB", AmountMinor: "0"}, {PeriodDays: 30, Currency: "USD", AmountMinor: "12345"}, {PeriodDays: 30, Currency: "XTR", AmountMinor: "0"}}}
			oldJSON, _ := json.Marshal(old)
			body, _ := json.Marshal(c.input)
			hash := sha256.Sum256(body)
			if _, err := s.pool.Exec(ctx, `INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6)`, "operator-account:"+actor.String(), c.operation, key, hash[:], oldJSON, s.now()); err != nil {
				t.Fatal(err)
			}
			got, err := c.call(key)
			gotJSON, _ := json.Marshal(got)
			if err != nil || !bytes.Equal(gotJSON, oldJSON) {
				t.Fatal("old replay lost", err)
			}
			unchanged, _ := json.Marshal(c.input)
			if !bytes.Equal(body, unchanged) {
				t.Fatal("input mutated before hashing replay")
			}
		})
	}
	var revisions int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM catalogue_revisions").Scan(&revisions); err != nil || revisions != 0 {
		t.Fatal("replay wrote another revision", err)
	}
	// Reuse a real persisted key to prove a different request remains a conflict.
	var createKey uuid.UUID
	if err := s.pool.QueryRow(ctx, "SELECT key FROM idempotency_records WHERE operation='createCataloguePlan'").Scan(&createKey); err != nil {
		t.Fatal(err)
	}
	create.Reason = "different"
	if _, err := s.createCataloguePlan(ctx, actor, createKey, create); !catalogueCode(err, "IDEMPOTENCY_CONFLICT") {
		t.Fatal("different body replay", err)
	}
	create.Reason = "  original ✨  "
	if err := s.changeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.createCataloguePlan(ctx, actor, createKey, create); status(err) != 403 {
		t.Fatal("revoked role replay", err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.createCataloguePlan(ctx, actor, createKey, create); !catalogueCode(err, "ACCOUNT_RESTRICTED") {
		t.Fatal("restricted replay", err)
	}
}
