package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"os"
	"time"
	"unicode/utf8"
)

func decodeLegacyApprovalPackage(reader io.Reader) (platform.LegacyApprovalPackage, error) {
	var out platform.LegacyApprovalPackage
	const limit = 32 << 20
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || len(data) > limit || !utf8.Valid(data) {
		return out, errors.New("IMPORT_INVALID_PACKAGE")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF || out.Version != 1 || out.Users == nil || out.ApprovalEvents == nil {
		return out, errors.New("IMPORT_INVALID_PACKAGE")
	}
	return out, nil
}

func runLegacyApprovalImport(flag string) error {
	if flag != "--dry-run" && flag != "--apply" {
		return errors.New("invalid import command")
	}
	packageData, err := decodeLegacyApprovalPackage(os.Stdin)
	if err != nil {
		return importError("IMPORT_INVALID_PACKAGE")
	}
	databaseURL, err := platform.SecretFile("DATABASE_URL")
	if err != nil {
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	defer pool.Close()
	result, err := platform.NewService(pool, nil, nil, platform.Config{}).ImportLegacyApprovals(ctx, packageData, flag == "--dry-run")
	if err != nil {
		var domain *platform.Error
		if errors.As(err, &domain) {
			return importError(domain.Code)
		}
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func importError(code string) error {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"error": code})
	return errors.New(code)
}
