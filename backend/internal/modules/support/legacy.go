package support

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/support/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sort"
)

type LegacySupportInput struct {
	Version int                `json:"version"`
	BotID   int64              `json:"bot_id"`
	GroupID int64              `json:"group_id"`
	Tickets []LegacySupportRow `json:"tickets"`
}
type LegacySupportRow struct {
	SourceID   int64  `json:"source_id"`
	TelegramID int64  `json:"tg_id"`
	ThreadID   *int64 `json:"thread_id"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}
type LegacySupportResult struct {
	Inserted int `json:"inserted"`
	Replayed int `json:"replayed"`
	Orphans  int `json:"orphans"`
}

// C46 uses the immutable original account mapping; current Telegram is not proof.
func (s *Service) BindLegacySupport(ctx context.Context, botID, groupID, sourceID int64, target uuid.UUID) error {
	if botID <= 0 || groupID >= 0 || sourceID <= 0 || target == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	source, err := q.LegacySupportSource(ctx, store.LegacySupportSourceParams{BotID: botID, GroupID: groupID, SourceID: sourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return unavailable()
	}
	var original LegacySupportRow
	if json.Unmarshal(source.SourceJson, &original) != nil || original.SourceID != sourceID {
		return failure(409, "IMPORT_SOURCE_CONFLICT")
	}
	hash := bodyHash(struct {
		Version        int
		BotID, GroupID int64
		Ticket         LegacySupportRow
	}{1, botID, groupID, original})
	if !bytes.Equal(hash, source.SourceHash) {
		return failure(409, "IMPORT_SOURCE_CONFLICT")
	}
	links, err := s.authority.LegacyAuditLinksTx(ctx, tx, []int64{original.TelegramID})
	if err != nil {
		return err
	}
	if links[original.TelegramID] != target {
		return failure(409, "IMPORT_IDENTITY_CONFLICT")
	}
	a, err := s.authority.Lock(ctx, tx, target)
	if err != nil {
		return err
	}
	proof, err := s.authority.LegacyApprovalTx(ctx, tx, target)
	if err != nil || a.LegacyUserID == nil || *a.LegacyUserID != proof.SourceLegacyUserID || proof.SourceTgID != original.TelegramID {
		return failure(409, "IMPORT_IDENTITY_CONFLICT")
	}
	topic, err := q.LegacyTelegramTopic(ctx, store.LegacyTelegramTopicParams{BotID: botID, GroupID: groupID, SourceID: telegramInt(sourceID)})
	if err != nil {
		return unavailable()
	}
	topic, err = q.LockTelegramTopic(ctx, topic.ID)
	if err != nil {
		return unavailable()
	}
	if topic.Kind == "account" && topic.AccountID != nil && *topic.AccountID == target {
		return nil
	}
	if topic.Kind != "orphan" || topic.AccountID != nil {
		return failure(409, "IMPORT_IDENTITY_CONFLICT")
	}
	_, err = q.AccountTelegramTopic(ctx, store.AccountTelegramTopicParams{BotID: botID, GroupID: groupID, AccountID: &target})
	if err == nil {
		return failure(409, "IMPORT_DESTINATION_CONFLICT")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return unavailable()
	}
	if q.BindLegacyTelegramTopic(ctx, store.BindLegacyTelegramTopicParams{ID: topic.ID, AccountID: &target}) != nil {
		return unavailable()
	}
	event := auditreports.SupportTelegramEvent{ID: uuid.New(), BotID: botID, GroupID: groupID, ChatID: groupID, ThreadID: original.ThreadID, TargetAccountID: &target, TopicID: &topic.ID, SourceID: &sourceID, Kind: "legacy_bound", Outcome: "completed"}
	if err = auditreports.RecordSupportTelegramTx(ctx, tx, event); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

func (r *LegacySupportRow) UnmarshalJSON(data []byte) error {
	type row LegacySupportRow
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode((*row)(r)); err != nil {
		return err
	}
	_, err := auditreports.ParseTimestamp(r.CreatedAt)
	if err != nil {
		return err
	}
	_, err = auditreports.ParseTimestamp(r.UpdatedAt)
	return err
}
func ValidateLegacySupportInput(p LegacySupportInput) error {
	if p.Version != 1 || p.BotID <= 0 || p.GroupID >= 0 || p.Tickets == nil {
		return failure(400, "IMPORT_INVALID_PACKAGE")
	}
	ids, tgs, threads := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	for _, r := range p.Tickets {
		created, e := auditreports.ParseTimestamp(r.CreatedAt)
		updated, e2 := auditreports.ParseTimestamp(r.UpdatedAt)
		raw, e3 := json.Marshal(r)
		if r.SourceID <= 0 || r.TelegramID <= 0 || ids[r.SourceID] || tgs[r.TelegramID] || r.ThreadID != nil && (*r.ThreadID <= 0 || threads[*r.ThreadID]) || e != nil || e2 != nil || e3 != nil || updated.Before(created) || len(raw) > 65536 || (r.Status != "open" && r.Status != "closed" && r.Status != "banned") {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		ids[r.SourceID], tgs[r.TelegramID] = true, true
		if r.ThreadID != nil {
			threads[*r.ThreadID] = true
		}
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > 32<<20 {
		return failure(400, "IMPORT_INVALID_PACKAGE")
	}
	return nil
}
func (s *Service) ImportLegacySupport(ctx context.Context, p LegacySupportInput, dry bool) (LegacySupportResult, error) {
	var out LegacySupportResult
	if err := ValidateLegacySupportInput(p); err != nil {
		return out, err
	}
	mode := pgx.ReadWrite
	if dry {
		mode = pgx.ReadOnly
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: mode})
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	out, err = s.ImportLegacySupportTx(ctx, tx, p, dry)
	if err != nil || dry {
		return out, err
	}
	if tx.Commit(ctx) != nil {
		return LegacySupportResult{}, unavailable()
	}
	return out, nil
}

// ImportLegacySupportTx participates in the caller's transaction.
func (s *Service) ImportLegacySupportTx(ctx context.Context, tx pgx.Tx, p LegacySupportInput, dry bool) (LegacySupportResult, error) {
	var out LegacySupportResult
	if tx == nil {
		return out, unavailable()
	}
	if err := ValidateLegacySupportInput(p); err != nil {
		return out, err
	}
	q := store.New(tx)
	tickets := append([]LegacySupportRow(nil), p.Tickets...)
	sort.Slice(tickets, func(i, j int) bool { return tickets[i].SourceID < tickets[j].SourceID })
	tgs := make([]int64, 0, len(tickets))
	for _, r := range tickets {
		tgs = append(tgs, r.TelegramID)
	}
	links, err := s.authority.LegacyAuditLinksTx(ctx, tx, tgs)
	if err != nil {
		return out, err
	}
	ids := make([]uuid.UUID, 0, len(links))
	seen := map[uuid.UUID]bool{}
	for _, id := range links {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	snapshots := map[uuid.UUID]accounts.Snapshot{}
	if dry {
		rows, e := s.authority.LookupManyTx(ctx, tx, ids)
		if e != nil {
			return out, e
		}
		for _, a := range rows {
			snapshots[a.ID] = a
		}
	} else {
		for _, id := range ids {
			a, e := s.authority.Lock(ctx, tx, id)
			if e != nil {
				return out, e
			}
			snapshots[id] = a
		}
	}
	mapped := map[uuid.UUID]bool{}
	for _, r := range tickets {
		raw, err := json.Marshal(r)
		if err != nil {
			return LegacySupportResult{}, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		hash := bodyHash(struct {
			Version        int
			BotID, GroupID int64
			Ticket         LegacySupportRow
		}{p.Version, p.BotID, p.GroupID, r})
		fresh := false
		if !dry {
			n, e := q.InsertLegacySupport(ctx, store.InsertLegacySupportParams{BotID: p.BotID, GroupID: p.GroupID, SourceID: r.SourceID, SourceHash: hash, SourceJson: raw})
			if e != nil {
				return LegacySupportResult{}, unavailable()
			}
			fresh = n == 1
		}
		if !fresh {
			prior, e := q.LegacySupportDigest(ctx, store.LegacySupportDigestParams{BotID: p.BotID, GroupID: p.GroupID, SourceID: r.SourceID})
			if dry && errors.Is(e, pgx.ErrNoRows) {
				fresh = true
			} else if e != nil {
				return LegacySupportResult{}, unavailable()
			} else if !bytes.Equal(prior, hash) {
				return LegacySupportResult{}, failure(409, "IMPORT_SOURCE_CONFLICT")
			}
		}
		if !fresh {
			out.Replayed++
			continue
		}
		var account *uuid.UUID
		kind := "orphan"
		if id, ok := links[r.TelegramID]; ok {
			snapshot, found := snapshots[id]
			proof, e := s.authority.LegacyApprovalTx(ctx, tx, id)
			if !found || e != nil || snapshot.LegacyUserID == nil || *snapshot.LegacyUserID != proof.SourceLegacyUserID || proof.SourceTgID != r.TelegramID {
				return LegacySupportResult{}, failure(409, "IMPORT_IDENTITY_CONFLICT")
			}
			if mapped[id] {
				return LegacySupportResult{}, failure(409, "IMPORT_DESTINATION_CONFLICT")
			}
			mapped[id] = true
			account = &id
			kind = "account"
			_, e = q.AccountTelegramTopic(ctx, store.AccountTelegramTopicParams{BotID: p.BotID, GroupID: p.GroupID, AccountID: account})
			if e == nil {
				return LegacySupportResult{}, failure(409, "IMPORT_DESTINATION_CONFLICT")
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return LegacySupportResult{}, unavailable()
			}
		} else {
			out.Orphans++
		}
		if r.ThreadID != nil {
			inUse, e := q.TelegramThreadInUse(ctx, store.TelegramThreadInUseParams{BotID: p.BotID, GroupID: p.GroupID, ThreadID: telegramInt(*r.ThreadID), ID: uuid.Nil})
			if e != nil {
				return LegacySupportResult{}, unavailable()
			}
			if inUse {
				return LegacySupportResult{}, failure(409, "IMPORT_DESTINATION_CONFLICT")
			}
		}
		out.Inserted++
		if dry {
			continue
		}
		status := "pending"
		thread := telegramInt(0)
		if r.ThreadID != nil {
			thread = telegramInt(*r.ThreadID)
			status = "unknown"
			if *r.ThreadID > 1 && *r.ThreadID <= 1<<52-1 {
				status = "ready"
			}
		}
		created, _ := auditreports.ParseTimestamp(r.CreatedAt)
		id := uuid.New()
		if q.AddLegacyTelegramTopic(ctx, store.AddLegacyTelegramTopicParams{ID: id, BotID: p.BotID, GroupID: p.GroupID, Kind: kind, AccountID: account, ThreadID: thread, Status: status, Closed: r.Status != "open", SupportBanned: r.Status == "banned", CreatedAt: stamp(created), SourceID: telegramInt(r.SourceID)}) != nil {
			return LegacySupportResult{}, unavailable()
		}
		event := auditreports.SupportTelegramEvent{ID: uuid.New(), BotID: p.BotID, GroupID: p.GroupID, ChatID: p.GroupID, ThreadID: r.ThreadID, TargetAccountID: account, TopicID: &id, SourceID: &r.SourceID, Kind: "legacy_imported", Outcome: "completed"}
		if err = auditreports.RecordSupportTelegramTx(ctx, tx, event); err != nil {
			return LegacySupportResult{}, err
		}
	}
	return out, nil
}
