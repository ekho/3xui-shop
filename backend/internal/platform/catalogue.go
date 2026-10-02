package platform

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"sort"
	"strconv"
	"strings"
	"time"
)

func validateCatalogueTerms(t wire.CatalogueTerms) error {
	if t.Devices < 1 || t.Devices > 10000 || t.TrafficGb < 0 || t.TrafficGb > 100000 ||
		(t.Profile != "regular" && t.Profile != "euru" && t.Profile != "unlimited") ||
		(t.Profile == "unlimited" && !t.Hidden) || len(t.Periods) > 100 || len(t.Prices) > 300 {
		return failure(400, "INVALID_INPUT")
	}
	periods := map[int64]bool{}
	for _, d := range t.Periods {
		if d < 1 || d > 106751 || periods[d] {
			return failure(400, "INVALID_INPUT")
		}
		periods[d] = true
	}
	if !t.Hidden && len(periods) == 0 {
		return failure(400, "INVALID_INPUT")
	}
	seen := map[string]bool{}
	for _, p := range t.Prices {
		if !periods[p.PeriodDays] || (p.Currency != "RUB" && p.Currency != "USD" && p.Currency != "XTR") || len(p.AmountMinor) > 19 || len(p.AmountMinor) == 0 || (len(p.AmountMinor) > 1 && p.AmountMinor[0] == '0') {
			return failure(400, "INVALID_INPUT")
		}
		for _, c := range p.AmountMinor {
			if c < '0' || c > '9' {
				return failure(400, "INVALID_INPUT")
			}
		}
		if _, err := strconv.ParseInt(p.AmountMinor, 10, 64); err != nil {
			return failure(400, "INVALID_INPUT")
		}
		k := strconv.FormatInt(p.PeriodDays, 10) + ":" + string(p.Currency)
		if seen[k] {
			return failure(400, "INVALID_INPUT")
		}
		seen[k] = true
	}
	if len(t.Prices) != 0 || !t.Hidden {
		if len(seen) != len(periods)*3 {
			return failure(400, "INVALID_INPUT")
		}
	}
	return nil
}

func canonicalTerms(t wire.CatalogueTerms) wire.CatalogueTerms {
	t.Periods = append([]int64(nil), t.Periods...)
	t.Prices = append([]wire.CataloguePrice(nil), t.Prices...)
	if t.Periods == nil {
		t.Periods = []int64{}
	}
	if t.Prices == nil {
		t.Prices = []wire.CataloguePrice{}
	}
	sort.Slice(t.Periods, func(i, j int) bool { return t.Periods[i] < t.Periods[j] })
	sort.Slice(t.Prices, func(i, j int) bool {
		if t.Prices[i].PeriodDays != t.Prices[j].PeriodDays {
			return t.Prices[i].PeriodDays < t.Prices[j].PeriodDays
		}
		order := map[wire.CataloguePriceCurrency]int{"RUB": 0, "USD": 1, "XTR": 2}
		return order[t.Prices[i].Currency] < order[t.Prices[j].Currency]
	})
	return t
}

func catalogueSnapshot(id uuid.UUID, rev int64, raw []byte) (wire.CataloguePlanSnapshot, error) {
	var t wire.CatalogueTerms
	if json.Unmarshal(raw, &t) != nil {
		return wire.CataloguePlanSnapshot{}, unavailable()
	}
	return wire.CataloguePlanSnapshot{PlanId: id, Revision: rev, Devices: t.Devices, TrafficGb: t.TrafficGb, Profile: wire.CataloguePlanSnapshotProfile(t.Profile), Hidden: t.Hidden, Periods: t.Periods, Prices: t.Prices}, nil
}

func operatorCatalogue(id uuid.UUID, legacy pgtype.Int8, rev int64, archived bool, raw []byte, actor *uuid.UUID, source string, changed pgtype.Timestamptz, reason pgtype.Text) (wire.OperatorCataloguePlan, error) {
	var t wire.CatalogueTerms
	if json.Unmarshal(raw, &t) != nil || !changed.Valid {
		return wire.OperatorCataloguePlan{}, unavailable()
	}
	o := wire.OperatorCataloguePlan{PlanId: id, Revision: rev, Devices: t.Devices, TrafficGb: t.TrafficGb, Profile: wire.OperatorCataloguePlanProfile(t.Profile), Hidden: t.Hidden, Periods: t.Periods, Prices: t.Prices, Archived: archived, ActorAccountId: actor, Source: wire.OperatorCataloguePlanSource(source), ChangedAt: changed.Time}
	if legacy.Valid {
		v := strconv.FormatInt(legacy.Int64, 10)
		o.LegacyPlanId = &v
	}
	if reason.Valid {
		o.Reason = &reason.String
	}
	return o, nil
}

func catalogueWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		if pg.ConstraintName == "catalogue_active_devices" {
			return failure(409, "CATALOGUE_DEVICES_CONFLICT")
		}
	}
	return unavailable()
}

func lockCatalogue(ctx context.Context, tx pgx.Tx) error {
	// ponytail: One catalogue-wide lock is intentional; if writes become frequent, use per-plan locks plus a separate last-visible guard.
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(9060247201)")
	if err != nil {
		return unavailable()
	}
	return nil
}

func catalogueSlot(ctx context.Context, tx pgx.Tx, devices int, except uuid.UUID) error {
	var occupied bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM catalogue_plans WHERE NOT archived AND current_devices=$1 AND id<>$2)", devices, except).Scan(&occupied); err != nil {
		return unavailable()
	}
	if occupied {
		return failure(409, "CATALOGUE_DEVICES_CONFLICT")
	}
	return nil
}

func (s *Service) Catalogue(ctx context.Context, actor uuid.UUID) (wire.CatalogueResult, error) {
	out := wire.CatalogueResult{Plans: []wire.CataloguePlanSnapshot{}}
	if actor == uuid.Nil {
		return out, failure(401, "INVALID_CREDENTIALS")
	}
	q := store.New(s.pool)
	a, err := q.AccountByID(ctx, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return out, unavailable()
	}
	if a.Restricted {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	rows, err := q.CatalogueVisible(ctx)
	if err != nil {
		return out, unavailable()
	}
	for _, r := range rows {
		item, e := catalogueSnapshot(r.ID, r.CurrentRevision, r.Terms)
		if e != nil {
			return out, e
		}
		out.Plans = append(out.Plans, item)
	}
	return out, nil
}

func (s *Service) OperatorCatalogue(ctx context.Context, actor uuid.UUID, page, perPage int) (wire.OperatorCatalogueResult, error) {
	out := wire.OperatorCatalogueResult{Plans: []wire.OperatorCataloguePlan{}, Page: page, PerPage: perPage}
	if page < 1 || perPage < 1 || perPage > 50 || int64(page-1) > (int64(^uint64(0)>>1)/int64(perPage)) {
		return out, failure(400, "INVALID_INPUT")
	}
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
		return out, err
	}
	q := store.New(s.pool)
	count, err := q.CatalogueOperatorCount(ctx)
	if err != nil {
		return out, unavailable()
	}
	rows, err := q.CatalogueOperatorPage(ctx, store.CatalogueOperatorPageParams{PageOffset: int64(page-1) * int64(perPage), PageLimit: int32(perPage)})
	if err != nil {
		return out, unavailable()
	}
	out.Total = count
	for _, r := range rows {
		item, e := operatorCatalogue(r.ID, r.LegacyPlanID, r.CurrentRevision, r.Archived, r.Terms, r.ActorAccountID, r.Source, r.ChangedAt, r.Reason)
		if e != nil {
			return out, e
		}
		out.Plans = append(out.Plans, item)
	}
	return out, nil
}

func (s *Service) CreateCataloguePlan(ctx context.Context, actor, key uuid.UUID, in wire.CataloguePlanCreateInput) (wire.OperatorCataloguePlan, error) {
	var out wire.OperatorCataloguePlan
	reason := strings.TrimSpace(in.Reason)
	if actor == uuid.Nil || key == uuid.Nil || !validText(reason, 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	if err := validateCatalogueTerms(in.Terms); err != nil {
		return out, err
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(in)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.lockOperatorPair(ctx, tx, actor, actor); err != nil {
		return out, err
	}
	q := store.New(tx)
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "createCataloguePlan", Key: key}); err != nil {
		return out, unavailable()
	}
	if prior, found, e := replay[wire.OperatorCataloguePlan](ctx, q, principal, "createCataloguePlan", key, hash); found || e != nil {
		return prior, e
	}
	if err = lockCatalogue(ctx, tx); err != nil {
		return out, err
	}
	terms := canonicalTerms(in.Terms)
	if err = catalogueSlot(ctx, tx, terms.Devices, uuid.Nil); err != nil {
		return out, err
	}
	id := uuid.New()
	now := s.now().UTC().Truncate(time.Microsecond)
	if err = insertCatalogue(ctx, q, id, pgtype.Int8{}, terms, &actor, "operator", reason, now); err != nil {
		return out, err
	}
	raw, _ := json.Marshal(terms)
	out, err = operatorCatalogue(id, pgtype.Int8{}, 1, false, raw, &actor, "operator", stamp(now), pgtype.Text{String: reason, Valid: true})
	if err != nil {
		return out, err
	}
	if err = s.saveIdempotency(ctx, q, principal, "createCataloguePlan", key, hash, out); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	return out, nil
}

func insertCatalogue(ctx context.Context, q *store.Queries, id uuid.UUID, legacy pgtype.Int8, t wire.CatalogueTerms, actor *uuid.UUID, source, reason string, now time.Time) error {
	if err := q.InsertCataloguePlan(ctx, store.InsertCataloguePlanParams{ID: id, LegacyPlanID: legacy, CurrentRevision: 1, CurrentDevices: int32(t.Devices), CurrentProfile: string(t.Profile), CurrentHidden: t.Hidden}); err != nil {
		return catalogueWriteError(err)
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return unavailable()
	}
	var text pgtype.Text
	if reason != "" {
		text = pgtype.Text{String: reason, Valid: true}
	}
	if err := q.InsertCatalogueRevision(ctx, store.InsertCatalogueRevisionParams{PlanID: id, Revision: 1, Terms: raw, ActorAccountID: actor, Source: source, ChangedAt: stamp(now), Reason: text}); err != nil {
		return catalogueWriteError(err)
	}
	return nil
}

func (s *Service) ReviseCataloguePlan(ctx context.Context, actor, id, key uuid.UUID, in wire.CataloguePlanRevisionInput) (wire.OperatorCataloguePlan, error) {
	return s.changeCatalogue(ctx, actor, id, key, in.ExpectedRevision, in.Reason, &in.Terms, false)
}
func (s *Service) ArchiveCataloguePlan(ctx context.Context, actor, id, key uuid.UUID, in wire.CataloguePlanArchiveInput) (wire.OperatorCataloguePlan, error) {
	return s.changeCatalogue(ctx, actor, id, key, in.ExpectedRevision, in.Reason, nil, true)
}
func (s *Service) changeCatalogue(ctx context.Context, actor, id, key uuid.UUID, expected int64, reason string, terms *wire.CatalogueTerms, archive bool) (wire.OperatorCataloguePlan, error) {
	var out wire.OperatorCataloguePlan
	rawReason := reason
	reason = strings.TrimSpace(reason)
	if actor == uuid.Nil || id == uuid.Nil || key == uuid.Nil || expected < 1 || !validText(reason, 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	if terms != nil {
		if err := validateCatalogueTerms(*terms); err != nil {
			return out, err
		}
	}
	operation := "reviseCataloguePlan"
	if archive {
		operation = "archiveCataloguePlan"
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		ID       uuid.UUID
		Expected int64
		Reason   string
		Terms    *wire.CatalogueTerms
	}{id, expected, rawReason, terms})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.lockOperatorPair(ctx, tx, actor, actor); err != nil {
		return out, err
	}
	q := store.New(tx)
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: operation, Key: key}); err != nil {
		return out, unavailable()
	}
	if prior, found, e := replay[wire.OperatorCataloguePlan](ctx, q, principal, operation, key, hash); found || e != nil {
		return prior, e
	}
	if err = lockCatalogue(ctx, tx); err != nil {
		return out, err
	}
	current, err := q.CatalogueByIDForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	if current.Archived || current.CurrentRevision != expected {
		return out, failure(409, "CATALOGUE_REVISION_CONFLICT")
	}
	var next wire.CatalogueTerms
	if archive {
		if json.Unmarshal(current.Terms, &next) != nil {
			return out, unavailable()
		}
	} else {
		next = canonicalTerms(*terms)
	}
	if !current.CurrentHidden && (archive || next.Hidden) {
		count, e := q.CatalogueVisibleCount(ctx)
		if e != nil {
			return out, unavailable()
		}
		if count <= 1 {
			return out, failure(409, "CATALOGUE_LAST_VISIBLE")
		}
	}
	if !archive {
		if err = catalogueSlot(ctx, tx, next.Devices, id); err != nil {
			return out, err
		}
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	rev := expected + 1
	raw, e := json.Marshal(next)
	if e != nil {
		return out, unavailable()
	}
	if err = q.InsertCatalogueRevision(ctx, store.InsertCatalogueRevisionParams{PlanID: id, Revision: rev, Terms: raw, Archived: archive, ActorAccountID: &actor, Source: "operator", ChangedAt: stamp(now), Reason: pgtype.Text{String: reason, Valid: true}}); err != nil {
		return out, catalogueWriteError(err)
	}
	if err = q.SetCatalogueCurrent(ctx, store.SetCatalogueCurrentParams{ID: id, CurrentRevision: rev, CurrentDevices: int32(next.Devices), CurrentProfile: string(next.Profile), CurrentHidden: next.Hidden, Archived: archive}); err != nil {
		return out, catalogueWriteError(err)
	}
	out, err = operatorCatalogue(id, current.LegacyPlanID, rev, archive, raw, &actor, "operator", stamp(now), pgtype.Text{String: reason, Valid: true})
	if err != nil {
		return out, err
	}
	if err = s.saveIdempotency(ctx, q, principal, operation, key, hash, out); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	return out, nil
}

// SeedUnlimitedCatalogue is deliberately explicit; it never edits an existing active plan.
func (s *Service) SeedUnlimitedCatalogue(ctx context.Context) (wire.OperatorCataloguePlan, bool, error) {
	var out wire.OperatorCataloguePlan
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = lockCatalogue(ctx, tx); err != nil {
		return out, false, err
	}
	q := store.New(tx)
	rows, err := q.CatalogueUnlimited(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	active := 0
	var existing store.CatalogueUnlimitedRow
	for _, r := range rows {
		if !r.Archived {
			active++
			existing = r
		}
	}
	if active > 1 {
		return out, false, failure(409, "CATALOGUE_UNLIMITED_AMBIGUOUS")
	}
	if active == 1 {
		out, err = operatorCatalogue(existing.ID, existing.LegacyPlanID, existing.CurrentRevision, existing.Archived, existing.Terms, existing.ActorAccountID, existing.Source, existing.ChangedAt, existing.Reason)
		if err != nil {
			return out, false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return out, false, unavailable()
		}
		return out, false, nil
	}
	if len(rows) > 0 {
		return out, false, failure(409, "CATALOGUE_UNLIMITED_ARCHIVED")
	}
	if err = catalogueSlot(ctx, tx, 7, uuid.Nil); err != nil {
		return out, false, err
	}
	t := wire.CatalogueTerms{Devices: 7, TrafficGb: 100, Profile: "unlimited", Hidden: true, Periods: []int64{}, Prices: []wire.CataloguePrice{}}
	id := uuid.New()
	now := s.now().UTC().Truncate(time.Microsecond)
	if err = insertCatalogue(ctx, q, id, pgtype.Int8{}, t, nil, "unlimited_seed", "", now); err != nil {
		return out, false, err
	}
	raw, _ := json.Marshal(t)
	out, err = operatorCatalogue(id, pgtype.Int8{}, 1, false, raw, nil, "unlimited_seed", stamp(now), pgtype.Text{})
	if err != nil {
		return out, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}
