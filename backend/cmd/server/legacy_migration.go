package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func decodeLegacyMigration(reader io.Reader) (operations.LegacyPackage, error) {
	var p operations.LegacyPackage
	raw, err := io.ReadAll(io.LimitReader(reader, (32<<20)+1))
	if err != nil || len(raw) > 32<<20 || !strictLegacyShape(raw, reflect.TypeOf(p)) {
		return p, errors.New("IMPORT_INVALID_PACKAGE")
	}
	p, err = decodeLegacyJSON[operations.LegacyPackage](bytes.NewReader(raw))
	if err != nil || operations.ValidateLegacyPackage(p) != nil {
		return p, errors.New("IMPORT_INVALID_PACKAGE")
	}
	for _, plan := range p.CatalogueSource.Plans {
		if !validJSONUnicode([]byte(plan.PricesJSON)) || !validLegacyTokens([]byte(plan.PricesJSON), false) {
			return p, errors.New("IMPORT_INVALID_PACKAGE")
		}
	}
	for _, event := range p.Audit.Events {
		if event.PayloadJSON != nil && (!validJSONUnicode([]byte(*event.PayloadJSON)) || !validLegacyTokens([]byte(*event.PayloadJSON), false)) {
			return p, errors.New("IMPORT_INVALID_PACKAGE")
		}
	}
	return p, nil
}

// Every source field is explicit, including NULL; custom UnmarshalJSON cannot hide extra keys.
func strictLegacyShape(raw []byte, t reflect.Type) bool {
	raw = bytes.TrimSpace(raw)
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(raw, []byte("null")) {
			return true
		}
		return strictLegacyShape(raw, t.Elem())
	}
	if bytes.Equal(raw, []byte("null")) {
		return false
	}
	if t == reflect.TypeOf(time.Time{}) {
		var stamp string
		return json.Unmarshal(raw, &stamp) == nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) != t.NumField() {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			value, ok := fields[name]
			if !ok || !strictLegacyShape(value, field.Type) {
				return false
			}
		}
	case reflect.Slice:
		var entries []json.RawMessage
		if json.Unmarshal(raw, &entries) != nil || entries == nil {
			return false
		}
		for _, value := range entries {
			if !strictLegacyShape(value, t.Elem()) {
				return false
			}
		}
	case reflect.Map:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return false
		}
		for _, value := range fields {
			if !strictLegacyShape(value, t.Elem()) {
				return false
			}
		}
	}
	return true
}

func runLegacyMigration(args []string) error {
	if len(args) != 3 || args[0] != "--dry-run" && args[0] != "--apply" || args[1] != "--operator-file" {
		return importError("INVALID_IMPORT_COMMAND")
	}
	actorText, err := operations.ReadPrivateText(args[2])
	if err != nil {
		return importError("INVALID_OPERATOR_FILE")
	}
	actor, err := uuid.Parse(actorText)
	if err != nil || actor == uuid.Nil || actor.String() != actorText {
		return importError("INVALID_OPERATOR_FILE")
	}
	if os.Getenv("DATABASE_URL") != "" {
		return importError("INVALID_PRIVATE_FILE")
	}
	databaseURL, err := operations.ReadPrivateText(os.Getenv("DATABASE_URL_FILE"))
	if err != nil {
		return importError("INVALID_PRIVATE_FILE")
	}
	if operations.ValidateConnectionURL(databaseURL) != nil {
		return importError("INVALID_DATABASE_URL")
	}
	p, err := decodeLegacyMigration(os.Stdin)
	if err != nil {
		return importError("IMPORT_INVALID_PACKAGE")
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	defer pool.Close()
	out, err := app.NewModules(pool, nil, nil, &app.Config{}).LegacyImport.Import(ctx, actor, p, args[0] == "--dry-run")
	if err != nil {
		var domain *operations.Error
		if errors.As(err, &domain) {
			return importError(domain.Code)
		}
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}
