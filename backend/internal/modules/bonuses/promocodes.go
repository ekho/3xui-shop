package bonuses

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/bonuses/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Service) CreatePromocode(ctx context.Context, actor, key uuid.UUID, in CreatePromocodeInput) (Promocode, error) {
	return s.changePromocode(ctx, actor, uuid.Nil, key, "create", in.DurationDays, 0, in.Reason)
}
func (s *Service) EditPromocode(ctx context.Context, actor, id, key uuid.UUID, in EditPromocodeInput) (Promocode, error) {
	if id == uuid.Nil || in.ExpectedRevision < 1 {
		return Promocode{}, failure(400, "INVALID_INPUT")
	}
	return s.changePromocode(ctx, actor, id, key, "edit", in.DurationDays, in.ExpectedRevision, in.Reason)
}
func (s *Service) DeletePromocode(ctx context.Context, actor, id, key uuid.UUID, in DeletePromocodeInput) (Promocode, error) {
	if id == uuid.Nil || in.ExpectedRevision < 1 {
		return Promocode{}, failure(400, "INVALID_INPUT")
	}
	return s.changePromocode(ctx, actor, id, key, "delete", 0, in.ExpectedRevision, in.Reason)
}

func (s *Service) changePromocode(ctx context.Context, actor, id, key uuid.UUID, action string, days int, revision int64, reason string) (Promocode, error) {
	if actor == uuid.Nil || key == uuid.Nil || !utf8.ValidString(reason) || strings.ContainsRune(reason, 0) || strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 1000 ||
		(action != "delete" && (days < 1 || days > 365)) {
		return Promocode{}, failure(400, "INVALID_INPUT")
	}
	body, _ := json.Marshal(struct {
		ID       uuid.UUID
		Days     int
		Revision int64
		Reason   string
	}{id, days, revision, reason})
	hash := sha256.Sum256(body)
	operation := action + "Promocode"
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Promocode{}, unavailable()
	}
	defer tx.Rollback(ctx)
	// The actor lock serializes replay and keeps role revocation ahead of every effect.
	if _, err = s.authority.LockOperatorPair(ctx, tx, actor, actor); err != nil {
		return Promocode{}, err
	}
	principal := "operator-account:" + actor.String()
	var prior, result []byte
	err = tx.QueryRow(ctx, "SELECT body_hash,result FROM idempotency_records WHERE principal=$1 AND operation=$2 AND key=$3", principal, operation, key).Scan(&prior, &result)
	if err == nil {
		if !bytes.Equal(prior, hash[:]) {
			return Promocode{}, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		var saved Promocode
		if json.Unmarshal(result, &saved) != nil {
			return Promocode{}, unavailable()
		}
		return saved, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Promocode{}, unavailable()
	}
	q := store.New(tx)
	var row store.Promocode
	var before *PromocodeMetadata
	now := pgtype.Timestamptz{Time: s.now().UTC().Truncate(time.Microsecond), Valid: true}
	if action == "create" {
		row, err = q.AddPromocode(ctx, store.AddPromocodeParams{ID: uuid.New(), Code: strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")), DurationDays: int32(days), CreatedAt: now})
	} else {
		row, err = q.LockPromocode(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return Promocode{}, failure(404, "PROMOCODE_NOT_FOUND")
		}
		if err != nil {
			return Promocode{}, unavailable()
		}
		if row.IsActivated {
			return Promocode{}, failure(409, "PROMOCODE_USED")
		}
		if row.DeletedAt.Valid {
			return Promocode{}, failure(409, "PROMOCODE_DELETED")
		}
		if row.Revision != revision {
			return Promocode{}, failure(409, "PROMOCODE_REVISION_CONFLICT")
		}
		metadata := promocodeMetadata(publicPromocode(row))
		before = &metadata
		if action == "edit" {
			row, err = q.EditPromocode(ctx, store.EditPromocodeParams{ID: id, DurationDays: int32(days)})
		} else {
			row, err = q.DeletePromocode(ctx, store.DeletePromocodeParams{ID: id, DeletedAt: now})
		}
	}
	if err != nil {
		return Promocode{}, unavailable()
	}
	out := publicPromocode(row)
	after, _ := json.Marshal(promocodeMetadata(out))
	var previous []byte
	if before != nil {
		previous, _ = json.Marshal(before)
	}
	reason = strings.TrimSpace(reason)
	eventID := uuid.New()
	if err = q.AddPromocodeEvent(ctx, store.AddPromocodeEventParams{ID: eventID, PromocodeID: out.PromocodeID, ActorAccountID: &actor, Action: action, CreatedAt: now, Reason: pgtype.Text{String: reason, Valid: true}, BeforeSnapshot: previous, AfterSnapshot: after}); err != nil {
		return Promocode{}, unavailable()
	}
	auditReason := "promocode_id=" + out.PromocodeID.String() + "; " + reason
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: eventID, AccountID: actor, CreatedAt: now.Time, Action: "promocode." + action, OperatorAccountID: &actor, Reason: &auditReason}); err != nil {
		return Promocode{}, unavailable()
	}
	result, _ = json.Marshal(out)
	if _, err = tx.Exec(ctx, "INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6)", principal, operation, key, hash[:], result, now.Time); err != nil {
		return Promocode{}, unavailable()
	}
	if tx.Commit(ctx) != nil {
		return Promocode{}, unavailable()
	}
	return out, nil
}

func (s *Service) ListPromocodes(ctx context.Context, actor uuid.UUID, page, perPage int) (PromocodeList, error) {
	out := PromocodeList{Promocodes: []Promocode{}, Page: page, PerPage: perPage}
	if page < 1 || page > 2147483647 || perPage < 1 || perPage > 50 {
		return out, failure(400, "INVALID_INPUT")
	}
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if out.Total, err = q.CountPromocodes(ctx); err != nil {
		return out, unavailable()
	}
	rows, err := q.ListPromocodes(ctx, store.ListPromocodesParams{PerPage: int32(perPage), OffsetRows: int64(page-1) * int64(perPage)})
	if err != nil {
		return out, unavailable()
	}
	for _, row := range rows {
		out.Promocodes = append(out.Promocodes, publicPromocode(row))
	}
	return out, nil
}

func (s *Service) GetPromocode(ctx context.Context, actor, id uuid.UUID) (PromocodeDetail, error) {
	out := PromocodeDetail{Events: []PromocodeEvent{}}
	if id == uuid.Nil {
		return out, failure(400, "INVALID_INPUT")
	}
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	row, err := q.ReadPromocode(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "PROMOCODE_NOT_FOUND")
	}
	if err != nil {
		return out, unavailable()
	}
	out.Promocode = publicPromocode(row)
	events, err := q.ReadPromocodeEvents(ctx, id)
	if err != nil {
		return out, unavailable()
	}
	out.EventsHasMore = len(events) > 50
	if out.EventsHasMore {
		events = events[:50]
	}
	for _, row := range events {
		event := PromocodeEvent{EventID: row.ID, ActorAccountID: row.ActorAccountID, Action: row.Action, CreatedAt: row.CreatedAt.Time}
		if row.Reason.Valid {
			event.Reason = &row.Reason.String
		}
		if len(row.BeforeSnapshot) > 0 && json.Unmarshal(row.BeforeSnapshot, &event.Before) != nil {
			return PromocodeDetail{}, unavailable()
		}
		if json.Unmarshal(row.AfterSnapshot, &event.After) != nil {
			return PromocodeDetail{}, unavailable()
		}
		out.Events = append(out.Events, event)
	}
	return out, nil
}

func publicPromocode(row store.Promocode) Promocode {
	out := Promocode{PromocodeID: row.ID, Code: row.Code, DurationDays: int(row.DurationDays), Revision: row.Revision, State: "available", ActivatedAccountID: row.ActivatedAccountID}
	if row.IsActivated {
		out.State = "activated"
	} else if row.DeletedAt.Valid {
		out.State = "deleted"
	}
	if row.CreatedAt.Valid {
		out.CreatedAt = &row.CreatedAt.Time
	}
	if row.ActivatedAt.Valid {
		out.ActivatedAt = &row.ActivatedAt.Time
	}
	if row.LegacySource.Valid {
		out.LegacySource = &row.LegacySource.String
	}
	if row.ActivatedByTgID.Valid {
		value := strconv.FormatInt(row.ActivatedByTgID.Int64, 10)
		out.ActivatedByTgID = &value
	}
	if row.LegacyPromocodeID.Valid {
		value := strconv.FormatInt(row.LegacyPromocodeID.Int64, 10)
		out.LegacyPromocodeID = &value
	}
	return out
}
func promocodeMetadata(row Promocode) PromocodeMetadata {
	return PromocodeMetadata{PromocodeID: row.PromocodeID, DurationDays: row.DurationDays, Revision: row.Revision, State: row.State}
}
