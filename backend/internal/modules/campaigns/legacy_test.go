package campaigns

import (
	"context"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestLegacyUnknownTrialSourcePreserved(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	for _, source := range []struct {
		legacy, telegram int64
	}{
		{5, 701}, {6, 702},
	} {
		id := uuid.New()
		if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id,legacy_user_id) VALUES($1,$2,'ru','fixture',now(),$3,$4,$5,'1','1',$6,$7)`, id, id.String()+"@example.test", uuid.New(), strings.ReplaceAll(id.String(), "-", "")[:16], "acct_"+id.String(), source.telegram, source.legacy); err != nil {
			t.Fatal(err)
		}
	}
	authority := accounts.New(e.Pool, e.Redis, nil, accounts.Config{Now: e.Clock})
	s := New(e.Pool, e.Redis, authority, nil, nil, "fixture", e.Clock)
	clicks, active, unused := int64(0), true, false
	p := LegacyPackage{Version: 1, Source: "fixture", Campaigns: []LegacyCampaign{{SourceID: 11, Name: "Invite", HashCode: "invite_A", Clicks: &clicks, IsActive: &active}}, Users: []LegacyUser{{SourceLegacyUserID: 5, SourceTgID: 701, SourceInviteName: "Invite"}, {SourceLegacyUserID: 6, SourceTgID: 702, SourceInviteName: "Invite", IsTrialUsed: &unused}}}
	if err := ValidateLegacyPackage(p); err != nil {
		t.Fatal("nullable source rejected", err)
	}
	if out, err := s.ImportLegacy(ctx, p, true); err != nil || out.InsertedUsers != 2 {
		t.Fatal("source import", out, err)
	}
	var unknown, explicitFalse, rawNull, grants bool
	if err := e.Pool.QueryRow(ctx, `SELECT
  (SELECT legacy_trial_used IS NULL FROM campaign_acquisitions WHERE legacy_payload->>'source_tg_id'='701'),
  (SELECT legacy_trial_used=false FROM campaign_acquisitions WHERE legacy_payload->>'source_tg_id'='702'),
  (SELECT legacy_payload->'is_trial_used'='null'::jsonb FROM campaign_acquisitions WHERE legacy_payload->>'source_tg_id'='701'),
  EXISTS(SELECT 1 FROM trial_grants)`).Scan(&unknown, &explicitFalse, &rawNull, &grants); err != nil || !unknown || !explicitFalse || !rawNull || grants {
		t.Fatal("NULL source became an unused grant or lost provenance", err)
	}
	if out, err := s.ImportLegacy(ctx, p, true); err != nil || out.InsertedUsers != 0 {
		t.Fatal("nullable source replay", out, err)
	}
	p.Users[0].IsTrialUsed = &unused
	if _, err := s.ImportLegacy(ctx, p, true); err == nil || err.Error() != "IMPORT_SOURCE_CONFLICT" {
		t.Fatal("NULL-to-false source change accepted", err)
	}
}
