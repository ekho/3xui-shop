package operations

import (
	"context"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
)

func TestMaintenanceDefaultAndSharedAdmission(t *testing.T) {
	env := testkit.Open(t)
	ctx := context.Background()
	first, second := New(env.Pool, nil, env.Clock), New(env.Pool, nil, env.Clock)
	status, err := first.Status(ctx)
	if err != nil || status.Enabled || status.Revision != 0 || status.ChangedAt != nil {
		t.Fatalf("default state: %+v, %v", status, err)
	}
	if err := second.AllowNew(ctx); err != nil {
		t.Fatalf("default admission: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, `UPDATE maintenance_state SET enabled=true,revision=1,changed_at=now() WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	if err := first.AllowNew(ctx); err == nil || err.Error() != "MAINTENANCE" {
		t.Fatalf("first instance admitted new work: %v", err)
	}
	if err := second.AllowNew(ctx); err == nil || err.Error() != "MAINTENANCE" {
		t.Fatalf("second instance admitted new work: %v", err)
	}
}
