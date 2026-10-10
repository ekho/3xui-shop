package catalogue

import (
	"context"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
)

func TestLegacyCatalogueTxAndStandaloneDryRun(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := New(e.Pool, nil, e.Clock)
	p := LegacyCataloguePackage{Version: 1, Durations: []int64{30}, Plans: []LegacyCataloguePlan{{LegacyPlanID: 123, Devices: 2, Profile: "regular", Prices: map[string]map[string]string{"RUB": {"30": "199.00"}, "USD": {"30": "2.00"}, "XTR": {"30": "100"}}}}}
	if out, err := s.ImportLegacyCatalogue(ctx, p, true); err != nil || !out.DryRun || out.Created != 1 {
		t.Fatal("standalone dry run", out, err)
	}
	var count int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM catalogue_plans WHERE legacy_plan_id=123`).Scan(&count); err != nil || count != 0 {
		t.Fatal("standalone dry run persisted plan", err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if out, err := s.ImportLegacyCatalogueTx(ctx, tx, p, false); err != nil || out.Created != 1 {
		t.Fatal("shared transaction import", out, err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM catalogue_plans WHERE legacy_plan_id=123`).Scan(&count); err != nil || count != 1 {
		t.Fatal("plan missing inside transaction", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM catalogue_plans WHERE legacy_plan_id=123`).Scan(&count); err != nil || count != 0 {
		t.Fatal("plan escaped rollback", err)
	}
}
