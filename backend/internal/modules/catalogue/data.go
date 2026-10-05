package catalogue

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/catalogue/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("catalogue plan not found")
var ErrInvalidTerms = errors.New("invalid catalogue terms")

func currentPlan(id uuid.UUID, revision int64, archived, hidden bool, profile string, raw []byte, err error) (CurrentPlan, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return CurrentPlan{}, ErrNotFound
	}
	if err != nil {
		return CurrentPlan{}, unavailable()
	}
	plan := CurrentPlan{ID: id, Revision: revision, Archived: archived, Hidden: hidden, Profile: profile}
	if json.Unmarshal(raw, &plan.Terms) != nil {
		return plan, ErrInvalidTerms
	}
	return plan, nil
}

func (s *Service) CurrentPlanTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (CurrentPlan, error) {
	r, err := store.New(tx).CatalogueByID(ctx, id)
	return currentPlan(r.ID, r.CurrentRevision, r.Archived, r.CurrentHidden, r.CurrentProfile, r.Terms, err)
}

// LockCurrentPlan holds FOR UPDATE in the caller's transaction, until its commit/rollback.
func (s *Service) LockCurrentPlan(ctx context.Context, tx pgx.Tx, id uuid.UUID) (CurrentPlan, error) {
	r, err := store.New(tx).CatalogueByIDForUpdate(ctx, id)
	return currentPlan(r.ID, r.CurrentRevision, r.Archived, r.CurrentHidden, r.CurrentProfile, r.Terms, err)
}

func (s *Service) UnlimitedPlansTx(ctx context.Context, tx pgx.Tx) ([]CurrentPlan, error) {
	rows, err := store.New(tx).CatalogueUnlimited(ctx)
	if err != nil {
		return nil, unavailable()
	}
	out := make([]CurrentPlan, 0, len(rows))
	for _, r := range rows {
		plan, err := currentPlan(r.ID, r.CurrentRevision, r.Archived, r.CurrentHidden, r.CurrentProfile, r.Terms, nil)
		// Existing grants ignore archived rows before inspecting terms.
		if err != nil && !r.Archived {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, nil
}
