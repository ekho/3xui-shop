package httpapi

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func catalogueError(err error) error {
	var domain *catalogue.Error
	if errors.As(err, &domain) {
		return failure(domain.Status, domain.Code)
	}
	return accountError(err)
}

func catalogueTermsInput(t wire.CatalogueTerms) catalogue.Terms {
	var prices []catalogue.Price
	if t.Prices != nil {
		prices = make([]catalogue.Price, len(t.Prices))
		for i, p := range t.Prices {
			prices[i] = catalogue.Price{AmountMinor: p.AmountMinor, Currency: string(p.Currency), PeriodDays: p.PeriodDays}
		}
	}
	return catalogue.Terms{Devices: t.Devices, Hidden: t.Hidden, Periods: t.Periods, Prices: prices, Profile: string(t.Profile), TrafficGb: t.TrafficGb}
}

func cataloguePrices(prices []catalogue.Price) []wire.CataloguePrice {
	if prices == nil {
		return nil
	}
	out := make([]wire.CataloguePrice, len(prices))
	for i, p := range prices {
		out[i] = wire.CataloguePrice{AmountMinor: p.AmountMinor, Currency: wire.CataloguePriceCurrency(p.Currency), PeriodDays: p.PeriodDays}
	}
	return out
}

func catalogueOperatorPlan(p catalogue.OperatorPlan) wire.OperatorCataloguePlan {
	return wire.OperatorCataloguePlan{ActorAccountId: p.ActorAccountId, Archived: p.Archived, ChangedAt: p.ChangedAt, Devices: p.Devices, Hidden: p.Hidden, LegacyPlanId: p.LegacyPlanId, Periods: p.Periods, PlanId: p.PlanId, Prices: cataloguePrices(p.Prices), Profile: wire.OperatorCataloguePlanProfile(p.Profile), Reason: p.Reason, Revision: p.Revision, Source: wire.OperatorCataloguePlanSource(p.Source), TrafficGb: p.TrafficGb}
}

func (a *API) catalogue(ctx context.Context, actor uuid.UUID) (wire.CatalogueResult, error) {
	result, err := a.catalogueOwner.Catalogue(ctx, actor)
	out := wire.CatalogueResult{Plans: make([]wire.CataloguePlanSnapshot, len(result.Plans))}
	for i, p := range result.Plans {
		out.Plans[i] = wire.CataloguePlanSnapshot{Devices: p.Devices, Hidden: p.Hidden, Periods: p.Periods, PlanId: p.PlanId, Prices: cataloguePrices(p.Prices), Profile: wire.CataloguePlanSnapshotProfile(p.Profile), Revision: p.Revision, TrafficGb: p.TrafficGb}
	}
	return out, catalogueError(err)
}

func (a *API) operatorCatalogue(ctx context.Context, actor uuid.UUID, page, perPage int) (wire.OperatorCatalogueResult, error) {
	result, err := a.catalogueOwner.OperatorCatalogue(ctx, actor, page, perPage)
	out := wire.OperatorCatalogueResult{Page: result.Page, PerPage: result.PerPage, Total: result.Total, Plans: make([]wire.OperatorCataloguePlan, len(result.Plans))}
	for i, p := range result.Plans {
		out.Plans[i] = catalogueOperatorPlan(p)
	}
	return out, catalogueError(err)
}

func (a *API) createCataloguePlan(ctx context.Context, actor, key uuid.UUID, in wire.CataloguePlanCreateInput) (wire.OperatorCataloguePlan, error) {
	out, err := a.catalogueOwner.CreateCataloguePlan(ctx, actor, key, catalogue.CreateInput{Reason: in.Reason, Terms: catalogueTermsInput(in.Terms)})
	return catalogueOperatorPlan(out), catalogueError(err)
}

func (a *API) reviseCataloguePlan(ctx context.Context, actor, id, key uuid.UUID, in wire.CataloguePlanRevisionInput) (wire.OperatorCataloguePlan, error) {
	out, err := a.catalogueOwner.ReviseCataloguePlan(ctx, actor, id, key, catalogue.RevisionInput{ExpectedRevision: in.ExpectedRevision, Reason: in.Reason, Terms: catalogueTermsInput(in.Terms)})
	return catalogueOperatorPlan(out), catalogueError(err)
}

func (a *API) archiveCataloguePlan(ctx context.Context, actor, id, key uuid.UUID, in wire.CataloguePlanArchiveInput) (wire.OperatorCataloguePlan, error) {
	out, err := a.catalogueOwner.ArchiveCataloguePlan(ctx, actor, id, key, catalogue.ArchiveInput(in))
	return catalogueOperatorPlan(out), catalogueError(err)
}
