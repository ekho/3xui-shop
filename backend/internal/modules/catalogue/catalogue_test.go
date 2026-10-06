package catalogue

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func fixture(t *testing.T) (*Service, *testkit.Env, *accounts.Service, uuid.UUID, uuid.UUID) {
	t.Helper()
	e := testkit.Open(t)
	ctx := context.Background()
	actor, customer := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, customer} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
            VALUES($1,$2,'en','test-only-hash',$3,$4,$5,$6,'1','1')`, id, id.String()+"@example.test", e.Clock(), uuid.New(), strings.ReplaceAll(id.String(), "-", "")[:16], "acct_"+id.String()); err != nil {
			t.Fatal(err)
		}
	}
	authority := accounts.New(e.Pool, e.Redis, nil, accounts.Config{Now: e.Clock})
	if err := authority.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	return New(e.Pool, authority, e.Clock), e, authority, actor, customer
}

func terms(devices int) Terms {
	return Terms{Devices: devices, TrafficGb: 0, Profile: "regular", Periods: []int64{30}, Prices: []Price{
		{PeriodDays: 30, Currency: "RUB", AmountMinor: "9007199254740993"},
		{PeriodDays: 30, Currency: "USD", AmountMinor: "12345"},
		{PeriodDays: 30, Currency: "XTR", AmountMinor: "0"},
	}}
}

// A second rules implementation, rounded price, leaked hidden plan or changed clock fails here.
func TestCatalogueOwner(t *testing.T) {
	s, e, authority, actor, customer := fixture(t)
	ctx := context.Background()
	input := CreateInput{Terms: terms(2), Reason: "  initial ✨  "}
	plan, err := s.CreateCataloguePlan(ctx, actor, uuid.New(), input)
	if err != nil || plan.ActorAccountId == nil || *plan.ActorAccountId != actor || plan.Reason == nil || *plan.Reason != "initial ✨" || !plan.ChangedAt.Equal(e.Clock()) {
		t.Fatal("owner create", err)
	}
	seed, created, err := s.SeedUnlimitedCatalogue(ctx)
	if err != nil || !created || seed.Devices != 7 || !seed.Hidden || seed.ActorAccountId != nil || seed.Reason != nil || len(seed.Periods) != 0 || len(seed.Prices) != 0 {
		t.Fatal("explicit seed", err)
	}
	visible, err := s.Catalogue(ctx, customer)
	if err != nil || len(visible.Plans) != 1 || visible.Plans[0].Prices[0].AmountMinor != "9007199254740993" {
		t.Fatal("visible exact price", err)
	}
	updated, err := s.ReviseCataloguePlan(ctx, actor, plan.PlanId, uuid.New(), RevisionInput{ExpectedRevision: 1, Terms: terms(3), Reason: "update"})
	if err != nil || updated.Revision != 2 || updated.Devices != 3 {
		t.Fatal("revision", err)
	}
	var old []byte
	if err = e.Pool.QueryRow(ctx, "SELECT terms FROM catalogue_revisions WHERE plan_id=$1 AND revision=1", plan.PlanId).Scan(&old); err != nil {
		t.Fatal(err)
	}
	var historical Terms
	if json.Unmarshal(old, &historical) != nil || historical.Devices != 2 || historical.Prices[0].AmountMinor != "9007199254740993" {
		t.Fatal("historical revision changed")
	}
	pkg := LegacyCataloguePackage{Version: 1, Durations: []int64{30}, Plans: []LegacyCataloguePlan{{LegacyPlanID: 100, Devices: 4, Profile: "regular", Prices: map[string]map[string]string{"RUB": {"30": "123.45"}, "USD": {"30": "0"}, "XTR": {"30": "9"}}}}}
	if result, err := s.ImportLegacyCatalogue(ctx, pkg, false); err != nil || result.Created != 1 {
		t.Fatal("import", err)
	}
	if err = authority.ChangeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateCataloguePlan(ctx, actor, uuid.New(), CreateInput{Terms: terms(5), Reason: "revoked"}); err == nil || err.Error() != "INVALID_CREDENTIALS" {
		t.Fatal("revoked authority", err)
	}
}
