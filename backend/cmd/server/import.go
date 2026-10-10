package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/campaigns"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/support"
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
	if err != nil || len(data) > limit || !utf8.Valid(data) || !validJSONUnicode(data) || !validLegacyTokens(data, true) {
		return out, errors.New("IMPORT_INVALID_PACKAGE")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF {
		return out, errors.New("IMPORT_INVALID_PACKAGE")
	}
	return out, nil
}

// Reject duplicate keys and timestamp digits before Go's time decoder can truncate them.
func validLegacyTokens(data []byte, checkTimestamps bool) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value func(string) bool
	value = func(key string) bool {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				keys := map[string]bool{}
				for decoder.More() {
					name, err := decoder.Token()
					field, ok := name.(string)
					if err != nil || !ok || keys[field] {
						return false
					}
					keys[field] = true
					if !value(field) {
						return false
					}
				}
				end, err := decoder.Token()
				return err == nil && end == json.Delim('}')
			case '[':
				for decoder.More() {
					if !value("") {
						return false
					}
				}
				end, err := decoder.Token()
				return err == nil && end == json.Delim(']')
			}
			return false
		}
		if !checkTimestamps {
			return true
		}
		switch key {
		case "created_at", "updated_at", "requested_at", "decided_at", "referred_rewarded_at", "rewarded_at":
			if token == nil {
				return true
			}
			stamp, ok := token.(string)
			if !ok {
				return false
			}
			_, err := auditreports.ParseTimestamp(stamp)
			return err == nil
		}
		return true
	}
	if !value("") {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
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

func runLegacySupportImport(flag string) error {
	if flag != "--dry-run" && flag != "--apply" {
		return errors.New("invalid import command")
	}
	p, err := decodeLegacyJSON[support.LegacySupportInput](os.Stdin)
	if err != nil || support.ValidateLegacySupportInput(p) != nil {
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
	result, err := app.NewModules(pool, nil, nil, &app.Config{}).Support.ImportLegacySupport(ctx, p, flag == "--dry-run")
	if err != nil {
		var domain *support.Error
		if errors.As(err, &domain) {
			return importError(domain.Code)
		}
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func decodeLegacyAuditPackage(reader io.Reader) (auditreports.LegacyAuditPackage, error) {
	p, err := decodeLegacyJSON[auditreports.LegacyAuditPackage](reader)
	if err != nil || auditreports.ValidateLegacyAuditPackage(p) != nil {
		return p, errors.New("IMPORT_INVALID_PACKAGE")
	}
	for _, event := range p.Events {
		if event.PayloadJSON != nil && !validJSONUnicode([]byte(*event.PayloadJSON)) {
			return p, errors.New("IMPORT_INVALID_PACKAGE")
		}
	}
	return p, nil
}

func runLegacyAuditImport(flag string) error {
	if flag != "--dry-run" && flag != "--apply" {
		return errors.New("invalid import command")
	}
	p, err := decodeLegacyAuditPackage(os.Stdin)
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
	out, err := app.NewModules(pool, nil, nil, &app.Config{}).AuditReports.ImportLegacy(ctx, p, flag == "--dry-run")
	if err != nil {
		var domain *auditreports.Error
		if errors.As(err, &domain) {
			return importError(domain.Code)
		}
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func runLegacyCampaignImport(flag string) error {
	if flag != "--dry-run" && flag != "--apply" {
		return errors.New("invalid import command")
	}
	p, err := decodeLegacyJSON[campaigns.LegacyPackage](os.Stdin)
	if err != nil || campaigns.ValidateLegacyPackage(p) != nil {
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
	out, err := app.NewModules(pool, nil, nil, &app.Config{}).Campaigns.ImportLegacy(ctx, p, flag == "--apply")
	if err != nil {
		var e *campaigns.Error
		if errors.As(err, &e) {
			return importError(e.Code)
		}
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}
