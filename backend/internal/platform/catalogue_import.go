package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"strconv"
	"strings"
	"time"
)

// LegacyCataloguePackage is a controlled, versioned package generated outside the server.
// Prices are decimal major-unit strings, never JSON numbers.
type LegacyCataloguePackage struct {
	Version   int                   `json:"version"`
	Durations []int64               `json:"durations"`
	Plans     []LegacyCataloguePlan `json:"plans"`
}
type LegacyCataloguePlan struct {
	LegacyPlanID int64                        `json:"legacy_plan_id"`
	Devices      int                          `json:"devices"`
	TrafficGB    int                          `json:"traffic_gb"`
	Profile      string                       `json:"profile"`
	Hidden       bool                         `json:"hidden"`
	Prices       map[string]map[string]string `json:"prices"`
}

func (p *LegacyCataloguePlan) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != 6 {
		return errors.New("IMPORT_INVALID_PACKAGE")
	}
	for _, name := range []string{"legacy_plan_id", "devices", "traffic_gb", "profile", "hidden", "prices"} {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("IMPORT_INVALID_PACKAGE")
		}
	}
	type plain LegacyCataloguePlan
	return json.Unmarshal(data, (*plain)(p))
}

type CatalogueImportResult struct {
	Created  int  `json:"created"`
	Replayed int  `json:"replayed"`
	DryRun   bool `json:"dry_run"`
}

func legacyMinor(major string, scale int) (string, error) {
	if major == "" || strings.HasPrefix(major, "+") || strings.HasPrefix(major, "-") || strings.ContainsAny(major, "eE \t\r\n") {
		return "", failure(400, "IMPORT_INVALID_PACKAGE")
	}
	parts := strings.Split(major, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 19 || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return "", failure(400, "IMPORT_INVALID_PACKAGE")
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return "", failure(400, "IMPORT_INVALID_PACKAGE")
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > scale {
			return "", failure(400, "IMPORT_INVALID_PACKAGE")
		}
		for _, c := range fraction {
			if c < '0' || c > '9' {
				return "", failure(400, "IMPORT_INVALID_PACKAGE")
			}
		}
	}
	if scale == 0 && len(parts) == 2 {
		return "", failure(400, "IMPORT_INVALID_PACKAGE")
	}
	minor := strings.TrimLeft(parts[0]+fraction+strings.Repeat("0", scale-len(fraction)), "0")
	if minor == "" {
		minor = "0"
	}
	if _, err := strconv.ParseInt(minor, 10, 64); err != nil {
		return "", failure(400, "IMPORT_INVALID_PACKAGE")
	}
	return minor, nil
}

func legacyTerms(p LegacyCataloguePlan, durations []int64) (wire.CatalogueTerms, error) {
	t := wire.CatalogueTerms{Devices: p.Devices, TrafficGb: p.TrafficGB, Profile: wire.CatalogueTermsProfile(p.Profile), Hidden: p.Hidden, Periods: append([]int64{}, durations...), Prices: []wire.CataloguePrice{}}
	if p.Prices == nil {
		return t, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	for currency, byPeriod := range p.Prices {
		if byPeriod == nil {
			return t, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		scale := 2
		if currency == "XTR" {
			scale = 0
		} else if currency != "RUB" && currency != "USD" {
			return t, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		for period, major := range byPeriod {
			d, err := strconv.ParseInt(period, 10, 64)
			if err != nil || strconv.FormatInt(d, 10) != period {
				return t, failure(400, "IMPORT_INVALID_PACKAGE")
			}
			minor, err := legacyMinor(major, scale)
			if err != nil {
				return t, err
			}
			t.Prices = append(t.Prices, wire.CataloguePrice{PeriodDays: d, Currency: wire.CataloguePriceCurrency(currency), AmountMinor: minor})
		}
	}
	if err := validateCatalogueTerms(t); err != nil {
		return t, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	return canonicalTerms(t), nil
}

func (s *Service) ImportLegacyCatalogue(ctx context.Context, pkg LegacyCataloguePackage, dryRun bool) (CatalogueImportResult, error) {
	out := CatalogueImportResult{DryRun: dryRun}
	if pkg.Version != 1 || pkg.Durations == nil || pkg.Plans == nil || len(pkg.Plans) > 10000 {
		return out, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	if len(pkg.Durations) > 100 {
		return out, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	durationSeen := map[int64]bool{}
	for _, days := range pkg.Durations {
		if days < 1 || days > 106751 || durationSeen[days] {
			return out, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		durationSeen[days] = true
	}
	seenIDs := map[int64]bool{}
	seenDevices := map[int]bool{}
	type candidate struct {
		id    int64
		terms wire.CatalogueTerms
		raw   []byte
	}
	items := make([]candidate, 0, len(pkg.Plans))
	for _, p := range pkg.Plans {
		if p.LegacyPlanID <= 0 || seenIDs[p.LegacyPlanID] || seenDevices[p.Devices] {
			return out, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		seenIDs[p.LegacyPlanID] = true
		seenDevices[p.Devices] = true
		terms, err := legacyTerms(p, pkg.Durations)
		if err != nil {
			return out, err
		}
		raw, _ := json.Marshal(terms)
		items = append(items, candidate{p.LegacyPlanID, terms, raw})
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = lockCatalogue(ctx, tx); err != nil {
		return out, err
	}
	q := store.New(tx)
	for _, item := range items {
		legacy := pgtype.Int8{Int64: item.id, Valid: true}
		found, e := q.CatalogueByLegacyID(ctx, legacy)
		if e == nil {
			var original []byte
			var source string
			if e = tx.QueryRow(ctx, "SELECT terms,source FROM catalogue_revisions WHERE plan_id=$1 AND revision=1", found.ID).Scan(&original, &source); e != nil {
				return out, unavailable()
			}
			var stored wire.CatalogueTerms
			if json.Unmarshal(original, &stored) != nil {
				return out, unavailable()
			}
			storedRaw, _ := json.Marshal(stored)
			if source != "legacy_import" || !bytes.Equal(storedRaw, item.raw) {
				return out, failure(409, "IMPORT_CONFLICT")
			}
			out.Replayed++
			continue
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return out, unavailable()
		}
		if e = catalogueSlot(ctx, tx, item.terms.Devices, uuid.Nil); e != nil {
			return out, e
		}
		if e = insertCatalogue(ctx, q, uuid.New(), legacy, item.terms, nil, "legacy_import", "", s.now().UTC().Truncate(time.Microsecond)); e != nil {
			return out, e
		}
		out.Created++
	}
	if dryRun {
		return out, nil
	}
	if err = tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	return out, nil
}
