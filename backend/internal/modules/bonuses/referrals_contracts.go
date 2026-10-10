package bonuses

const ReferralsVersion = "2026-10-10-referrals-v1"

type ReferralLevel struct {
	Level               int32  `json:"level"`
	Invited             int64  `json:"invited"`
	GrantedDays         string `json:"granted_days"`
	PendingDays         string `json:"pending_days"`
	GrantedRewards      int64  `json:"granted_rewards"`
	PendingRewards      int64  `json:"pending_rewards"`
	MoneyRecords        int64  `json:"money_records"`
	PendingMoneyRecords int64  `json:"pending_money_records"`
}

type ReferralsResult struct {
	Version             string          `json:"version"`
	WebURL              string          `json:"web_url"`
	Levels              []ReferralLevel `json:"levels"`
	UnclassifiedRecords int64           `json:"unclassified_records"`
}
