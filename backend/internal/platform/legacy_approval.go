package platform

import (
	"bytes"
	"context"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"sort"
	"strings"
	"time"
)

type LegacyApprovalPackage struct {
	Version        int                         `json:"version"`
	Users          []LegacyApprovalUser        `json:"users"`
	ApprovalEvents []LegacyApprovalSourceEvent `json:"approval_events"`
}
type LegacyApprovalUser struct {
	SourceLegacyUserID int64      `json:"source_legacy_user_id"`
	SourceTgID         int64      `json:"source_tg_id"`
	Status             string     `json:"status"`
	RequestedAt        *time.Time `json:"requested_at"`
	DecidedAt          *time.Time `json:"decided_at"`
	DecidedBy          *int64     `json:"decided_by"`
}
type LegacyApprovalSourceEvent struct {
	SourceID   int64     `json:"source_id"`
	TargetTgID int64     `json:"target_tg_id"`
	CreatedAt  time.Time `json:"created_at"`
	Action     string    `json:"action"`
	ActorType  *string   `json:"actor_type"`
	ActorID    *int64    `json:"actor_id"`
	ActorName  *string   `json:"actor_name"`
	Source     *string   `json:"source"`
}
type LegacyApprovalImportResult struct {
	Users   int `json:"users"`
	Events  int `json:"events"`
	Changed int `json:"changed"`
}

func validLegacyTime(t *time.Time) bool {
	return t == nil || (!t.IsZero() && t.Location() != nil && t.Year() >= 1 && t.Year() <= 9999 && t.Nanosecond()%1000 == 0)
}
func legacyStamp(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return stamp(*t)
}
func legacyInt(id *int64) pgtype.Int8 {
	if id == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *id, Valid: true}
}
func legacyText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}
func sameStamp(a pgtype.Timestamptz, b *time.Time) bool {
	return (!a.Valid && b == nil) || (a.Valid && b != nil && a.Time.Equal(*b))
}
func sameInt(a pgtype.Int8, b *int64) bool {
	return (!a.Valid && b == nil) || (a.Valid && b != nil && a.Int64 == *b)
}
func sameText(a pgtype.Text, b *string) bool {
	return (!a.Valid && b == nil) || (a.Valid && b != nil && a.String == *b)
}

func validateLegacyPackage(p LegacyApprovalPackage) error {
	if p.Version != 1 || p.Users == nil || p.ApprovalEvents == nil {
		return failure(400, "IMPORT_INVALID_PACKAGE")
	}
	users := map[int64]bool{}
	tgIDs := map[int64]bool{}
	events := map[int64]bool{}
	for _, u := range p.Users {
		if u.SourceLegacyUserID <= 0 || u.SourceTgID <= 0 || users[u.SourceLegacyUserID] || tgIDs[u.SourceTgID] || !validLegacyTime(u.RequestedAt) || !validLegacyTime(u.DecidedAt) || (u.DecidedBy != nil && *u.DecidedBy <= 0) {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		if u.Status != "pending" && u.Status != "approved" && u.Status != "rejected" {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		users[u.SourceLegacyUserID] = true
		tgIDs[u.SourceTgID] = true
	}
	for _, e := range p.ApprovalEvents {
		if e.SourceID <= 0 || e.TargetTgID <= 0 || events[e.SourceID] || !tgIDs[e.TargetTgID] || !validLegacyTime(&e.CreatedAt) || (e.Action != "approval.approve" && e.Action != "approval.reject") || (e.ActorID != nil && *e.ActorID <= 0) || (e.Source != nil && strings.ContainsRune(*e.Source, '\x00')) {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		if e.ActorType != nil && (*e.ActorType == "" || strings.ContainsRune(*e.ActorType, '\x00')) {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		if e.ActorName != nil && strings.ContainsRune(*e.ActorName, '\x00') {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		events[e.SourceID] = true
	}
	return nil
}

// The whole package is one transaction. Dry run reads the same rows but never writes.
func (s *Service) ImportLegacyApprovals(ctx context.Context, p LegacyApprovalPackage, dryRun bool) (LegacyApprovalImportResult, error) {
	var result LegacyApprovalImportResult
	if err := validateLegacyPackage(p); err != nil {
		return result, err
	}
	opts := pgx.TxOptions{}
	if dryRun {
		opts.AccessMode = pgx.ReadOnly
	}
	tx, err := s.pool.BeginTx(ctx, opts)
	if err != nil {
		return result, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	type located struct {
		user LegacyApprovalUser
		id   uuid.UUID
	}
	locatedUsers := make([]located, 0, len(p.Users))
	ids := make(map[int64]uuid.UUID, len(p.Users))
	for _, u := range p.Users {
		a, err := q.AccountByTelegramID(ctx, pgtype.Int8{Int64: u.SourceTgID, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return result, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		if err != nil {
			return result, unavailable()
		}
		locatedUsers = append(locatedUsers, located{u, a.ID})
		ids[u.SourceTgID] = a.ID
	}
	if !dryRun {
		sort.Slice(locatedUsers, func(i, j int) bool { return bytes.Compare(locatedUsers[i].id[:], locatedUsers[j].id[:]) < 0 })
	}
	for _, item := range locatedUsers {
		u, id := item.user, item.id
		var a store.Account
		if dryRun {
			a, err = q.AccountByID(ctx, id)
		} else {
			a, err = q.LockAccount(ctx, id)
		}
		if err != nil {
			return result, unavailable()
		}
		if !a.TelegramID.Valid || a.TelegramID.Int64 != u.SourceTgID || !a.LegacyUserID.Valid || a.LegacyUserID.Int64 != u.SourceLegacyUserID {
			return result, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		prior, err := q.LegacyApprovalBySourceID(ctx, u.SourceLegacyUserID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return result, unavailable()
		}
		fresh := errors.Is(err, pgx.ErrNoRows)
		if !fresh && (prior.AccountID != id || prior.SourceTgID != u.SourceTgID || prior.Status != u.Status || !sameStamp(prior.RequestedAt, u.RequestedAt) || !sameStamp(prior.DecidedAt, u.DecidedAt) || !sameInt(prior.DecidedBy, u.DecidedBy)) {
			return result, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		other, err := q.LegacyApprovalByAccount(ctx, id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return result, unavailable()
		}
		if err == nil && other.SourceLegacyUserID != u.SourceLegacyUserID {
			return result, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		if fresh {
			result.Users++
			if !dryRun {
				n, err := q.InsertLegacyApproval(ctx, store.InsertLegacyApprovalParams{AccountID: id, SourceLegacyUserID: u.SourceLegacyUserID, SourceTgID: u.SourceTgID, Status: u.Status, RequestedAt: legacyStamp(u.RequestedAt), DecidedAt: legacyStamp(u.DecidedAt), DecidedBy: legacyInt(u.DecidedBy)})
				if err != nil {
					return result, failure(409, "IMPORT_SOURCE_CONFLICT")
				}
				if n != 1 {
					return result, failure(409, "IMPORT_SOURCE_CONFLICT")
				}
			}
		}
		if fresh && u.Status == "rejected" {
			protected, err := q.OperatorRoleExists(ctx, id)
			if err != nil {
				return result, unavailable()
			}
			if protected {
				return result, failure(409, "IMPORT_PROTECTED_OPERATOR")
			}
			if !a.Restricted {
				result.Changed++
				if !dryRun {
					if q.SetAccountRestriction(ctx, store.SetAccountRestrictionParams{ID: id, Restricted: true}) != nil || q.DeleteAccountSessions(ctx, id) != nil || s.revokeCredentialProofs(ctx, tx, id) != nil {
						return result, unavailable()
					}
				}
			}
		}
	}
	for _, e := range p.ApprovalEvents {
		id := ids[e.TargetTgID]
		prior, err := q.LegacyApprovalEventByID(ctx, e.SourceID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return result, unavailable()
		}
		if err == nil {
			if prior.AccountID != id || prior.TargetTgID != e.TargetTgID || !prior.CreatedAt.Time.Equal(e.CreatedAt) || prior.Action != e.Action || !sameText(prior.ActorType, e.ActorType) || !sameInt(prior.ActorID, e.ActorID) || !sameText(prior.ActorName, e.ActorName) || !sameText(prior.Source, e.Source) {
				return result, failure(409, "IMPORT_SOURCE_CONFLICT")
			}
			continue
		}
		result.Events++
		if !dryRun {
			n, err := q.InsertLegacyApprovalEvent(ctx, store.InsertLegacyApprovalEventParams{SourceID: e.SourceID, AccountID: id, TargetTgID: e.TargetTgID, CreatedAt: stamp(e.CreatedAt), Action: e.Action, ActorType: legacyText(e.ActorType), ActorID: legacyInt(e.ActorID), ActorName: legacyText(e.ActorName), Source: legacyText(e.Source)})
			if err != nil || n != 1 {
				return result, failure(409, "IMPORT_SOURCE_CONFLICT")
			}
		}
	}
	if !dryRun {
		if err := tx.Commit(ctx); err != nil {
			return result, unavailable()
		}
	}
	return result, nil
}
