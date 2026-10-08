package auditreports

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Money DTOs are shared report contracts; their calculations remain owner ports.
type StatisticsMoney struct {
	Currency           string `json:"currency"`
	GrossMinor         string `json:"gross_minor"`
	KnownNetMinor      string `json:"known_net_minor"`
	UnknownNetReceipts int64  `json:"unknown_net_receipts"`
}
type StatisticsRefund struct {
	Currency       string `json:"currency"`
	ReturnedAmount string `json:"returned_amount"`
}
type LegacyStatisticsMoney struct {
	Currency    string `json:"currency"`
	QuotedMinor string `json:"quoted_minor"`
}
type LegacyPaymentStatistics struct {
	CompletedTransactions int64                   `json:"completed_transactions"`
	PaidUsers             int64                   `json:"paid_users"`
	RepeatUsers           int64                   `json:"repeat_users"`
	UnknownQuoteCount     int64                   `json:"unknown_quote_count"`
	Money                 []LegacyStatisticsMoney `json:"money"`
}
type PaymentStatistics struct {
	PaidOrders  int64                   `json:"paid_orders"`
	PaidUsers   int64                   `json:"paid_users"`
	RepeatUsers int64                   `json:"repeat_users"`
	Money       []StatisticsMoney       `json:"money"`
	Refunds     []StatisticsRefund      `json:"refunds"`
	Legacy      LegacyPaymentStatistics `json:"legacy"`
}
type TrialStatistics struct {
	TrialUsers int64 `json:"trial_users"`
}
type StatisticsConversions struct {
	TrialPercent  *string `json:"trial_percent"`
	PaidPercent   *string `json:"paid_percent"`
	RepeatPercent *string `json:"repeat_percent"`
}
type StatisticsActivity struct {
	ActiveUsers        *int64    `json:"active_users"`
	KnownActiveUsers   int64     `json:"known_active_users"`
	KnownInactiveUsers int64     `json:"known_inactive_users"`
	UnknownUsers       int64     `json:"unknown_users"`
	ObservedAt         time.Time `json:"observed_at"`
}
type StatisticsGroup struct {
	Name                     string `json:"name"`
	UserReferences           int64  `json:"user_references"`
	PlanReferences           int64  `json:"plan_references"`
	InboundReferences        *int64 `json:"inbound_references"`
	EnabledInboundReferences *int64 `json:"enabled_inbound_references"`
}
type StatisticsServer struct {
	PanelID         string    `json:"panel_id"`
	Availability    string    `json:"availability"`
	Clients         *int64    `json:"clients"`
	Inbounds        *int64    `json:"inbounds"`
	EnabledInbounds *int64    `json:"enabled_inbounds"`
	ObservedAt      time.Time `json:"observed_at"`
	ErrorCode       *string   `json:"error_code"`
}
type StatisticsReport struct {
	Version             string                `json:"version"`
	CampaignID          *uuid.UUID            `json:"campaign_id"`
	DatabaseObservedAt  time.Time             `json:"database_observed_at"`
	InfrastructureScope string                `json:"infrastructure_scope"`
	Users               int64                 `json:"users"`
	Trials              TrialStatistics       `json:"trials"`
	Payments            PaymentStatistics     `json:"payments"`
	Conversions         StatisticsConversions `json:"conversions"`
	Activity            StatisticsActivity    `json:"activity"`
	Groups              []StatisticsGroup     `json:"groups"`
	UnknownUserProfiles int64                 `json:"unknown_user_profiles"`
	Servers             []StatisticsServer    `json:"servers"`
}
type StatisticsAccount struct {
	ID            uuid.UUID
	AccessProfile string
	VpnBanned     bool
}
type InboundStatistics struct{ References, Enabled int64 }
type VPNStatistics struct {
	Activity          StatisticsActivity
	Servers           []StatisticsServer
	InboundReferences map[string]InboundStatistics
}
type StatisticsPorts struct {
	RequireOperator func(context.Context, uuid.UUID) error
	AccountsTx      func(context.Context, pgx.Tx) ([]StatisticsAccount, error)
	ReportCohortTx  func(context.Context, pgx.Tx, uuid.UUID) ([]uuid.UUID, bool, error)
	PaymentsTx      func(context.Context, pgx.Tx, []uuid.UUID) (PaymentStatistics, error)
	TrialsTx        func(context.Context, pgx.Tx, []uuid.UUID) (TrialStatistics, error)
	PlansTx         func(context.Context, pgx.Tx) (map[string]int64, error)
	VPNTx           func(context.Context, pgx.Tx, []uuid.UUID) (VPNStatistics, error)
}
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return e.Code }
func unavailable() error       { return &Error{503, "SERVICE_UNAVAILABLE"} }

func percent(numerator, denominator int64) *string {
	if denominator == 0 {
		return nil
	}
	n := new(big.Int).Mul(big.NewInt(numerator), big.NewInt(10000))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, big.NewInt(denominator), r)
	if r.Lsh(r, 1).Cmp(big.NewInt(denominator)) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(q, big.NewInt(100), fraction)
	out := fmt.Sprintf("%s.%02d", whole.String(), fraction.Int64())
	return &out
}

func (s *Service) Statistics(ctx context.Context, actor uuid.UUID, campaign *uuid.UUID) (StatisticsReport, error) {
	out := StatisticsReport{Version: "statistics-v1", CampaignID: campaign, InfrastructureScope: "global"}
	if err := s.statistics.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&out.DatabaseObservedAt) != nil {
		return out, unavailable()
	}
	accounts, err := s.statistics.AccountsTx(ctx, tx)
	if err != nil {
		return out, unavailable()
	}
	var cohort map[uuid.UUID]bool
	if campaign != nil {
		ids, found, e := s.statistics.ReportCohortTx(ctx, tx, *campaign)
		if e != nil {
			return out, unavailable()
		}
		if !found {
			return out, &Error{404, "INVALID_INPUT"}
		}
		cohort = make(map[uuid.UUID]bool, len(ids))
		for _, id := range ids {
			cohort[id] = true
		}
	}
	ids := []uuid.UUID{}
	users := map[string]int64{}
	for _, a := range accounts {
		if cohort != nil && !cohort[a.ID] {
			continue
		}
		ids = append(ids, a.ID)
		if a.AccessProfile == "regular" || a.AccessProfile == "unlimited" || a.AccessProfile == "euru" {
			users[a.AccessProfile]++
		} else {
			out.UnknownUserProfiles++
		}
		if a.VpnBanned {
			users["banned"]++
		}
	}
	out.Users = int64(len(ids))
	if out.Payments, err = s.statistics.PaymentsTx(ctx, tx, ids); err != nil {
		return out, unavailable()
	}
	if out.Trials, err = s.statistics.TrialsTx(ctx, tx, ids); err != nil {
		return out, unavailable()
	}
	plans, err := s.statistics.PlansTx(ctx, tx)
	if err != nil {
		return out, unavailable()
	}
	vpn, err := s.statistics.VPNTx(ctx, tx, ids)
	if err != nil {
		return out, unavailable()
	}
	out.Activity, out.Servers = vpn.Activity, vpn.Servers
	for _, name := range []string{"banned", "regular", "unlimited", "euru"} {
		g := StatisticsGroup{Name: name, UserReferences: users[name], PlanReferences: plans[name]}
		if vpn.InboundReferences != nil {
			v := vpn.InboundReferences[name]
			g.InboundReferences, g.EnabledInboundReferences = &v.References, &v.Enabled
		}
		out.Groups = append(out.Groups, g)
	}
	out.Conversions = StatisticsConversions{percent(out.Trials.TrialUsers, out.Users), percent(out.Payments.PaidUsers, out.Users), percent(out.Payments.RepeatUsers, out.Payments.PaidUsers)}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	// Check outside the completed snapshot: a revocation during panel I/O is fresh,
	// and a one-connection pool cannot deadlock waiting on our read transaction.
	if err = s.statistics.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	return out, nil
}
