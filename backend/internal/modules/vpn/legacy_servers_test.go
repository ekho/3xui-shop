package vpn

import (
	"context"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
)

func TestLegacyServerImportReplayAndRollback(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	online := false
	location := "Europe"
	base := "https://sub.example.test/user/"
	server := LegacyServer{SourceID: 42, Name: "Original", Host: "https://panel.example.test", MaxClients: 90, Location: &location, Online: &online, SubscriptionURL: &base}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := ImportLegacyServersTx(ctx, tx, []LegacyServer{server})
	if err != nil || n != 1 {
		t.Fatalf("import: %d %v", n, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var id, host, savedBase string
	var savedOnline, snapshot bool
	err = e.Pool.QueryRow(ctx, `SELECT s.id,s.host,s.subscription_base_url,s.online,p.source_snapshot->>'location'='Europe' FROM vpn_servers s JOIN legacy_server_imports p ON p.server_id=s.id`).Scan(&id, &host, &savedBase, &savedOnline, &snapshot)
	if err != nil || id != "42" || host != server.Host || savedBase != base || savedOnline || !snapshot {
		t.Fatalf("identity/snapshot: %v", err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE vpn_servers SET online=true WHERE id='42'`); err != nil {
		t.Fatal(err)
	}
	tx, err = e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err = ImportLegacyServersTx(ctx, tx, []LegacyServer{server})
	if err != nil || n != 0 {
		t.Fatalf("replay: %d %v", n, err)
	}
	tx.Rollback(ctx)
	if err = e.Pool.QueryRow(ctx, `SELECT online FROM vpn_servers WHERE id='42'`).Scan(&savedOnline); err != nil || !savedOnline {
		t.Fatal("replay rewrote current state", err)
	}
	server.MaxClients++
	tx, err = e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ImportLegacyServersTx(ctx, tx, []LegacyServer{server}); err == nil {
		t.Fatal("changed source accepted")
	}
	tx.Rollback(ctx)

	second := LegacyServer{SourceID: 43, Name: "Second", Host: "https://second.example.test", MaxClients: 1, Online: &online}
	conflict := second
	conflict.SourceID = 44
	conflict.Host = server.Host
	tx, err = e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ImportLegacyServersTx(ctx, tx, []LegacyServer{second, conflict}); err == nil {
		t.Fatal("host collision accepted")
	}
	tx.Rollback(ctx)
	var count int
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM vpn_servers`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial server import: %d %v", count, err)
	}
}
