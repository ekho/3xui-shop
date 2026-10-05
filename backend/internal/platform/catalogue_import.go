package platform

import (
	"context"
	"example.com/cabinet/backend/internal/modules/catalogue"
)

type LegacyCataloguePackage = catalogue.LegacyCataloguePackage
type LegacyCataloguePlan = catalogue.LegacyCataloguePlan
type CatalogueImportResult = catalogue.CatalogueImportResult

func (s *Service) ImportLegacyCatalogue(ctx context.Context, pkg LegacyCataloguePackage, dryRun bool) (CatalogueImportResult, error) {
	out, err := s.catalogue.ImportLegacyCatalogue(ctx, pkg, dryRun)
	return out, catalogueError(err)
}
