package httpapi

import (
	"context"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"strings"
	"testing"
)

// Miswiring either owner permits a revoked/restricted actor or loses the catalogue.
func TestCatalogueComposition(t *testing.T) {
	e := testkit.Open(t)
	s := composeForTest(e.Pool, e.Redis, nil, app.Config{})
	ctx := context.Background()
	actor, customer := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, customer} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
            VALUES($1,$2,'en','test-only-hash',$3,$4,$5,$6,'1','1')`, id, id.String()+"@example.test", e.Clock(), uuid.New(), strings.ReplaceAll(id.String(), "-", "")[:16], "acct_"+id.String()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	input := wire.CataloguePlanCreateInput{Reason: "composed", Terms: wire.CatalogueTerms{Devices: 2, TrafficGb: 0, Profile: "regular", Periods: []int64{30}, Prices: []wire.CataloguePrice{{AmountMinor: "9007199254740993", Currency: "RUB", PeriodDays: 30}, {AmountMinor: "0", Currency: "USD", PeriodDays: 30}, {AmountMinor: "0", Currency: "XTR", PeriodDays: 30}}}}
	plan, err := s.createCataloguePlan(ctx, actor, uuid.New(), input)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := s.catalogue(ctx, customer)
	if err != nil || len(visible.Plans) != 1 || visible.Plans[0].PlanId != plan.PlanId || visible.Plans[0].Prices[0].AmountMinor != "9007199254740993" {
		t.Fatal("composed list", err)
	}
	if err = s.Accounts.ChangeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.createCataloguePlan(ctx, actor, uuid.New(), input); err == nil || err.Error() != "INVALID_CREDENTIALS" {
		t.Fatal("composed revoke", err)
	}
	if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", customer); err != nil {
		t.Fatal(err)
	}
	if _, err = s.catalogue(ctx, customer); err == nil || err.Error() != "ACCOUNT_RESTRICTED" {
		t.Fatal("composed restriction", err)
	}
}
