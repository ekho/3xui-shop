package platform

import (
	"context"
	"example.com/cabinet/backend/internal/modules/accounts"
)

type LegacyApprovalPackage = accounts.LegacyApprovalPackage
type LegacyApprovalUser = accounts.LegacyApprovalUser
type LegacyApprovalSourceEvent = accounts.LegacyApprovalSourceEvent
type LegacyApprovalImportResult = accounts.LegacyApprovalImportResult

func (s *Service) ImportLegacyApprovals(ctx context.Context, p LegacyApprovalPackage, dryRun bool) (LegacyApprovalImportResult, error) {
	out, err := s.accounts.ImportLegacyApprovals(ctx, p, dryRun)
	return out, accountError(err)
}
