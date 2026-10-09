package vpn

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/vpn/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type managedAction struct {
	Kind, ID, Name, Host, Channel string
	MaxClients                    int64
}

// Only these public server facts are persisted for an exact replay. The panel
// subscription base and credentials never enter action history.
type managedStoredServer struct {
	ID, Name, Host                   string
	MaxClients                       *int64
	Online, Retired                  bool
	ObservedAt                       *time.Time
	AssignedClients, ReservedClients int64
	Revision                         int64
}
type managedStoredResult struct {
	Server  *managedStoredServer
	Servers []managedStoredServer
}

func managedSnapshot(v Server) managedStoredServer {
	return managedStoredServer{ID: v.ID, Name: v.Name, Host: v.Host, MaxClients: v.MaxClients, Online: v.Online, Retired: v.Retired,
		ObservedAt: v.ObservedAt, AssignedClients: v.AssignedClients, ReservedClients: v.ReservedClients, Revision: v.Revision}
}
func (v managedStoredServer) server() Server {
	return Server{ID: v.ID, Name: v.Name, Host: v.Host, MaxClients: v.MaxClients, Online: v.Online, Retired: v.Retired,
		ObservedAt: v.ObservedAt, AssignedClients: v.AssignedClients, ReservedClients: v.ReservedClients, Revision: v.Revision}
}
func managedSnapshotList(rows []Server) managedStoredResult {
	out := managedStoredResult{Servers: make([]managedStoredServer, 0, len(rows))}
	for _, v := range rows {
		if !v.Retired {
			out.Servers = append(out.Servers, managedSnapshot(v))
		}
	}
	return out
}
func (v managedStoredResult) servers() []Server {
	out := make([]Server, 0, len(v.Servers))
	for _, row := range v.Servers {
		out = append(out, row.server())
	}
	return out
}

func (s *Service) ListManagedServers(ctx context.Context, actor uuid.UUID) ([]Server, error) {
	if err := s.accounts.RequireInfrastructure(ctx, actor); err != nil {
		return nil, err
	}
	rows, err := s.servers(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make([]Server, 0, len(rows))
	for _, row := range rows {
		if !row.Retired {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *Service) GetManagedServer(ctx context.Context, actor uuid.UUID, id string) (Server, error) {
	if err := s.accounts.RequireInfrastructure(ctx, actor); err != nil {
		return Server{}, err
	}
	return s.managedServer(ctx, id)
}

func (s *Service) managedServer(ctx context.Context, id string) (Server, error) {
	if id == "" {
		return Server{}, failure(400, "INVALID_INPUT")
	}
	v, err := s.ServerTx(ctx, nil, id)
	if errors.Is(err, ErrPanel) {
		return Server{}, failure(404, "SERVER_NOT_FOUND")
	}
	return v, err
}

func managedHash(in managedAction) [32]byte {
	raw, _ := json.Marshal(in)
	return sha256.Sum256(raw)
}

func validManagedChannel(channel string) bool { return channel == "web" || channel == "telegram" }

// managedReplayTx serializes this actor/key before checking its stored input.
func managedReplayTx(ctx context.Context, tx pgx.Tx, actor, key uuid.UUID, in managedAction) (managedStoredResult, bool, error) {
	if actor == uuid.Nil || key == uuid.Nil || !validManagedChannel(in.Channel) {
		return managedStoredResult{}, false, failure(400, "INVALID_INPUT")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('vpn-server-action:'||$1::text||':'||$2::text,0))`, actor, key); err != nil {
		return managedStoredResult{}, false, unavailable()
	}
	var previous []byte
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT input_hash,result FROM vpn_server_actions WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(&previous, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return managedStoredResult{}, false, nil
	}
	if err != nil {
		return managedStoredResult{}, false, unavailable()
	}
	hash := managedHash(in)
	if !bytes.Equal(previous, hash[:]) {
		return managedStoredResult{}, false, failure(409, "SERVER_CONFLICT")
	}
	var result managedStoredResult
	if json.Unmarshal(raw, &result) != nil {
		return managedStoredResult{}, false, unavailable()
	}
	return result, true, nil
}

func (s *Service) managedReadReplay(ctx context.Context, actor, key uuid.UUID, in managedAction) (managedStoredResult, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return managedStoredResult{}, false, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = s.accounts.RequireInfrastructureTx(ctx, tx, actor); err != nil {
		return managedStoredResult{}, false, err
	}
	result, replay, err := managedReplayTx(ctx, tx, actor, key, in)
	if err != nil {
		return managedStoredResult{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return managedStoredResult{}, false, unavailable()
	}
	return result, replay, nil
}

func (s *Service) saveManagedActionTx(ctx context.Context, tx pgx.Tx, actor, key uuid.UUID, in managedAction, id string, value managedStoredResult) error {
	hash := managedHash(in)
	result, err := json.Marshal(value)
	if err != nil {
		return unavailable()
	}
	var serverID *string
	if id != "" {
		serverID = &id
	}
	if _, err = tx.Exec(ctx, `INSERT INTO vpn_server_actions(actor_id,idempotency_key,action,input_hash,server_id,result,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, actor, key, in.Kind, hash[:], serverID, result, s.now()); err != nil {
		return unavailable()
	}
	reason := fmt.Sprintf("server_id=%s channel=%s result=success", id, in.Channel)
	if in.Kind == "sync" {
		reason = fmt.Sprintf("channel=%s result=success", in.Channel)
	}
	if err := auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "server." + in.Kind, AccountID: actor, OperatorAccountID: &actor, Reason: &reason}); err != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) CreateManagedServer(ctx context.Context, actor, key uuid.UUID, input ServerInput, channel string) (Server, bool, error) {
	host, err := httpsURL(input.Host, false)
	if err != nil || input.Name == "" || strings.IndexFunc(input.Name, unicode.IsControl) >= 0 || input.MaxClients < 0 || input.MaxClients > 2147483647 {
		return Server{}, false, failure(400, "INVALID_INPUT")
	}
	in := managedAction{Kind: "create", Name: input.Name, Host: host, MaxClients: input.MaxClients, Channel: channel}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Server{}, false, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = s.accounts.RequireInfrastructureTx(ctx, tx, actor); err != nil {
		return Server{}, false, err
	}
	prior, replay, err := managedReplayTx(ctx, tx, actor, key, in)
	if err != nil {
		return Server{}, false, err
	}
	if replay {
		if prior.Server == nil {
			return Server{}, false, unavailable()
		}
		return prior.Server.server(), false, nil
	}
	if err = store.New(tx).LockServerPool(ctx); err != nil {
		return Server{}, false, unavailable()
	}
	input.ID = "srv_" + uuid.NewString()
	input.Host = host
	v, err := s.RegisterServerTx(ctx, tx, input)
	if err != nil {
		return Server{}, false, err
	}
	snap := managedSnapshot(v)
	if err = s.saveManagedActionTx(ctx, tx, actor, key, in, v.ID, managedStoredResult{Server: &snap}); err != nil {
		return Server{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Server{}, false, unavailable()
	}
	return v, true, nil
}

type managedProbe struct {
	server Server
	at     time.Time
	online bool
	base   string
}

func (s *Service) probeManaged(ctx context.Context, server Server) managedProbe {
	p := managedProbe{server: server, at: time.Now().UTC()}
	panel := NewPanelClient(Config{PanelURL: server.Host, PanelToken: s.config().Panel.PanelToken, PanelUsername: s.config().Panel.PanelUsername, PanelPassword: s.config().Panel.PanelPassword, PanelRootCAs: s.config().Panel.PanelRootCAs})
	defer panel.Close()
	_, err := panel.RegularInboundIDs(ctx)
	p.online = err == nil
	if p.online && server.SubscriptionBaseURL == "" {
		p.base, _ = panel.subscriptionBase(ctx)
	}
	return p
}

func (s *Service) PingManagedServer(ctx context.Context, actor uuid.UUID, id string, key uuid.UUID, channel string) (Server, error) {
	in := managedAction{Kind: "ping", ID: id, Channel: channel}
	prior, replay, err := s.managedReadReplay(ctx, actor, key, in)
	if err != nil {
		return Server{}, err
	}
	if replay {
		if prior.Server == nil {
			return Server{}, unavailable()
		}
		return prior.Server.server(), nil
	}
	v, err := s.managedServer(ctx, id)
	if err != nil {
		return Server{}, err
	}
	probe := s.probeManaged(ctx, v)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Server{}, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = s.accounts.RequireInfrastructureTx(ctx, tx, actor); err != nil {
		return Server{}, err
	}
	prior, replay, err = managedReplayTx(ctx, tx, actor, key, in)
	if err != nil {
		return Server{}, err
	}
	if replay {
		if prior.Server == nil {
			return Server{}, unavailable()
		}
		return prior.Server.server(), nil
	}
	// A read-only legacy fallback has revision zero. Materialize it in the
	// same transaction as the observation, action result, and audit event.
	if v.Revision == 0 {
		if err = s.seedPrimaryTx(ctx, tx); err != nil {
			return Server{}, err
		}
		v.Revision = 1
	}
	if err = store.New(tx).ObservePoolServer(ctx, store.ObservePoolServerParams{ID: id, Revision: v.Revision, ObservedAt: pgtype.Timestamptz{Time: probe.at, Valid: true}, Online: probe.online, Base: probe.base}); err != nil {
		return Server{}, unavailable()
	}
	rows, err := s.ServersTx(ctx, tx)
	if err != nil {
		return Server{}, err
	}
	for _, row := range rows {
		if row.ID == id && !row.Retired {
			snap := managedSnapshot(row)
			if err = s.saveManagedActionTx(ctx, tx, actor, key, in, id, managedStoredResult{Server: &snap}); err != nil {
				return Server{}, err
			}
			if err = tx.Commit(ctx); err != nil {
				return Server{}, unavailable()
			}
			return row, nil
		}
	}
	return Server{}, failure(404, "SERVER_NOT_FOUND")
}

func (s *Service) SyncManagedServers(ctx context.Context, actor, key uuid.UUID, channel string) ([]Server, error) {
	in := managedAction{Kind: "sync", Channel: channel}
	prior, replay, err := s.managedReadReplay(ctx, actor, key, in)
	if err != nil {
		return nil, err
	}
	if replay {
		return prior.servers(), nil
	}
	rows, err := s.servers(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	active := make([]Server, 0, len(rows))
	for _, row := range rows {
		if !row.Retired {
			active = append(active, row)
		}
	}
	// Panel requests have their own 10 second timeout. Probe at most four
	// panels at once and reserve up to two seconds of the caller's deadline
	// for the transaction that persists all observations and the audit.
	probeCtx := ctx
	cancel := func() {}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		reserve := remaining / 5
		if reserve > 2*time.Second {
			reserve = 2 * time.Second
		}
		probeCtx, cancel = context.WithDeadline(ctx, deadline.Add(-reserve))
	} else {
		probeCtx, cancel = context.WithTimeout(ctx, 13*time.Second)
	}
	defer cancel()
	probes := make([]managedProbe, len(active))
	if len(active) != 0 {
		jobs := make(chan int)
		var workers sync.WaitGroup
		for i := 0; i < len(active) && i < 4; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for index := range jobs {
					probes[index] = s.probeManaged(probeCtx, active[index])
				}
			}()
		}
	dispatch:
		for i := range active {
			select {
			case jobs <- i:
			case <-probeCtx.Done():
				break dispatch
			}
		}
		close(jobs)
		workers.Wait()
	}
	if probeCtx.Err() != nil || ctx.Err() != nil {
		return nil, unavailable()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = s.accounts.RequireInfrastructureTx(ctx, tx, actor); err != nil {
		return nil, err
	}
	prior, replay, err = managedReplayTx(ctx, tx, actor, key, in)
	if err != nil {
		return nil, err
	}
	if replay {
		return prior.servers(), nil
	}
	if !replay {
		if err = s.seedPrimaryTx(ctx, tx); err != nil {
			return nil, err
		}
		q := store.New(tx)
		for _, probe := range probes {
			if probe.server.Revision == 0 {
				probe.server.Revision = 1
			}
			if err = q.ObservePoolServer(ctx, store.ObservePoolServerParams{ID: probe.server.ID, Revision: probe.server.Revision, ObservedAt: pgtype.Timestamptz{Time: probe.at, Valid: true}, Online: probe.online, Base: probe.base}); err != nil {
				return nil, unavailable()
			}
		}
	}
	rows, err = s.ServersTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := make([]Server, 0, len(rows))
	for _, row := range rows {
		if !row.Retired {
			out = append(out, row)
		}
	}
	if err = s.saveManagedActionTx(ctx, tx, actor, key, in, "", managedSnapshotList(out)); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, unavailable()
	}
	return out, nil
}

func (s *Service) DeleteManagedServer(ctx context.Context, actor uuid.UUID, id string, key uuid.UUID, channel string) (Server, error) {
	in := managedAction{Kind: "delete", ID: id, Channel: channel}
	preflight, err := s.pool.Begin(ctx)
	if err != nil {
		return Server{}, unavailable()
	}
	defer preflight.Rollback(ctx)
	if err = s.accounts.RequireInfrastructureTx(ctx, preflight, actor); err != nil {
		return Server{}, err
	}
	prior, replay, err := managedReplayTx(ctx, preflight, actor, key, in)
	if err != nil {
		return Server{}, err
	}
	if replay {
		if prior.Server == nil {
			return Server{}, unavailable()
		}
		return prior.Server.server(), nil
	}
	if err = store.New(preflight).LockServerPool(ctx); err != nil {
		return Server{}, unavailable()
	}
	rows, err := s.ServersTx(ctx, preflight)
	if err != nil {
		return Server{}, err
	}
	var v Server
	found := false
	for _, row := range rows {
		if row.ID == id && !row.Retired {
			v, found = row, true
			break
		}
	}
	if !found {
		return Server{}, failure(404, "SERVER_NOT_FOUND")
	}
	busy, err := s.managedServerBusyTx(ctx, preflight, id)
	if err != nil {
		return Server{}, err
	}
	if busy {
		return Server{}, failure(409, "SERVER_IN_USE")
	}
	if err = preflight.Commit(ctx); err != nil {
		return Server{}, unavailable()
	}
	panel, err := s.PanelFor(ctx, id)
	if err != nil {
		return Server{}, unavailable()
	}
	view, err := panel.statisticsSnapshot(ctx)
	panel.Close()
	if err != nil {
		return Server{}, unavailable()
	}
	if len(view.clients) != 0 {
		return Server{}, failure(409, "SERVER_IN_USE")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Server{}, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = s.accounts.RequireInfrastructureTx(ctx, tx, actor); err != nil {
		return Server{}, err
	}
	prior, replay, err = managedReplayTx(ctx, tx, actor, key, in)
	if err != nil {
		return Server{}, err
	}
	if err = store.New(tx).LockServerPool(ctx); err != nil {
		return Server{}, unavailable()
	}
	// The first delete may have read the synthetic primary fallback. Seed it
	// only here, so provider failure cannot commit a registry fact without
	// the matching tombstone, idempotency result, and audit event.
	if v.Revision == 0 {
		if err = s.seedPrimaryTx(ctx, tx); err != nil {
			return Server{}, err
		}
		v.Revision = 1
	}
	rows, err = s.ServersTx(ctx, tx)
	if err != nil {
		return Server{}, err
	}
	var current Server
	found = false
	for _, row := range rows {
		if row.ID == id {
			current, found = row, true
			break
		}
	}
	if !found {
		return Server{}, failure(404, "SERVER_NOT_FOUND")
	}
	if replay {
		if prior.Server == nil {
			return Server{}, unavailable()
		}
		return prior.Server.server(), nil
	}
	if current.Retired {
		return Server{}, failure(404, "SERVER_NOT_FOUND")
	}
	if current.Revision != v.Revision {
		return Server{}, failure(409, "SERVER_CONFLICT")
	}
	busy, err = s.managedServerBusyTx(ctx, tx, id)
	if err != nil {
		return Server{}, err
	}
	if busy {
		return Server{}, failure(409, "SERVER_IN_USE")
	}
	updated, err := tx.Exec(ctx, `UPDATE vpn_servers SET retired=true,online=false,revision=revision+1 WHERE id=$1 AND NOT retired`, id)
	if err != nil {
		return Server{}, unavailable()
	}
	if updated.RowsAffected() != 1 {
		return Server{}, failure(409, "SERVER_CONFLICT")
	}
	current.Retired, current.Online, current.Revision = true, false, current.Revision+1
	snap := managedSnapshot(current)
	if err = s.saveManagedActionTx(ctx, tx, actor, key, in, id, managedStoredResult{Server: &snap}); err != nil {
		return Server{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Server{}, unavailable()
	}
	return current, nil
}

func (s *Service) managedServerBusyTx(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	loads, err := s.accounts.PanelLoadsTx(ctx, tx)
	if err != nil {
		return false, err
	}
	if loads[id] > 0 {
		return true, nil
	}
	busy, err := store.New(tx).ManagedServerBusy(ctx, id)
	if err != nil {
		return false, unavailable()
	}
	if !busy.Valid {
		return false, unavailable()
	}
	return busy.Bool, nil
}
