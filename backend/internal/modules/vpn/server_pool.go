package vpn

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/vpn/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Server struct {
	ID, Name, Host, SubscriptionBaseURL string
	MaxClients                          *int64
	Online, Retired                     bool
	Revision                            int64
	ObservedAt                          *time.Time
	AssignedClients, ReservedClients    int64
}

type ServerInput struct {
	ID, Name, Host string
	MaxClients     int64
}

var serverID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (s *Service) RegisterServerTx(ctx context.Context, tx pgx.Tx, in ServerInput) (Server, error) {
	host, err := httpsURL(in.Host, false)
	if err != nil || !serverID.MatchString(in.ID) || !utf8.ValidString(in.Name) || strings.TrimSpace(in.Name) != in.Name || utf8.RuneCountInString(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 100 || in.MaxClients < 0 {
		return Server{}, failure(400, "INVALID_INPUT")
	}
	q := store.New(tx)
	if q.LockServerPool(ctx) != nil {
		return Server{}, unavailable()
	}
	_, err = q.RegisterPoolServer(ctx, store.RegisterPoolServerParams{ID: in.ID, Name: in.Name, Host: host, MaxClients: pgtype.Int8{Int64: in.MaxClients, Valid: true}})
	if err != nil {
		return Server{}, failure(409, "SERVER_CONFLICT")
	}
	return s.ServerTx(ctx, tx, in.ID)
}

func (s *Service) ServersTx(ctx context.Context, tx pgx.Tx) ([]Server, error) {
	rows, err := store.New(tx).PoolServers(ctx)
	if err != nil {
		return nil, unavailable()
	}
	loads, err := s.accounts.PanelLoadsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := make([]Server, 0, len(rows))
	for _, row := range rows {
		v := Server{ID: row.ID, Name: row.Name, Host: row.Host, SubscriptionBaseURL: row.SubscriptionBaseUrl, Online: row.Online, Retired: row.Retired, Revision: row.Revision, AssignedClients: loads[row.ID], ReservedClients: row.ReservedClients}
		if row.MaxClients.Valid {
			n := row.MaxClients.Int64
			v.MaxClients = &n
		}
		if row.ObservedAt.Valid {
			at := row.ObservedAt.Time
			v.ObservedAt = &at
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		cfg := s.config()
		host, err := httpsURL(cfg.Panel.PanelURL, false)
		if serverID.MatchString(cfg.PanelID) && err == nil {
			base, _ := httpsURL(cfg.SubscriptionBaseURL, true)
			out = append(out, Server{ID: cfg.PanelID, Name: cfg.PanelID, Host: host, SubscriptionBaseURL: base, AssignedClients: loads[cfg.PanelID]})
		}
	}
	return out, nil
}

func (s *Service) ServerTx(ctx context.Context, tx pgx.Tx, id string) (Server, error) {
	rows, err := s.ServersTx(ctx, tx)
	if err != nil {
		return Server{}, err
	}
	for _, v := range rows {
		if v.ID == id && !v.Retired {
			return v, nil
		}
	}
	return Server{}, ErrPanel
}

func (s *Service) servers(ctx context.Context) ([]Server, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, unavailable()
	}
	defer tx.Rollback(ctx)
	rows, err := s.ServersTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, unavailable()
	}
	return rows, nil
}

func (s *Service) PanelFor(ctx context.Context, id string) (*PanelClient, error) {
	rows, err := s.servers(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range rows {
		if v.ID == id && !v.Retired {
			cfg := s.config().Panel
			cfg.PanelURL = v.Host
			return NewPanelClient(cfg), nil
		}
	}
	return nil, ErrPanel
}

func (s *Service) SubscriptionBase(ctx context.Context, id string) (string, error) {
	rows, err := s.servers(ctx)
	if err != nil {
		return "", err
	}
	for _, v := range rows {
		if v.ID == id && !v.Retired {
			return httpsURL(v.SubscriptionBaseURL, true)
		}
	}
	return "", ErrPanel
}

func (s *Service) seedPrimary(ctx context.Context) error {
	cfg := s.config()
	host, err := httpsURL(cfg.Panel.PanelURL, false)
	if err != nil || !serverID.MatchString(cfg.PanelID) {
		return ErrPanel
	}
	base := ""
	if cfg.SubscriptionBaseURL != "" {
		base, err = httpsURL(cfg.SubscriptionBaseURL, true)
		if err != nil {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if q.LockServerPool(ctx) != nil || q.SeedPrimaryServer(ctx, store.SeedPrimaryServerParams{ID: cfg.PanelID, Name: cfg.PanelID, Host: host, SubscriptionBaseUrl: base}) != nil {
		return unavailable()
	}
	return tx.Commit(ctx)
}

func (s *Service) SyncServers(ctx context.Context) error {
	if err := s.seedPrimary(ctx); err != nil {
		return err
	}
	rows, err := s.servers(ctx)
	if err != nil {
		return err
	}
	for _, v := range rows {
		if v.Retired {
			continue
		}
		// Probe ordering uses wall time even when the business clock is frozen.
		started := time.Now().UTC()
		cfg := s.config().Panel
		cfg.PanelURL = v.Host
		panel := NewPanelClient(cfg)
		_, probeErr := panel.RegularInboundIDs(ctx)
		base := ""
		if probeErr == nil && v.SubscriptionBaseURL == "" {
			base, _ = panel.subscriptionBase(ctx)
		}
		panel.Close()
		if err = store.New(s.pool).ObservePoolServer(ctx, store.ObservePoolServerParams{ID: v.ID, Revision: v.Revision, ObservedAt: pgtype.Timestamptz{Time: started, Valid: true}, Online: probeErr == nil, Base: base}); err != nil {
			return unavailable()
		}
	}
	return nil
}

func chooseServer(rows []Server) (Server, error) {
	var best *Server
	for _, v := range rows {
		if !v.Online || v.Retired {
			continue
		}
		if _, err := httpsURL(v.SubscriptionBaseURL, true); err != nil {
			continue
		}
		load := v.AssignedClients + v.ReservedClients
		free := v.MaxClients == nil || load < *v.MaxClients
		if best == nil {
			copy := v
			best = &copy
			continue
		}
		bestLoad := best.AssignedClients + best.ReservedClients
		bestFree := best.MaxClients == nil || bestLoad < *best.MaxClients
		if free && !bestFree || free == bestFree && (load < bestLoad || load == bestLoad && v.ID < best.ID) {
			copy := v
			best = &copy
		}
	}
	if best == nil {
		return Server{}, ErrPanel
	}
	return *best, nil
}

func (s *Service) AvailableServer(ctx context.Context) (Server, error) {
	if err := s.SyncServers(ctx); err != nil {
		return Server{}, err
	}
	rows, err := s.servers(ctx)
	if err != nil {
		return Server{}, err
	}
	return chooseServer(rows)
}
