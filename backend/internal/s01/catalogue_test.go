package s01

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sync"
	"testing"
)

func TestCatalogueRejectsInvalidConditions(t *testing.T) {
	s, _, actor := operatorActors(t)
	ctx := context.Background()
	cases := []wire.CatalogueTerms{}
	bad := catalogueTerms(1)
	bad.Prices = bad.Prices[:2]
	cases = append(cases, bad)
	bad = catalogueTerms(1)
	bad.Prices[1].Currency = "RUB"
	cases = append(cases, bad)
	bad = catalogueTerms(1)
	bad.Prices[0].AmountMinor = "9223372036854775808"
	cases = append(cases, bad)
	bad = catalogueTerms(1)
	bad.Profile = "unknown"
	cases = append(cases, bad)
	bad = catalogueTerms(1)
	bad.Devices = 0
	cases = append(cases, bad)
	bad = catalogueTerms(1)
	bad.TrafficGb = 100001
	cases = append(cases, bad)
	bad = catalogueTerms(1)
	bad.Periods = []int64{106752}
	cases = append(cases, bad)
	bad = catalogueTerms(1)
	bad.Profile = "unlimited"
	cases = append(cases, bad)
	for i, terms := range cases {
		if _, err := s.CreateCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: terms, Reason: "bad"}); !catalogueCode(err, "INVALID_INPUT") {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

func TestCatalogueLegacyImportAndSeed(t *testing.T) {
	s, customer, actor := operatorActors(t)
	ctx := context.Background()
	seed, created, err := s.SeedUnlimitedCatalogue(ctx)
	if err != nil || !created || seed.Devices != 7 || !seed.Hidden || seed.ActorAccountId != nil {
		t.Fatalf("seed: %v", err)
	}
	seed2, created, err := s.SeedUnlimitedCatalogue(ctx)
	if err != nil || created || seed2.PlanId != seed.PlanId {
		t.Fatalf("seed replay: %v", err)
	}
	pkg := LegacyCataloguePackage{Version: 1, Durations: []int64{30}, Plans: []LegacyCataloguePlan{{LegacyPlanID: 10, Devices: 1, TrafficGB: 0, Profile: "regular", Prices: map[string]map[string]string{"RUB": {"30": "123.45"}, "USD": {"30": "0"}, "XTR": {"30": "9"}}}}}
	result, err := s.ImportLegacyCatalogue(ctx, pkg, true)
	if err != nil || result.Created != 1 {
		t.Fatalf("dry run: %v", err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM catalogue_plans").Scan(&count); err != nil || count != 1 {
		t.Fatalf("dry run wrote: %v", err)
	}
	result, err = s.ImportLegacyCatalogue(ctx, pkg, false)
	if err != nil || result.Created != 1 {
		t.Fatalf("apply: %v", err)
	}
	result, err = s.ImportLegacyCatalogue(ctx, pkg, false)
	if err != nil || result.Replayed != 1 {
		t.Fatalf("replay: %v", err)
	}
	visible, err := s.Catalogue(ctx, customer)
	if err != nil || len(visible.Plans) != 1 || visible.Plans[0].Prices[0].AmountMinor != "12345" {
		t.Fatalf("visible: %v", err)
	}
	importedID := visible.Plans[0].PlanId
	_, err = s.ReviseCataloguePlan(ctx, actor, importedID, uuid.New(), wire.CataloguePlanRevisionInput{ExpectedRevision: 1, Terms: catalogueTerms(2), Reason: "operator edit"})
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.ImportLegacyCatalogue(ctx, pkg, false)
	if err != nil || result.Replayed != 1 {
		t.Fatalf("operator edit overwritten: %v", err)
	}
	wrong := pkg
	wrong.Plans = append([]LegacyCataloguePlan{}, pkg.Plans...)
	wrong.Plans[0].Prices = map[string]map[string]string{"RUB": {"30": "123.46"}, "USD": {"30": "0"}, "XTR": {"30": "9"}}
	if _, err = s.ImportLegacyCatalogue(ctx, wrong, false); !catalogueCode(err, "IMPORT_CONFLICT") {
		t.Fatalf("identity conflict: %v", err)
	}
	rollback := wrong
	rollback.Plans = append([]LegacyCataloguePlan{{LegacyPlanID: 13, Devices: 3, TrafficGB: 0, Profile: "regular", Prices: pkg.Plans[0].Prices}}, wrong.Plans...)
	if _, err = s.ImportLegacyCatalogue(ctx, rollback, false); !catalogueCode(err, "IMPORT_CONFLICT") {
		t.Fatalf("later conflict: %v", err)
	}
	var rolledBack bool
	if err = s.pool.QueryRow(ctx, "SELECT NOT EXISTS(SELECT 1 FROM catalogue_plans WHERE legacy_plan_id=13)").Scan(&rolledBack); err != nil || !rolledBack {
		t.Fatalf("partial import before conflict: %v", err)
	}
	bad := pkg
	bad.Plans = []LegacyCataloguePlan{{LegacyPlanID: 11, Devices: 3, TrafficGB: 0, Profile: "regular", Prices: pkg.Plans[0].Prices}, {LegacyPlanID: 12, Devices: 4, TrafficGB: 0, Profile: "regular", Prices: map[string]map[string]string{"RUB": {"30": "0.001"}, "USD": {"30": "0"}, "XTR": {"30": "0"}}}}
	if _, err = s.ImportLegacyCatalogue(ctx, bad, false); !catalogueCode(err, "IMPORT_INVALID_PACKAGE") {
		t.Fatalf("fraction accepted: %v", err)
	}
	var absent bool
	if err = s.pool.QueryRow(ctx, "SELECT NOT EXISTS(SELECT 1 FROM catalogue_plans WHERE legacy_plan_id=11)").Scan(&absent); err != nil || !absent {
		t.Fatalf("partial import: %v", err)
	}
	var oldRaw []byte
	if err = s.pool.QueryRow(ctx, "SELECT terms FROM catalogue_revisions WHERE plan_id=$1 AND revision=1", importedID).Scan(&oldRaw); err != nil {
		t.Fatal(err)
	}
	if len(oldRaw) == 0 {
		t.Fatal(pgx.ErrNoRows)
	}
	var old wire.CatalogueTerms
	if err = json.Unmarshal(oldRaw, &old); err != nil || old.Prices[0].AmountMinor != "12345" {
		t.Fatalf("old revision changed: %v", err)
	}
	if _, err = s.pool.Exec(ctx, "UPDATE catalogue_revisions SET terms='{}'::jsonb WHERE plan_id=$1 AND revision=1", importedID); err == nil {
		t.Fatal("immutable revision updated")
	}
}

func catalogueTerms(devices int) wire.CatalogueTerms {
	return wire.CatalogueTerms{Devices: devices, TrafficGb: 100, Profile: "regular", Periods: []int64{30}, Prices: []wire.CataloguePrice{{PeriodDays: 30, Currency: "RUB", AmountMinor: "0"}, {PeriodDays: 30, Currency: "USD", AmountMinor: "12345"}, {PeriodDays: 30, Currency: "XTR", AmountMinor: "0"}}}
}

func catalogueCode(err error, code string) bool { return err != nil && err.Error() == code }

func TestCatalogueVersionsReplayAndConcurrentGuards(t *testing.T) {
	s, customer, actor := operatorActors(t)
	ctx := context.Background()
	one, err := s.CreateCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(1), Reason: "one"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.CreateCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(2), Reason: "two"})
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	revision := wire.CataloguePlanRevisionInput{ExpectedRevision: 1, Terms: catalogueTerms(3), Reason: "revised"}
	updated, err := s.ReviseCataloguePlan(ctx, actor, one.PlanId, key, revision)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("revision: %v", err)
	}
	replayed, err := s.ReviseCataloguePlan(ctx, actor, one.PlanId, key, revision)
	if err != nil || replayed.Revision != 2 {
		t.Fatalf("replay: %v", err)
	}
	if _, err = s.ReviseCataloguePlan(ctx, actor, one.PlanId, key, wire.CataloguePlanRevisionInput{ExpectedRevision: 1, Terms: catalogueTerms(4), Reason: "changed"}); !catalogueCode(err, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("key mismatch: %v", err)
	}
	if _, err = s.ReviseCataloguePlan(ctx, actor, one.PlanId, uuid.New(), revision); !catalogueCode(err, "CATALOGUE_REVISION_CONFLICT") {
		t.Fatalf("stale: %v", err)
	}
	if _, err = s.CreateCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(3), Reason: "slot"}); !catalogueCode(err, "CATALOGUE_DEVICES_CONFLICT") {
		t.Fatalf("slot: %v", err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, id := range []uuid.UUID{one.PlanId, two.PlanId} {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			_, e := s.ArchiveCataloguePlan(ctx, actor, id, uuid.New(), wire.CataloguePlanArchiveInput{ExpectedRevision: map[uuid.UUID]int64{one.PlanId: 2, two.PlanId: 1}[id], Reason: "archive"})
			outcomes <- e
		}(id)
	}
	wg.Wait()
	close(outcomes)
	success, guard := 0, 0
	for e := range outcomes {
		if e == nil {
			success++
		} else if catalogueCode(e, "CATALOGUE_LAST_VISIBLE") {
			guard++
		} else {
			t.Fatalf("unexpected archive: %v", e)
		}
	}
	if success != 1 || guard != 1 {
		t.Fatalf("concurrent archive: success=%d guard=%d", success, guard)
	}
	visible, err := s.Catalogue(ctx, customer)
	if err != nil || len(visible.Plans) != 1 {
		t.Fatalf("visible: %v", err)
	}
}

func TestCatalogueCanonicalizationDoesNotMutateReplayInput(t *testing.T) {
	s, _, actor := operatorActors(t)
	terms := catalogueTerms(5)
	terms.Prices[0], terms.Prices[2] = terms.Prices[2], terms.Prices[0]
	in := wire.CataloguePlanCreateInput{Terms: terms, Reason: "same request"}
	key := uuid.New()
	first, err := s.CreateCataloguePlan(context.Background(), actor, key, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateCataloguePlan(context.Background(), actor, key, in)
	if err != nil || first.PlanId != second.PlanId {
		t.Fatalf("identical input replay: %v", err)
	}
}

// A price converted through float64, or a hidden plan exposed to the client, fails here.
func TestCatalogueExactPricesAndVisibility(t *testing.T) {
	s, customer, actor := operatorActors(t)
	ctx := context.Background()
	in := wire.CataloguePlanCreateInput{Terms: wire.CatalogueTerms{
		Devices: 3, TrafficGb: 0, Profile: "regular", Hidden: false,
		Periods: []int64{30}, Prices: []wire.CataloguePrice{
			{PeriodDays: 30, Currency: "RUB", AmountMinor: "9007199254740993"},
			{PeriodDays: 30, Currency: "USD", AmountMinor: "12345"},
			{PeriodDays: 30, Currency: "XTR", AmountMinor: "0"},
		},
	}, Reason: "initial"}
	created, err := s.CreateCataloguePlan(ctx, actor, uuid.New(), in)
	if err != nil || created.PlanId == uuid.Nil || created.Revision != 1 {
		t.Fatal("create", err)
	}
	list, err := s.Catalogue(ctx, customer)
	if err != nil || len(list.Plans) != 1 || list.Plans[0].Prices[0].AmountMinor != "9007199254740993" {
		t.Fatal("exact client price", err)
	}
}

func TestCatalogueRestrictedReaderAndLiveOperator(t *testing.T) {
	s, customer, actor := operatorActors(t)
	ctx := context.Background()
	created, err := s.CreateCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(1), Reason: "before restriction"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1 OR id=$2", customer, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Catalogue(ctx, customer); !catalogueCode(err, "ACCOUNT_RESTRICTED") {
		t.Fatalf("restricted reader: %v", err)
	}
	if _, err = s.OperatorCatalogue(ctx, actor, 1, 50); !catalogueCode(err, "ACCOUNT_RESTRICTED") {
		t.Fatalf("restricted operator read: %v", err)
	}
	if _, err = s.CreateCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(2), Reason: "denied"}); !catalogueCode(err, "ACCOUNT_RESTRICTED") {
		t.Fatalf("restricted operator create: %v", err)
	}
	if _, err = s.ReviseCataloguePlan(ctx, actor, created.PlanId, uuid.New(), wire.CataloguePlanRevisionInput{Terms: catalogueTerms(2), ExpectedRevision: 1, Reason: "denied"}); !catalogueCode(err, "ACCOUNT_RESTRICTED") {
		t.Fatalf("restricted operator revision: %v", err)
	}
	var revisions int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM catalogue_revisions WHERE plan_id=$1", created.PlanId).Scan(&revisions); err != nil || revisions != 1 {
		t.Fatalf("restricted write changed revisions: count=%d err=%v", revisions, err)
	}
}
