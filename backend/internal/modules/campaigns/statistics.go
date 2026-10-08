package campaigns

import (
	"context"
	"encoding/json"
	"errors"

	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Statistics struct {
	Users                 int64                         `json:"users"`
	WebRegistrations      int64                         `json:"web_registrations"`
	TelegramRegistrations int64                         `json:"telegram_registrations"`
	LegacyNameUsers       int64                         `json:"legacy_name_users"`
	LegacyTrialUsed       int64                         `json:"legacy_trial_used"`
	Trials                subscriptions.TrialStatistics `json:"trials"`
	Payments              payments.Statistics           `json:"payments"`
}
type Detail struct {
	Campaign      Campaign   `json:"campaign"`
	Statistics    Statistics `json:"statistics"`
	Events        []Event    `json:"events"`
	EventsHasMore bool       `json:"events_has_more"`
}

func (s *Service) CohortTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) ([]uuid.UUID, error) {
	ids := []uuid.UUID{}
	rows, err := tx.Query(ctx, `SELECT account_id FROM campaign_acquisitions WHERE campaign_id=$1 ORDER BY account_id`, id)
	if err != nil {
		return nil, unavailable()
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) != nil {
			return nil, unavailable()
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, unavailable()
	}
	return ids, nil
}
func (s *Service) Detail(ctx context.Context, actor, id uuid.UUID) (Detail, error) {
	out := Detail{Events: []Event{}}
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	out.Campaign, err = scanCampaign(tx.QueryRow(ctx, "SELECT "+campaignColumns+" FROM campaigns WHERE id=$1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	stat := &out.Statistics
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE channel='web'),count(*) FILTER(WHERE channel='telegram'),count(*) FILTER(WHERE channel='legacy_name'),count(*) FILTER(WHERE legacy_trial_used) FROM campaign_acquisitions WHERE campaign_id=$1`, id).Scan(&stat.Users, &stat.WebRegistrations, &stat.TelegramRegistrations, &stat.LegacyNameUsers, &stat.LegacyTrialUsed); err != nil {
		return out, unavailable()
	}
	ids, err := s.CohortTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	stat.Trials, err = s.subscriptions.StatisticsTx(ctx, tx, ids)
	if err != nil {
		return out, unavailable()
	}
	stat.Payments, err = s.payments.StatisticsTx(ctx, tx, ids)
	if err != nil {
		return out, unavailable()
	}
	rows, err := tx.Query(ctx, `SELECT id,actor_account_id,action,created_at,reason,before_snapshot,after_snapshot FROM campaign_events WHERE campaign_id=$1 ORDER BY created_at DESC,id DESC LIMIT 21`, id)
	if err != nil {
		return out, unavailable()
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		var before, after []byte
		if rows.Scan(&e.EventID, &e.ActorAccountID, &e.Action, &e.CreatedAt, &e.Reason, &before, &after) != nil {
			return out, unavailable()
		}
		if before != nil {
			if json.Unmarshal(before, &e.Before) != nil {
				return out, unavailable()
			}
		}
		if json.Unmarshal(after, &e.After) != nil {
			return out, unavailable()
		}
		out.Events = append(out.Events, e)
	}
	if rows.Err() != nil {
		return out, unavailable()
	}
	if len(out.Events) > 20 {
		out.EventsHasMore = true
		out.Events = out.Events[:20]
	}
	return out, nil
}
