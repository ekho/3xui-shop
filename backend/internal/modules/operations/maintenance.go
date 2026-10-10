package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/operations/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Maintenance struct {
	pool     *pgxpool.Pool
	accounts *accounts.Service
	now      func() time.Time
}

type Status struct {
	Enabled   bool       `json:"enabled"`
	Revision  int64      `json:"revision"`
	ChangedAt *time.Time `json:"changed_at"`
}

type Input struct {
	Enabled          bool   `json:"enabled"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
	Confirmed        bool   `json:"confirmed"`
}

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string            { return e.Code }
func fault(status int, code string) error { return &Error{Status: status, Code: code} }
func unavailable() error                  { return fault(503, "SERVICE_UNAVAILABLE") }

func New(pool *pgxpool.Pool, authority *accounts.Service, now func() time.Time) *Maintenance {
	if now == nil {
		now = time.Now
	}
	return &Maintenance{pool: pool, accounts: authority, now: now}
}

func publicStatus(row store.MaintenanceStatusRow) Status {
	out := Status{Enabled: row.Enabled, Revision: row.Revision}
	if row.ChangedAt.Valid {
		out.ChangedAt = &row.ChangedAt.Time
	}
	return out
}

func (m *Maintenance) Status(ctx context.Context) (Status, error) {
	return m.status(ctx, nil)
}

func (m *Maintenance) status(ctx context.Context, tx pgx.Tx) (Status, error) {
	var db store.DBTX = m.pool
	if tx != nil {
		db = tx
	}
	row, err := store.New(db).MaintenanceStatus(ctx)
	if err != nil {
		return Status{}, unavailable()
	}
	return publicStatus(row), nil
}

// AllowNew reads shared state on the caller's connection; existing work and money callbacks never call it.
func (m *Maintenance) AllowNew(ctx context.Context, tx pgx.Tx) error {
	status, err := m.status(ctx, tx)
	if err != nil {
		return err
	}
	if status.Enabled {
		return fault(503, "MAINTENANCE")
	}
	return nil
}

func (m *Maintenance) Set(ctx context.Context, actor, key uuid.UUID, in Input) (Status, error) {
	if m.accounts == nil || actor == uuid.Nil || key == uuid.Nil || !in.Confirmed || in.ExpectedRevision < 0 || !utf8.ValidString(in.Reason) || utf8.RuneCountInString(in.Reason) < 1 || utf8.RuneCountInString(in.Reason) > 1000 || strings.ContainsRune(in.Reason, '\x00') {
		return Status{}, fault(400, "INVALID_INPUT")
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return Status{}, unavailable()
	}
	hash := sha256.Sum256(encoded)
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return Status{}, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = m.accounts.LockOperatorPair(ctx, tx, actor, actor); err != nil {
		var domain *accounts.Error
		if errors.As(err, &domain) {
			return Status{}, fault(domain.Status, domain.Code)
		}
		return Status{}, unavailable()
	}
	var savedHash, savedResult []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,result FROM maintenance_commands WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(&savedHash, &savedResult)
	if err == nil {
		if !bytes.Equal(hash[:], savedHash) {
			return Status{}, fault(409, "IDEMPOTENCY_CONFLICT")
		}
		var saved Status
		if json.Unmarshal(savedResult, &saved) != nil {
			return Status{}, unavailable()
		}
		return saved, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Status{}, unavailable()
	}
	var out Status
	if err = tx.QueryRow(ctx, `SELECT enabled,revision,changed_at FROM maintenance_state WHERE singleton=true FOR UPDATE`).Scan(&out.Enabled, &out.Revision, &out.ChangedAt); err != nil {
		return Status{}, unavailable()
	}
	if in.ExpectedRevision != out.Revision {
		return Status{}, fault(409, "MAINTENANCE_CONFLICT")
	}
	if in.Enabled != out.Enabled {
		out.Enabled, out.Revision = in.Enabled, out.Revision+1
		changed := m.now().UTC().Truncate(time.Microsecond)
		out.ChangedAt = &changed
		if _, err = tx.Exec(ctx, `UPDATE maintenance_state SET enabled=$1,revision=$2,changed_at=$3 WHERE singleton=true`, out.Enabled, out.Revision, changed); err != nil {
			return Status{}, unavailable()
		}
		action := "maintenance.disabled"
		if out.Enabled {
			action = "maintenance.enabled"
		}
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: actor, OperatorAccountID: &actor, CreatedAt: changed, Action: action, Reason: &in.Reason}); err != nil {
			return Status{}, unavailable()
		}
	}
	result, err := json.Marshal(out)
	if err != nil {
		return Status{}, unavailable()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO maintenance_commands(actor_id,idempotency_key,request_hash,request,result,created_at) VALUES($1,$2,$3,$4,$5,$6)`, actor, key, hash[:], encoded, result, m.now().UTC()); err != nil {
		return Status{}, unavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return Status{}, unavailable()
	}
	return out, nil
}
