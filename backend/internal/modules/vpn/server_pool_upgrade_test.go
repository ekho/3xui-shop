package vpn

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A registry write must not hide or claim the pre-upgrade configured identity.
func TestServerPoolRegisterBeforeSync(t *testing.T) {
	for _, mode := range []string{"secondary", "primary-host-conflict"} {
		t.Run(mode, func(t *testing.T) {
			e, s, cfg, a, b := poolFixture(t)
			ctx := context.Background()
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			in := ServerInput{ID: "b", Name: "Secondary", Host: b.URL, MaxClients: 2}
			if mode == "primary-host-conflict" {
				in.ID = "a"
			}
			_, err = s.RegisterServerTx(ctx, tx, in)
			if mode == "primary-host-conflict" {
				if err == nil {
					t.Fatal("first registration claimed the configured primary ID with another host")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			cfg.Panel.PanelURL = b.URL
			panel, err := s.PanelFor(ctx, "a")
			if err != nil {
				t.Fatal("secondary registration hid the pre-upgrade primary", err)
			}
			defer panel.Close()
			base, err := s.SubscriptionBase(ctx, "a")
			if err != nil || panel.cfg.PanelURL != a.URL || base != "https://primary.example.test/sub/" {
				t.Fatal("registration/config change replaced the old host or key base", err)
			}
		})
	}
}

// New-ID validation must not invalidate previously accepted configured text IDs.
func TestServerPoolLegacyIdentity(t *testing.T) {
	for _, id := range []string{"eu:1", "eu primary", strings.Repeat("界", 101)} {
		t.Run(id, func(t *testing.T) {
			e, s, cfg, a, b := poolFixture(t)
			ctx := context.Background()
			cfg.PanelID = id
			account := uuid.New()
			if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,assigned_panel_id)
 VALUES($1,'legacy-pool@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1',$4)`, account, uuid.New(), "acct_"+account.String(), id); err != nil {
				t.Fatal(err)
			}
			panel, err := s.PanelFor(ctx, id)
			if err != nil {
				t.Fatal("legacy readonly identity no longer resolves", err)
			}
			panel.Close()
			registerPoolServer(t, e, s, ServerInput{ID: "b", Name: "Secondary", Host: b.URL, MaxClients: 2})
			if err = s.SyncServers(ctx); err != nil {
				t.Fatal("legacy identity cannot seed/sync", err)
			}
			panel, err = s.PanelFor(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			defer panel.Close()
			var assigned, savedHost string
			if err = e.Pool.QueryRow(ctx, "SELECT a.assigned_panel_id,s.host FROM accounts a JOIN vpn_servers s ON s.id=a.assigned_panel_id WHERE a.id=$1", account).Scan(&assigned, &savedHost); err != nil || assigned != id || savedHost != a.URL || panel.cfg.PanelURL != a.URL {
				t.Fatal("legacy bootstrap rewrote assigned identity", err)
			}
		})
	}
}
