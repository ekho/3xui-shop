package catalogue

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

// Removing FOR UPDATE, switching to a new Tx, or returning index fields from Terms fails here.
func TestCatalogueTransactionPorts(t *testing.T) {
	s, e, _, actor, _ := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plan, err := s.CreateCataloguePlan(ctx, actor, uuid.New(), CreateInput{Terms: terms(2), Reason: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	seed, _, err := s.SeedUnlimitedCatalogue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	current, err := s.CurrentPlanTx(ctx, tx, plan.PlanId)
	if err != nil || current.ID != plan.PlanId || current.Revision != 1 || current.Hidden || current.Profile != "regular" || current.Terms.Prices[0].AmountMinor != "9007199254740993" {
		t.Fatal("current snapshot", err)
	}
	if _, err = s.CurrentPlanTx(ctx, tx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing plan", err)
	}
	unlimited, err := s.UnlimitedPlansTx(ctx, tx)
	if err != nil || len(unlimited) != 1 || unlimited[0].ID != seed.PlanId || !unlimited[0].Hidden || unlimited[0].Profile != "unlimited" || unlimited[0].Terms.Devices != 7 {
		t.Fatal("unlimited snapshot", err)
	}
	if _, err = s.LockCurrentPlan(ctx, tx, plan.PlanId); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := s.ReviseCataloguePlan(ctx, actor, plan.PlanId, uuid.New(), RevisionInput{ExpectedRevision: 1, Terms: terms(3), Reason: "concurrent edit"})
		finished <- err
	}()
	for {
		var waiting bool
		if err = e.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%CatalogueByIDForUpdate%')`).Scan(&waiting); err != nil {
			t.Fatal("lock observation", err)
		}
		if waiting {
			break
		}
		select {
		case err = <-finished:
			t.Fatal("revision escaped caller lock", err)
		case <-ctx.Done():
			t.Fatal("no blocked revision observed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal("revision after rollback", err)
	}
	if current.Revision != 1 || current.Terms.Devices != 2 {
		t.Fatal("old snapshot mutated")
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	if _, err = s.ReviseCataloguePlan(stopped, actor, plan.PlanId, uuid.New(), RevisionInput{ExpectedRevision: 2, Terms: terms(4), Reason: "cancelled"}); err == nil {
		t.Fatal("cancelled write succeeded")
	}
	var revisions int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM catalogue_revisions WHERE plan_id=$1", plan.PlanId).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatal("cancelled write changed history", err)
	}
}

// Unreadable archived legacy terms must not disable a still-valid unlimited grant.
func TestCatalogueInvalidTermsKeepsMetadata(t *testing.T) {
	s, e, _, _, _ := fixture(t)
	ctx := context.Background()
	if _, _, err := s.SeedUnlimitedCatalogue(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden,archived) VALUES($1,1,9,'unlimited',true,true)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,'{"devices":"invalid"}'::jsonb,true,'legacy_import',$2)`, id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	plan, err := s.CurrentPlanTx(ctx, tx, id)
	if !errors.Is(err, ErrInvalidTerms) || plan.ID != id || !plan.Archived || !plan.Hidden || plan.Profile != "unlimited" || plan.Revision != 1 {
		t.Fatal("metadata needed for original eligibility order", err)
	}
	rows, err := s.UnlimitedPlansTx(ctx, tx)
	if err != nil || len(rows) != 2 {
		t.Fatal("archived malformed terms rejected usable grant", err)
	}
}
