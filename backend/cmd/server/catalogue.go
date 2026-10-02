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

func decodeLegacyCatalogue(reader io.Reader) (platform.LegacyCataloguePackage, error) {
	var pkg platform.LegacyCataloguePackage
	const limit = 32 << 20
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || len(data) > limit || !utf8.Valid(data) {
		return pkg, errors.New("IMPORT_INVALID_PACKAGE")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&pkg) != nil || d.Decode(new(any)) != io.EOF || pkg.Version != 1 || pkg.Durations == nil || pkg.Plans == nil {
		return pkg, errors.New("IMPORT_INVALID_PACKAGE")
	}
	return pkg, nil
}

func runCatalogueCommand(args []string) error {
	if (len(args) != 1 || args[0] != "seed-unlimited") && (len(args) != 2 || args[0] != "import-legacy" || (args[1] != "--dry-run" && args[1] != "--apply")) {
		return errors.New("invalid catalogue command")
	}
	var pkg platform.LegacyCataloguePackage
	if args[0] == "import-legacy" {
		var err error
		pkg, err = decodeLegacyCatalogue(os.Stdin)
		if err != nil {
			return importError("IMPORT_INVALID_PACKAGE")
		}
	}
	url, err := platform.SecretFile("DATABASE_URL")
	if err != nil {
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return importError("IMPORT_DATABASE_UNAVAILABLE")
	}
	defer pool.Close()
	svc := platform.NewService(pool, nil, nil, platform.Config{})
	if args[0] == "seed-unlimited" {
		plan, created, e := svc.SeedUnlimitedCatalogue(ctx)
		if e != nil {
			return catalogueCLIError(e)
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"plan_id": plan.PlanId, "revision": plan.Revision, "created": created})
	}
	result, e := svc.ImportLegacyCatalogue(ctx, pkg, args[1] == "--dry-run")
	if e != nil {
		return catalogueCLIError(e)
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func catalogueCLIError(err error) error {
	var domain *platform.Error
	if errors.As(err, &domain) {
		return importError(domain.Code)
	}
	return importError("IMPORT_DATABASE_UNAVAILABLE")
}
