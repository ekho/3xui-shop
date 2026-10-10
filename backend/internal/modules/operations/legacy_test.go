package operations

import (
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/campaigns"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/vpn"
)

func emptyLegacyPackage() LegacyPackage {
	return LegacyPackage{Version: 1, Source: "synthetic-source", Users: []accounts.LegacyUser{}, Servers: []vpn.LegacyServer{},
		Catalogue: catalogue.LegacyCataloguePackage{Version: 1, Durations: []int64{}, Plans: []catalogue.LegacyCataloguePlan{}},
		Approvals: accounts.LegacyApprovalPackage{Version: 1, Users: []accounts.LegacyApprovalUser{}, ApprovalEvents: []accounts.LegacyApprovalSourceEvent{}},
		Payments:  payments.LegacyPaymentPackage{Version: 1, Users: []payments.LegacyPaymentUser{}, Transactions: []payments.LegacyPaymentTransaction{}}, Stars: []payments.LegacyStarsUser{},
		Bonuses:   bonuses.LegacyPackage{Version: 1, Source: "synthetic-source", Promocodes: []bonuses.LegacyPromocode{}, Referrals: []bonuses.LegacyReferral{}, Rewards: []bonuses.LegacyReward{}},
		Campaigns: campaigns.LegacyPackage{Version: 1, Source: "synthetic-source", Campaigns: []campaigns.LegacyCampaign{}, Users: []campaigns.LegacyUser{}},
		Support:   support.LegacySupportInput{Version: 1, BotID: 1, GroupID: -1, Tickets: []support.LegacySupportRow{}}, Audit: auditreports.LegacyAuditPackage{Version: 1, Events: []auditreports.LegacyAuditInput{}},
		CatalogueSource: LegacyCatalogueSource{Durations: []LegacyDuration{}, Plans: []LegacyPlanSource{}}}
}

func TestLegacyPackageRequiresCompleteConsistentSource(t *testing.T) {
	if ValidateLegacyPackage(emptyLegacyPackage()) != nil {
		t.Fatal("complete empty source rejected")
	}
	for _, change := range []func(*LegacyPackage){
		func(p *LegacyPackage) { p.Users = nil },
		func(p *LegacyPackage) { p.Stars = nil },
		func(p *LegacyPackage) { p.Campaigns.Source = "different" },
		func(p *LegacyPackage) {
			p.Payments.Users = append(p.Payments.Users, payments.LegacyPaymentUser{SourceLegacyUserID: 1, SourceTgID: 1})
		},
		func(p *LegacyPackage) { p.CatalogueSource.Durations = []LegacyDuration{{SourceID: 1, Days: 30}} },
	} {
		p := emptyLegacyPackage()
		change(&p)
		if ValidateLegacyPackage(p) == nil {
			t.Fatal("partial or inconsistent source accepted")
		}
	}
}

func TestLegacyTotalsKeepUnknownCurrencySeparate(t *testing.T) {
	p := emptyLegacyPackage()
	p.Bonuses.Rewards = []bonuses.LegacyReward{{RewardType: "DAYS", Amount: "10"}, {RewardType: "MONEY", Amount: "99999999999999999999.123456789012345678"}, {RewardType: "MONEY", Amount: "0.000000000000000001"}}
	report, err := legacyReport(p)
	if err != nil || report.Totals.RewardDays != "10" || report.Totals.RewardMoneyUnknownCurrency != "99999999999999999999.123456789012345679" {
		t.Fatal("exact units or numeric precision lost")
	}
}
