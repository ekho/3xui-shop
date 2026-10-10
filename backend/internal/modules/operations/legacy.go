package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/campaigns"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LegacyPackage struct {
	Version         int                              `json:"version"`
	Source          string                           `json:"source"`
	Users           []accounts.LegacyUser            `json:"users"`
	Servers         []vpn.LegacyServer               `json:"servers"`
	Catalogue       catalogue.LegacyCataloguePackage `json:"catalogue"`
	Approvals       accounts.LegacyApprovalPackage   `json:"approvals"`
	Payments        payments.LegacyPaymentPackage    `json:"payments"`
	Stars           []payments.LegacyStarsUser       `json:"stars"`
	Bonuses         bonuses.LegacyPackage            `json:"bonuses"`
	Campaigns       campaigns.LegacyPackage          `json:"campaigns"`
	Support         support.LegacySupportInput       `json:"support"`
	Audit           auditreports.LegacyAuditPackage  `json:"audit"`
	CatalogueSource LegacyCatalogueSource            `json:"catalogue_source"`
}
type LegacyCatalogueSource struct {
	Durations []LegacyDuration   `json:"durations"`
	Plans     []LegacyPlanSource `json:"plans"`
}
type LegacyDuration struct {
	SourceID int64 `json:"source_id"`
	Days     int64 `json:"days"`
}
type LegacyPlanSource struct {
	SourceID      int64    `json:"source_id"`
	InboundGroups []string `json:"inbound_groups"`
	PricesJSON    string   `json:"prices_json"`
}
type LegacyTotals struct {
	RewardDays                 string            `json:"reward_days"`
	RewardMoneyUnknownCurrency string            `json:"reward_money_unknown_currency"`
	CatalogueMajor             map[string]string `json:"catalogue_major"`
}
type LegacyReport struct {
	Version         int            `json:"version"`
	SourceDigest    string         `json:"source_digest"`
	DryRun          bool           `json:"dry_run"`
	Replayed        bool           `json:"replayed"`
	Counts          map[string]int `json:"counts"`
	Inserted        map[string]int `json:"inserted"`
	Unknown         map[string]int `json:"unknown"`
	PaymentStatuses map[string]int `json:"payment_statuses"`
	Totals          LegacyTotals   `json:"totals"`
}

var legacySourceName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidateLegacyPackage checks agreement between owners before opening a transaction.
func ValidateLegacyPackage(p LegacyPackage) error {
	bad := func() error { return fault(400, "IMPORT_INVALID_PACKAGE") }
	if p.Version != 1 || !legacySourceName.MatchString(p.Source) || p.Users == nil || len(p.Users) > 100000 || p.Servers == nil || p.Stars == nil || p.CatalogueSource.Durations == nil || p.CatalogueSource.Plans == nil ||
		p.Approvals.Version != 1 || p.Payments.Version != 1 || p.Catalogue.Version != 1 || p.Bonuses.Version != 1 || p.Campaigns.Source != p.Source || p.Bonuses.Source != p.Source ||
		p.Approvals.Users == nil || p.Approvals.ApprovalEvents == nil || p.Payments.Users == nil || p.Payments.Transactions == nil || p.Catalogue.Durations == nil || p.Catalogue.Plans == nil || p.Bonuses.Promocodes == nil || p.Bonuses.Referrals == nil || p.Bonuses.Rewards == nil ||
		campaigns.ValidateLegacyPackage(p.Campaigns) != nil || support.ValidateLegacySupportInput(p.Support) != nil || auditreports.ValidateLegacyAuditPackage(p.Audit) != nil {
		return bad()
	}
	users, tgs := map[int64]accounts.LegacyUser{}, map[int64]accounts.LegacyUser{}
	servers := map[int64]bool{}
	for _, s := range p.Servers {
		if s.SourceID <= 0 || servers[s.SourceID] {
			return bad()
		}
		servers[s.SourceID] = true
	}
	for _, u := range p.Users {
		if u.SourceID <= 0 || u.TgID <= 0 || users[u.SourceID].SourceID != 0 || tgs[u.TgID].SourceID != 0 || u.ServerID != nil && !servers[*u.ServerID] {
			return bad()
		}
		users[u.SourceID], tgs[u.TgID] = u, u
	}
	check := func(source, tg int64, seen map[int64]bool) bool {
		u, ok := users[source]
		if !ok || u.TgID != tg || seen[source] {
			return false
		}
		seen[source] = true
		return true
	}
	if len(p.Approvals.Users) != len(users) || len(p.Payments.Users) != len(users) || len(p.Stars) != len(users) {
		return bad()
	}
	seen := map[int64]bool{}
	for _, u := range p.Approvals.Users {
		if !check(u.SourceLegacyUserID, u.SourceTgID, seen) {
			return bad()
		}
	}
	seen = map[int64]bool{}
	for _, u := range p.Payments.Users {
		if !check(u.SourceLegacyUserID, u.SourceTgID, seen) {
			return bad()
		}
	}
	seen = map[int64]bool{}
	for _, u := range p.Stars {
		if !check(u.SourceLegacyUserID, u.SourceTgID, seen) {
			return bad()
		}
	}
	seen = map[int64]bool{}
	for _, u := range p.Campaigns.Users {
		if !check(u.SourceLegacyUserID, u.SourceTgID, seen) {
			return bad()
		}
		original := users[u.SourceLegacyUserID]
		if original.SourceInviteName == nil || *original.SourceInviteName != u.SourceInviteName || !reflect.DeepEqual(original.IsTrialUsed, u.IsTrialUsed) {
			return bad()
		}
	}
	for _, u := range users {
		if (u.SourceInviteName != nil) != seen[u.SourceID] {
			return bad()
		}
	}
	for _, v := range p.Payments.Transactions {
		if tgs[v.SourceTgID].SourceID == 0 {
			return bad()
		}
	}
	for _, v := range p.Bonuses.Promocodes {
		if v.ActivatedBy != nil && tgs[*v.ActivatedBy].SourceID == 0 {
			return bad()
		}
	}
	for _, v := range p.Bonuses.Referrals {
		if tgs[v.ReferredTgID].SourceID == 0 || tgs[v.ReferrerTgID].SourceID == 0 {
			return bad()
		}
	}
	for _, v := range p.Bonuses.Rewards {
		if tgs[v.UserTgID].SourceID == 0 {
			return bad()
		}
	}
	events := map[int64]accounts.LegacyApprovalSourceEvent{}
	for _, e := range p.Approvals.ApprovalEvents {
		if events[e.SourceID].SourceID != 0 {
			return bad()
		}
		events[e.SourceID] = e
	}
	for _, e := range p.Audit.Events {
		if e.TargetTgID == nil || tgs[*e.TargetTgID].SourceID == 0 || e.Action != "approval.approve" && e.Action != "approval.reject" {
			continue
		}
		expected := accounts.LegacyApprovalSourceEvent{SourceID: e.SourceID, TargetTgID: *e.TargetTgID, CreatedAt: e.CreatedAt, Action: e.Action, ActorType: e.ActorType, ActorID: e.ActorID, ActorName: e.ActorName, Source: e.Source}
		if !reflect.DeepEqual(events[e.SourceID], expected) {
			return bad()
		}
		delete(events, e.SourceID)
	}
	if len(events) != 0 || !validLegacyCatalogueSource(p) {
		return bad()
	}
	encoded, err := json.Marshal(p)
	if err != nil || len(encoded) > 32<<20 {
		return bad()
	}
	return nil
}

func validLegacyCatalogueSource(p LegacyPackage) bool {
	ids, days := map[int64]bool{}, map[int64]bool{}
	if len(p.CatalogueSource.Durations) != len(p.Catalogue.Durations) || len(p.CatalogueSource.Plans) != len(p.Catalogue.Plans) {
		return false
	}
	for _, v := range p.CatalogueSource.Durations {
		if v.SourceID <= 0 || v.Days <= 0 || ids[v.SourceID] || days[v.Days] {
			return false
		}
		ids[v.SourceID], days[v.Days] = true, true
	}
	for _, day := range p.Catalogue.Durations {
		if !days[day] {
			return false
		}
		delete(days, day)
	}
	plans := map[int64]catalogue.LegacyCataloguePlan{}
	for _, plan := range p.Catalogue.Plans {
		if plan.LegacyPlanID <= 0 || plans[plan.LegacyPlanID].LegacyPlanID != 0 {
			return false
		}
		plans[plan.LegacyPlanID] = plan
	}
	for _, source := range p.CatalogueSource.Plans {
		plan, ok := plans[source.SourceID]
		if !ok || len(source.InboundGroups) != 1 || source.InboundGroups[0] != plan.Profile {
			return false
		}
		var prices map[string]map[string]json.RawMessage
		if json.Unmarshal([]byte(source.PricesJSON), &prices) != nil || prices == nil || len(prices) != len(plan.Prices) {
			return false
		}
		for currency, byPeriod := range prices {
			wanted, ok := plan.Prices[currency]
			if !ok || byPeriod == nil || len(byPeriod) != len(wanted) {
				return false
			}
			for period, raw := range byPeriod {
				expected, ok := wanted[period]
				if !ok {
					return false
				}
				value := string(raw)
				if len(raw) > 0 && raw[0] == '"' {
					if json.Unmarshal(raw, &value) != nil {
						return false
					}
				}
				actual, aok := new(big.Rat).SetString(value)
				target, tok := new(big.Rat).SetString(expected)
				if !aok || !tok || actual.Cmp(target) != 0 {
					return false
				}
			}
		}
		delete(plans, source.SourceID)
	}
	return len(plans) == 0
}

func legacyReport(p LegacyPackage) (LegacyReport, error) {
	report := LegacyReport{Version: 1, Counts: map[string]int{"users": len(p.Users), "servers": len(p.Servers), "plan_durations": len(p.CatalogueSource.Durations), "plans": len(p.Catalogue.Plans), "transactions": len(p.Payments.Transactions), "stars": len(p.Stars), "promocodes": len(p.Bonuses.Promocodes), "referrals": len(p.Bonuses.Referrals), "referrer_rewards": len(p.Bonuses.Rewards), "invites": len(p.Campaigns.Campaigns), "acquisitions": len(p.Campaigns.Users), "support_tickets": len(p.Support.Tickets), "audit_log": len(p.Audit.Events), "approval_events": len(p.Approvals.ApprovalEvents)}, Inserted: map[string]int{}, Unknown: map[string]int{}, PaymentStatuses: map[string]int{}, Totals: LegacyTotals{CatalogueMajor: map[string]string{}}}
	for _, u := range p.Users {
		if u.IsTrialUsed == nil || !*u.IsTrialUsed {
			report.Unknown["trial"]++
		}
		if u.InboundGroups == nil {
			report.Unknown["inbound_groups"]++
		}
		if u.LanguageCode != "en" && u.LanguageCode != "ru" {
			report.Unknown["locale_fallback"]++
		}
	}
	report.Unknown["recurring"] = len(p.Stars)
	knownUsers := make(map[int64]bool, len(p.Users))
	for _, u := range p.Users {
		knownUsers[u.TgID] = true
	}
	for _, ticket := range p.Support.Tickets {
		if !knownUsers[ticket.TelegramID] {
			report.Unknown["support_account"]++
		}
		if ticket.ThreadID == nil {
			report.Unknown["support_topic"]++
		}
	}
	for _, v := range p.Payments.Transactions {
		report.PaymentStatuses[v.Status]++
	}
	totals := map[string]*big.Rat{"DAYS": new(big.Rat), "MONEY": new(big.Rat)}
	for _, v := range p.Bonuses.Rewards {
		value, ok := new(big.Rat).SetString(v.Amount)
		sum := totals[v.RewardType]
		if !ok || sum == nil {
			return report, fault(400, "IMPORT_INVALID_PACKAGE")
		}
		sum.Add(sum, value)
		if v.RewardType == "MONEY" {
			report.Unknown["reward_currency"]++
		}
	}
	report.Totals.RewardDays = totals["DAYS"].FloatString(0)
	report.Totals.RewardMoneyUnknownCurrency = totals["MONEY"].FloatString(18)
	prices := map[string]*big.Rat{}
	for _, plan := range p.Catalogue.Plans {
		for currency, byPeriod := range plan.Prices {
			for _, amount := range byPeriod {
				value, ok := new(big.Rat).SetString(amount)
				if !ok {
					return report, fault(400, "IMPORT_INVALID_PACKAGE")
				}
				if prices[currency] == nil {
					prices[currency] = new(big.Rat)
				}
				prices[currency].Add(prices[currency], value)
			}
		}
	}
	for currency, value := range prices {
		scale := 2
		if currency == "XTR" {
			scale = 0
		}
		report.Totals.CatalogueMajor[currency] = value.FloatString(scale)
	}
	return report, nil
}

type LegacyOwners struct {
	Accounts  *accounts.Service
	Catalogue *catalogue.Service
	Payments  *payments.Service
	Bonuses   *bonuses.Service
	Campaigns *campaigns.Service
	Support   *support.Service
	Audit     *auditreports.Service
}
type LegacyImporter struct {
	pool   *pgxpool.Pool
	owners LegacyOwners
	now    func() time.Time
}

func NewLegacyImporter(pool *pgxpool.Pool, owners LegacyOwners, now func() time.Time) *LegacyImporter {
	return &LegacyImporter{pool: pool, owners: owners, now: now}
}

func (s *LegacyImporter) Import(ctx context.Context, actor uuid.UUID, p LegacyPackage, dryRun bool) (LegacyReport, error) {
	var out LegacyReport
	if err := ValidateLegacyPackage(p); err != nil {
		return out, err
	}
	if s == nil || s.pool == nil || s.owners.Accounts == nil {
		return out, unavailable()
	}
	if s.owners.Accounts.RequireOperator(ctx, actor) != nil {
		return out, fault(403, "OPERATOR_REQUIRED")
	}
	encoded, _ := json.Marshal(p)
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return out, unavailable()
	}
	defer rollback(tx)
	if _, err = s.owners.Accounts.LockOperatorPair(ctx, tx, actor, actor); err != nil {
		return out, fault(403, "OPERATOR_REQUIRED")
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('operations.legacy-migration',0))`); err != nil {
		return out, unavailable()
	}
	var priorDigest string
	var saved []byte
	err = tx.QueryRow(ctx, `SELECT source_digest,report FROM legacy_migration_runs WHERE source=$1`, p.Source).Scan(&priorDigest, &saved)
	if err == nil {
		if priorDigest != digest {
			return out, fault(409, "IMPORT_SOURCE_CONFLICT")
		}
		if json.Unmarshal(saved, &out) != nil {
			return out, unavailable()
		}
		out.DryRun, out.Replayed = dryRun, true
		out.Inserted = map[string]int{}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	out, err = legacyReport(p)
	if err != nil {
		return out, err
	}
	out.SourceDigest = digest
	out.DryRun = dryRun
	if out.Inserted["servers"], err = vpn.ImportLegacyServersTx(ctx, tx, p.Servers); err != nil {
		return out, legacyImportError(err)
	}
	if out.Inserted["users"], err = accounts.ImportLegacyUsersTx(ctx, tx, p.Users); err != nil {
		return out, legacyImportError(err)
	}
	approval, err := s.owners.Accounts.ImportLegacyApprovalsTx(ctx, tx, p.Approvals, false)
	if err != nil {
		return out, legacyImportError(err)
	}
	out.Inserted["approvals"] = approval.Changed
	plans, err := s.owners.Catalogue.ImportLegacyCatalogueTx(ctx, tx, p.Catalogue, false)
	if err != nil {
		return out, legacyImportError(err)
	}
	out.Inserted["plans"] = plans.Created
	payment, err := s.owners.Payments.ImportLegacyPaymentsTx(ctx, tx, p.Payments, false)
	if err != nil {
		return out, legacyImportError(err)
	}
	out.Inserted["transactions"] = payment.Inserted
	if out.Inserted["stars"], err = s.owners.Payments.ImportLegacyStarsTx(ctx, tx, p.Stars); err != nil {
		return out, legacyImportError(err)
	}
	if out.Inserted["bonuses"], err = s.owners.Bonuses.ImportLegacyTx(ctx, tx, p.Bonuses); err != nil {
		return out, legacyImportError(err)
	}
	campaign, err := s.owners.Campaigns.ImportLegacyTx(ctx, tx, p.Campaigns, true)
	if err != nil {
		return out, legacyImportError(err)
	}
	out.Inserted["invites"] = campaign.InsertedCampaigns
	out.Inserted["acquisitions"] = campaign.InsertedUsers
	tickets, err := s.owners.Support.ImportLegacySupportTx(ctx, tx, p.Support, false)
	if err != nil {
		return out, legacyImportError(err)
	}
	out.Inserted["support_tickets"] = tickets.Inserted
	audit, err := s.owners.Audit.ImportLegacyTx(ctx, tx, p.Audit, false)
	if err != nil {
		return out, legacyImportError(err)
	}
	out.Inserted["audit_log"] = audit.Inserted
	stored := out
	stored.DryRun = false
	report, _ := json.Marshal(stored)
	metadata, _ := json.Marshal(p.CatalogueSource)
	if _, err = tx.Exec(ctx, `INSERT INTO legacy_migration_runs(source,source_digest,catalogue_source,report,operator_account_id,created_at) VALUES($1,$2,$3::jsonb,$4::jsonb,$5,$6)`, p.Source, digest, metadata, report, actor, s.now()); err != nil {
		return out, unavailable()
	}
	reason := "source_digest=" + digest + ";users=" + strconv.Itoa(len(p.Users)) + ";transactions=" + strconv.Itoa(len(p.Payments.Transactions))
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "legacy_migration_applied", AccountID: actor, OperatorAccountID: &actor, Reason: &reason}); err != nil {
		return out, unavailable()
	}
	if dryRun {
		if err = tx.Rollback(ctx); err != nil {
			return out, unavailable()
		}
		return out, nil
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func legacyImportError(err error) error {
	var a *accounts.Error
	var c *catalogue.Error
	var p *payments.Error
	var b *bonuses.Error
	var campaign *campaigns.Error
	var supportError *support.Error
	var audit *auditreports.Error
	var v *vpn.Error
	switch {
	case errors.As(err, &a):
		return fault(a.Status, a.Code)
	case errors.As(err, &c):
		return fault(c.Status, c.Code)
	case errors.As(err, &p):
		return fault(p.Status, p.Code)
	case errors.As(err, &b):
		return fault(b.Status, b.Code)
	case errors.As(err, &campaign):
		return fault(campaign.Status, campaign.Code)
	case errors.As(err, &supportError):
		return fault(supportError.Status, supportError.Code)
	case errors.As(err, &audit):
		return fault(audit.Status, audit.Code)
	case errors.As(err, &v):
		return fault(v.Status, v.Code)
	}
	return unavailable()
}
