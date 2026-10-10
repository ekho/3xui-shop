package subscriptions

import (
	"context"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions/internal/store"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestImportedTrialUnknownAndUsedStayBlocked(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	service := New(e.Pool, nil, nil, nil, nil, func() Config { return Config{TrialEnabled: true} }, e.Clock)
	for i, used := range []bool{false, true} {
		id := uuid.New()
		tg := int64(77000 + i)
		if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,legacy_user_id,display_name,locale,vpn_id,sub_id,panel_key) VALUES($1,'telegram',$2,$2,'Legacy','ru',$3,$4,$5)`, id, tg, uuid.New(), "abcdefghijklmnop"[:15]+string(rune('a'+i)), "legacy-trial-"+id.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Pool.Exec(ctx, `INSERT INTO legacy_account_imports(source_id,account_id,source_tg_id,source_snapshot) VALUES($1,$2,$1,$3::jsonb)`, tg, id, map[bool]string{false: `{"is_trial_used":false}`, true: `{"is_trial_used":true}`}[used]); err != nil {
			t.Fatal(err)
		}
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		name := "Legacy"
		a := accounts.Snapshot{ID: id, Kind: "telegram", TelegramID: &tg, DisplayName: &name}
		if err = service.trialEligibility(ctx, tx, store.New(tx), a); err == nil || err.Error() != "TRIAL_ALREADY_USED" {
			t.Fatalf("source used=%v: %v", used, err)
		}
		tx.Rollback(ctx)
	}
}
