package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime/debug"
	"time"

	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/modules/operations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Docker's backend-only build context has no Git metadata; CI supplies its checkout SHA.
var sourceRevision string

type cutoverArtifact struct {
	SourceRevision        string          `json:"source_revision"`
	LegacyReceiptsVersion int             `json:"legacy_receipts_version"`
	Schema                db.BackupSchema `json:"schema"`
}

func artifactSourceRevision() string {
	value := sourceRevision
	if value == "" {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			return ""
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				return ""
			}
			if setting.Key == "vcs.revision" {
				value = setting.Value
			}
		}
	}
	if decoded, err := hex.DecodeString(value); err != nil || len(decoded) != 20 {
		return ""
	}
	return value
}

// This preflight never migrates or starts a writer; compare it with the current artifact before stopping serve.
func runCutoverCheck(output io.Writer) error {
	bad := errors.New("CUTOVER_UNSUPPORTED_ARTIFACT")
	revision := artifactSourceRevision()
	if revision == "" || os.Getenv("DATABASE_URL") != "" {
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
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return bad
	}
	defer tx.Rollback(ctx)
	schema, err := db.BackupSchemaState(ctx, tx)
	if err != nil || schema.Version != 44 {
		return bad
	}
	if err = tx.Commit(ctx); err != nil {
		return bad
	}
	return json.NewEncoder(output).Encode(cutoverArtifact{revision, 1, schema})
}
