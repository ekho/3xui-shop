package vpn

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func reminderPeriod(a accounts.Snapshot, b statisticsBaseline, panel string, resetID *uuid.UUID, resetRaw json.RawMessage) notifications.ReminderAccess {
	t, _, confirmed := statisticsTarget(a, b, panel)
	if !confirmed || a.Restricted || a.VpnBanned {
		return notifications.ReminderAccess{}
	}
	p := notifications.ReminderAccess{Known: true, ExpiryMS: t.ExpiryTimeMS, LimitBytes: t.TrafficLimitBytes}
	if resetID != nil {
		var reset AccessTarget
		if json.Unmarshal(resetRaw, &reset) == nil && reset.OperationID == *resetID && reset.Reset && !reset.NoClientIntent && reset.PanelID == panel && reset.PanelKey == a.PanelKey && reset.VPNID == a.VpnID && reset.SubID == a.SubID {
			p.TrafficPeriod = resetID.String()
		}
	} else if b.TrialID != nil && b.TrialStatus == "applied" {
		var trial ProvisionTarget
		if json.Unmarshal(b.TrialTarget, &trial) == nil && trial.OperationID == *b.TrialID && trial.PanelID == panel && trial.PanelKey == a.PanelKey && trial.VPNID == a.VpnID && trial.SubID == a.SubID {
			p.TrafficPeriod = b.TrialID.String()
		}
	}
	return p
}

type reminderReset struct {
	id  *uuid.UUID
	raw json.RawMessage
}

func reminderResetsTx(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID]reminderReset, error) {
	rows, err := tx.Query(ctx, `SELECT selected.account_id,reset.id,reset.target FROM unnest($1::uuid[]) selected(account_id)
 LEFT JOIN LATERAL(SELECT id,target FROM access_operations WHERE account_id=selected.account_id AND status='applied'
 AND target->'reset'='true'::jsonb ORDER BY updated_at DESC,sequence DESC LIMIT 1) reset ON true`, ids)
	if err != nil {
		return nil, unavailable()
	}
	defer rows.Close()
	out := map[uuid.UUID]reminderReset{}
	for rows.Next() {
		var account uuid.UUID
		var r reminderReset
		if rows.Scan(&account, &r.id, &r.raw) != nil {
			return nil, unavailable()
		}
		out[account] = r
	}
	if rows.Err() != nil {
		return nil, unavailable()
	}
	return out, nil
}
func (s *Service) ReminderPeriodTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (notifications.ReminderAccess, error) {
	if tx == nil || id == uuid.Nil {
		return notifications.ReminderAccess{}, failure(400, "INVALID_INPUT")
	}
	a, err := s.accounts.LookupTx(ctx, tx, id)
	if err != nil {
		return notifications.ReminderAccess{}, unavailable()
	}
	bs, err := s.statisticsBaselinesTx(ctx, tx, []uuid.UUID{id})
	if err != nil {
		return notifications.ReminderAccess{}, err
	}
	rs, err := reminderResetsTx(ctx, tx, []uuid.UUID{id})
	if err != nil {
		return notifications.ReminderAccess{}, err
	}
	r := rs[id]
	return reminderPeriod(a, bs[id], s.config().PanelID, r.id, r.raw), nil
}
func (s *Service) ReminderAccessTx(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID]notifications.ReminderAccess, error) {
	if tx == nil {
		return nil, failure(400, "INVALID_INPUT")
	}
	out := map[uuid.UUID]notifications.ReminderAccess{}
	if len(ids) == 0 {
		return out, nil
	}
	as, err := s.accounts.LookupManyTx(ctx, tx, ids)
	if err != nil || len(as) != len(ids) {
		return nil, unavailable()
	}
	bs, err := s.statisticsBaselinesTx(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	rs, err := reminderResetsTx(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	cfg := s.config()
	p := NewPanelClient(cfg.Panel)
	defer p.Close()
	snapshot, err := p.statisticsSnapshot(ctx)
	if err != nil {
		return nil, unavailable()
	}
	observed := s.now().UTC()
	for _, a := range as {
		_, known := statisticsAccountActivity(a, bs[a.ID], snapshot, cfg.PanelID, observed)
		v, exists := snapshot.clients[a.PanelKey]
		if !known || !exists || v.UsedTraffic == nil {
			continue
		}
		r := rs[a.ID]
		facts := reminderPeriod(a, bs[a.ID], cfg.PanelID, r.id, r.raw)
		if facts.Known {
			facts.UsedBytes = *v.UsedTraffic
			facts.ObservedAt = observed
			out[a.ID] = facts
		}
	}
	return out, nil
}
