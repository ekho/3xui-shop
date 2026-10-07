package httpapi

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Persisted test rows are independent of module-private generated models.
type trialRow struct {
	ID                                                uuid.UUID
	Sequence                                          pgtype.Int8
	AccountID                                         uuid.UUID
	Status, Comment, DecisionSource                   string
	CreatedAt, DecidedAt                              pgtype.Timestamptz
	OperatorTgID                                      pgtype.Int8
	Reason                                            pgtype.Text
	OperationID, PreviousRequestID, OperatorAccountID *uuid.UUID
}
type provisionRow struct {
	ID, AccountID, RequestID         uuid.UUID
	Status                           string
	TrialEnabled                     bool
	PeriodDays, TrafficGb, Devices   int64
	PanelID                          string
	CreatedAt, FirstStartedAt        pgtype.Timestamptz
	Target                           []byte
	WriteStarted                     bool
	Attempts                         int32
	LeaseHash                        []byte
	LeaseExpiresAt                   pgtype.Timestamptz
	WorkerPid                        pgtype.Int4
	TrafficUsedBytes                 pgtype.Int8
	ObservedAt                       pgtype.Timestamptz
	TrafficUpBytes, TrafficDownBytes pgtype.Int8
	ProfileSnapshot                  []byte
}

func testTrialRow(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, id uuid.UUID) (trialRow, error) {
	rows, err := db.Query(ctx, "SELECT * FROM trial_requests WHERE id=$1", id)
	if err != nil {
		return trialRow{}, err
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[trialRow])
}
func testProvisionRow(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, id uuid.UUID) (provisionRow, error) {
	rows, err := db.Query(ctx, "SELECT * FROM trial_operations WHERE id=$1", id)
	if err != nil {
		return provisionRow{}, err
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[provisionRow])
}
