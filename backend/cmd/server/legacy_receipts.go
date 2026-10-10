package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runLegacyReceipts(flag, path string) error {
	bad := errors.New("LEGACY_RECEIPTS_UNAVAILABLE")
	if flag != "--operator-file" || os.Getenv("DATABASE_URL") != "" {
		return bad
	}
	text, err := operations.ReadPrivateText(path)
	if err != nil {
		return bad
	}
	actor, err := uuid.Parse(text)
	if err != nil || actor == uuid.Nil || actor.String() != text {
		return bad
	}
	databaseURL, err := operations.ReadPrivateText(os.Getenv("DATABASE_URL_FILE"))
	if err != nil || operations.ValidateConnectionURL(databaseURL) != nil {
		return bad
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return bad
	}
	defer pool.Close()
	rows, err := app.NewModules(pool, nil, nil, &app.Config{}).Payments.ListLegacyReceipts(ctx, actor)
	if err != nil {
		return bad
	}
	return json.NewEncoder(os.Stdout).Encode(rows)
}
