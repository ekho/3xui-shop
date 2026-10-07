package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/payments"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"os"
	"strconv"
	"time"
	"unicode/utf8"
)

func decodeLegacyPaymentPackage(reader io.Reader) (payments.LegacyPaymentPackage, error) {
	out, err := decodeLegacyJSON[payments.LegacyPaymentPackage](reader)
	if err != nil || out.Version != 1 || out.Users == nil || out.Transactions == nil {
		return out, errors.New("IMPORT_INVALID_PACKAGE")
	}
	return out, nil
}

func decodeLegacyApprovalPackage(reader io.Reader) (accounts.LegacyApprovalPackage, error) {
	out, err := decodeLegacyJSON[accounts.LegacyApprovalPackage](reader)
	if err != nil || out.Version != 1 || out.Users == nil || out.ApprovalEvents == nil {
		return out, errors.New("IMPORT_INVALID_PACKAGE")
	}
	return out, nil
}

func decodeLegacyJSON[T any](reader io.Reader) (T, error) {
	var out T
	const limit = 32 << 20
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || len(data) > limit || !utf8.Valid(data) || !validJSONUnicode(data) {
		return out, errors.New("IMPORT_INVALID_PACKAGE")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF {
		return out, errors.New("IMPORT_INVALID_PACKAGE")
	}
	return out, nil
}

// encoding/json replaces unpaired UTF-16 escapes. Raw financial IDs must never
// be silently repaired; valid pairs and escaped literal backslashes are kept.
func validJSONUnicode(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) || data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		unit, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func runLegacyPaymentImport(flag string) error {
	if flag != "--dry-run" && flag != "--apply" {
		return errors.New("invalid import command")
	}
	packageData, err := decodeLegacyPaymentPackage(os.Stdin)
	if err != nil {
		return importError("IMPORT_INVALID_PACKAGE")
	}
	databaseURL, err := app.SecretFile("DATABASE_URL")
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
	result, err := app.NewModules(pool, nil, nil, &app.Config{}).Payments.ImportLegacyPayments(ctx, packageData, flag == "--dry-run")
	if err != nil {
		var domain *payments.Error
		if errors.As(err, &domain) {
			return importError(domain.Code)
		}
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func runLegacyApprovalImport(flag string) error {
	if flag != "--dry-run" && flag != "--apply" {
		return errors.New("invalid import command")
	}
	packageData, err := decodeLegacyApprovalPackage(os.Stdin)
	if err != nil {
		return importError("IMPORT_INVALID_PACKAGE")
	}
	databaseURL, err := app.SecretFile("DATABASE_URL")
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
	result, err := app.NewModules(pool, nil, nil, &app.Config{}).Accounts.ImportLegacyApprovals(ctx, packageData, flag == "--dry-run")
	if err != nil {
		var domain *accounts.Error
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
