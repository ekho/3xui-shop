package auditreports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/audit_reports/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type LegacyAuditPackage struct {
	Version int                `json:"version"`
	Events  []LegacyAuditInput `json:"events"`
}
type LegacyAuditInput struct {
	SourceID    int64     `json:"source_id"`
	CreatedAt   time.Time `json:"created_at"`
	Action      string    `json:"action"`
	TargetTgID  *int64    `json:"target_tg_id"`
	ActorType   *string   `json:"actor_type"`
	ActorID     *int64    `json:"actor_id"`
	ActorName   *string   `json:"actor_name"`
	Source      *string   `json:"source"`
	PayloadJSON *string   `json:"payload_json"`
}
type LegacyAuditImportResult struct {
	Inserted int `json:"inserted"`
	Replayed int `json:"replayed"`
}

func ValidateLegacyAuditPackage(p LegacyAuditPackage) error {
	if p.Version != 1 || p.Events == nil {
		return &Error{400, "IMPORT_INVALID_PACKAGE"}
	}
	seen := make(map[int64]bool, len(p.Events))
	for _, e := range p.Events {
		if e.SourceID <= 0 || seen[e.SourceID] || !validAuditTime(e.CreatedAt) || !auditTextValid(e.Action, 128) || strings.TrimSpace(e.Action) == "" || e.TargetTgID != nil && *e.TargetTgID <= 0 {
			return &Error{400, "IMPORT_INVALID_PACKAGE"}
		}
		seen[e.SourceID] = true
		for _, value := range []*string{e.ActorType, e.ActorName, e.Source} {
			if value != nil && !auditTextValid(*value, 256) {
				return &Error{400, "IMPORT_INVALID_PACKAGE"}
			}
		}
		if e.PayloadJSON != nil {
			var object map[string]json.RawMessage
			if len(*e.PayloadJSON) > 64<<10 || !utf8.ValidString(*e.PayloadJSON) || json.Unmarshal([]byte(*e.PayloadJSON), &object) != nil || object == nil {
				return &Error{400, "IMPORT_INVALID_PACKAGE"}
			}
		}
	}
	return nil
}

func auditTextValid(value string, max int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && utf8.RuneCountInString(value) <= max
}

func (s *Service) ImportLegacy(ctx context.Context, p LegacyAuditPackage, dryRun bool) (LegacyAuditImportResult, error) {
	out := LegacyAuditImportResult{}
	if err := ValidateLegacyAuditPackage(p); err != nil {
		return out, err
	}
	mode := pgx.ReadWrite
	if dryRun {
		mode = pgx.ReadOnly
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: mode})
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	events := append([]LegacyAuditInput(nil), p.Events...)
	// Shared packages lock source IDs in one order, including overlapping batches.
	sort.Slice(events, func(i, j int) bool { return events[i].SourceID < events[j].SourceID })
	for _, e := range events {
		e.CreatedAt = e.CreatedAt.UTC()
		encoded, err := json.Marshal(e)
		if err != nil {
			return LegacyAuditImportResult{}, &Error{400, "IMPORT_INVALID_PACKAGE"}
		}
		hash := sha256.Sum256(encoded)
		fresh := false
		if !dryRun {
			inserted, err := q.InsertLegacyAuditDigest(ctx, store.InsertLegacyAuditDigestParams{SourceID: e.SourceID, SourceHash: hash[:]})
			if err != nil {
				return LegacyAuditImportResult{}, unavailable()
			}
			fresh = inserted == 1
		}
		if !fresh {
			prior, err := q.LegacyAuditDigest(ctx, e.SourceID)
			if dryRun && errors.Is(err, pgx.ErrNoRows) {
				fresh = true
			} else if err != nil {
				return LegacyAuditImportResult{}, unavailable()
			} else if !bytes.Equal(prior, hash[:]) {
				return LegacyAuditImportResult{}, &Error{409, "IMPORT_SOURCE_CONFLICT"}
			}
		}
		if !fresh {
			out.Replayed++
			continue
		}
		out.Inserted++
		if dryRun {
			continue
		}
		var payload []byte
		if e.PayloadJSON != nil {
			payload = []byte(*e.PayloadJSON)
		}
		if q.InsertLegacyAudit(ctx, store.InsertLegacyAuditParams{SourceID: e.SourceID, CreatedAt: pgtype.Timestamptz{Time: e.CreatedAt, Valid: true}, Action: e.Action, TargetTgID: intValue(e.TargetTgID), ActorType: textValue(e.ActorType), ActorID: intValue(e.ActorID), ActorName: textValue(e.ActorName), Source: textValue(e.Source), PayloadJson: payload}) != nil {
			return LegacyAuditImportResult{}, unavailable()
		}
	}
	if !dryRun && out.Inserted > 0 {
		now, err := q.AuditDatabaseTime(ctx)
		if err != nil || q.InsertSystemAudit(ctx, store.InsertSystemAuditParams{ID: uuid.New(), CreatedAt: now, Action: "audit.legacy_imported", LegacyCount: int64(out.Inserted)}) != nil {
			return LegacyAuditImportResult{}, unavailable()
		}
	}
	if tx.Commit(ctx) != nil {
		return LegacyAuditImportResult{}, unavailable()
	}
	return out, nil
}
