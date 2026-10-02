package s01

import (
	"context"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"testing"
	"time"
)

// Replaying a rejected snapshot after a manual unblock must leave the account open.
func TestLegacyApprovalImportAtomicReplay(t *testing.T) {
	s, e := fixture(t)
	customer := verified(t, s, e, "legacy-rejected@example.test")
	approved := verified(t, s, e, "legacy-approved@example.test")
	pending := verified(t, s, e, "legacy-pending@example.test")
	actor := verified(t, s, e, "legacy-operator@example.test")
	ctx := context.Background()
	if err := s.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	const tgID int64 = 701234567
	for id, tg := range map[uuid.UUID]int64{customer: tgID, approved: tgID + 1, pending: tgID + 2} {
		if _, err := s.pool.Exec(ctx, `UPDATE accounts SET telegram_id=$2,legacy_user_id=$3 WHERE id=$1`, id, tg, 51+tg-tgID); err != nil {
			t.Fatal(err)
		}
	}
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	pkg := LegacyApprovalPackage{Version: 1, Users: []LegacyApprovalUser{{SourceLegacyUserID: 51, SourceTgID: tgID, Status: "rejected", DecidedAt: &when}, {SourceLegacyUserID: 52, SourceTgID: tgID + 1, Status: "approved"}, {SourceLegacyUserID: 53, SourceTgID: tgID + 2, Status: "pending"}}, ApprovalEvents: []LegacyApprovalSourceEvent{}}
	for i := int64(0); i < 52; i++ {
		pkg.ApprovalEvents = append(pkg.ApprovalEvents, LegacyApprovalSourceEvent{SourceID: 71 + i, TargetTgID: tgID, CreatedAt: when, Action: "approval.reject", Source: nil})
	}
	if _, err := s.ImportLegacyApprovals(ctx, pkg, true); err != nil {
		t.Fatal("dryrun", err)
	}
	var restricted bool
	if err := s.pool.QueryRow(ctx, `SELECT restricted FROM accounts WHERE id=$1`, customer).Scan(&restricted); err != nil || restricted {
		t.Fatal("dryrun wrote", err)
	}
	if _, err := s.ImportLegacyApprovals(ctx, pkg, false); err != nil {
		t.Fatal("apply", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT restricted FROM accounts WHERE id=$1`, customer).Scan(&restricted); err != nil || !restricted {
		t.Fatal("reject not restricted", err)
	}
	card, err := s.OperatorClient(ctx, actor, customer)
	if err != nil || card.LegacyApproval == nil || card.LegacyApproval.DecidedBy != nil || card.LegacyApproval.RequestedAt != nil || len(card.LegacyEvents) != 50 || !card.LegacyHasMore || card.LegacyEvents[0].Source != nil {
		t.Fatal("legacy card or nullable metadata", err)
	}
	before := card.LegacyEvents[49].CreatedAt
	sourceID := card.LegacyEvents[49].SourceId
	next, err := s.OperatorClientHistory(ctx, actor, customer, wire.OperatorHistoryInput{Kind: "legacy", BeforeCreatedAt: &before, BeforeSourceId: &sourceID})
	if err != nil || len(next.LegacyEvents) != 2 || next.HasMore {
		t.Fatal("same-time source-id cursor", err)
	}
	imported, err := s.SetOperatorRestriction(ctx, actor, customer, uuid.New(), wire.OperatorRestrictionInput{Restricted: true, Reason: "unchanged"})
	if err != nil || imported.ChangedAt != nil || imported.OperatorAccountId != nil {
		t.Fatal("invented web actor for import", err)
	}
	if _, err := s.SetOperatorRestriction(ctx, actor, customer, uuid.New(), wire.OperatorRestrictionInput{Restricted: false, Reason: "restored"}); err != nil {
		t.Fatal("unrestrict", err)
	}
	if _, err := s.ImportLegacyApprovals(ctx, pkg, false); err != nil {
		t.Fatal("replay", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT restricted FROM accounts WHERE id=$1`, customer).Scan(&restricted); err != nil || restricted {
		t.Fatal("replay restricted", err)
	}
	pkg.ApprovalEvents[0].Action = "approval.approve"
	extra := verified(t, s, e, "legacy-extra@example.test")
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET telegram_id=$2,legacy_user_id=54 WHERE id=$1`, extra, tgID+3); err != nil {
		t.Fatal(err)
	}
	pkg.Users = append(pkg.Users, LegacyApprovalUser{SourceLegacyUserID: 54, SourceTgID: tgID + 3, Status: "approved"})
	if _, err := s.ImportLegacyApprovals(ctx, pkg, false); err == nil {
		t.Fatal("changed event accepted")
	}
	var extraCount int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM legacy_approval_snapshots WHERE account_id=$1`, extra).Scan(&extraCount); err != nil || extraCount != 0 {
		t.Fatal("whole-package rollback", err)
	}
	var events int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM legacy_approval_events WHERE account_id=$1 AND action='approval.approve'`, customer).Scan(&events); err != nil || events != 0 {
		t.Fatal("conflict wrote", err)
	}
}

func legacySource(s string) *string { return &s }

func TestLegacyApprovalImportRejectsUnmatchedIdentity(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	first := verified(t, s, e, "import-first@example.test")
	second := verified(t, s, e, "import-second@example.test")
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET telegram_id=801,legacy_user_id=101 WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET telegram_id=802 WHERE id=$1`, second); err != nil {
		t.Fatal(err)
	}
	pkg := LegacyApprovalPackage{Version: 1, Users: []LegacyApprovalUser{{SourceLegacyUserID: 101, SourceTgID: 801, Status: "approved"}, {SourceLegacyUserID: 102, SourceTgID: 802, Status: "rejected"}}, ApprovalEvents: []LegacyApprovalSourceEvent{}}
	for _, legacyID := range []any{nil, int64(999)} {
		if legacyID != nil {
			if _, err := s.pool.Exec(ctx, `UPDATE accounts SET legacy_user_id=$2 WHERE id=$1`, second, legacyID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.ImportLegacyApprovals(ctx, pkg, false); restrictionCode(err) != "IMPORT_IDENTITY_CONFLICT" {
			t.Fatal("missing or wrong legacy user identity accepted", err)
		}
		var count int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM legacy_approval_snapshots WHERE account_id IN ($1,$2)`, first, second).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial package after identity conflict", err)
		}
	}
}

func TestLegacyApprovalImportProtectsRestrictedOperator(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	operator := verified(t, s, e, "legacy-protected@example.test")
	if err := s.ChangeOperatorRole(ctx, operator, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET telegram_id=901,legacy_user_id=201,restricted=true WHERE id=$1`, operator); err != nil {
		t.Fatal(err)
	}
	pkg := LegacyApprovalPackage{Version: 1, Users: []LegacyApprovalUser{{SourceLegacyUserID: 201, SourceTgID: 901, Status: "rejected"}}, ApprovalEvents: []LegacyApprovalSourceEvent{}}
	if _, err := s.ImportLegacyApprovals(ctx, pkg, false); restrictionCode(err) != "IMPORT_PROTECTED_OPERATOR" {
		t.Fatal("protected operator imported", err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM legacy_approval_snapshots WHERE account_id=$1`, operator).Scan(&count); err != nil || count != 0 {
		t.Fatal("protected snapshot persisted", err)
	}
}

func TestLegacyApprovalImportRejectsInvalidEventDate(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	id := verified(t, s, e, "legacy-date@example.test")
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET telegram_id=902,legacy_user_id=202 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	pkg := LegacyApprovalPackage{Version: 1, Users: []LegacyApprovalUser{{SourceLegacyUserID: 202, SourceTgID: 902, Status: "approved"}}, ApprovalEvents: []LegacyApprovalSourceEvent{{SourceID: 301, TargetTgID: 902, CreatedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), Action: "approval.approve"}}}
	if _, err := s.ImportLegacyApprovals(ctx, pkg, false); restrictionCode(err) != "IMPORT_INVALID_PACKAGE" {
		t.Fatal("out-of-range event time accepted", err)
	}
}
