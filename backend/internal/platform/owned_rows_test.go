package platform

import (
	"context"

	"example.com/cabinet/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Regression assertions inspect persisted rows without exposing owner repositories.
func testTrialRow(ctx context.Context, db store.DBTX, id uuid.UUID) (store.TrialRequest, error) {
	rows, e := db.Query(ctx, "SELECT * FROM trial_requests WHERE id=$1", id)
	if e != nil {
		return store.TrialRequest{}, e
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[store.TrialRequest])
}
func testProvisionRow(ctx context.Context, db store.DBTX, id uuid.UUID) (store.TrialOperation, error) {
	rows, e := db.Query(ctx, "SELECT * FROM trial_operations WHERE id=$1", id)
	if e != nil {
		return store.TrialOperation{}, e
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[store.TrialOperation])
}
