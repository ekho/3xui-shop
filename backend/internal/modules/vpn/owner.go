package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrBusy = errors.New("account access busy")

type AccessOwner struct {
	service *Service
	conn    *pgxpool.Conn
	account uuid.UUID
	locked  bool
}

func (s *Service) OpenAccessOwner(ctx context.Context, account uuid.UUID) (*AccessOwner, error) {
	c, e := s.pool.Acquire(ctx)
	if e != nil {
		return nil, e
	}
	return &AccessOwner{service: s, conn: c, account: account}, nil
}
func (o *AccessOwner) Begin(ctx context.Context) (pgx.Tx, error) { return o.conn.Begin(ctx) }

// Reuse the owned session; a one-connection pool cannot lend a second one.
func (o *AccessOwner) PanelFor(ctx context.Context, id string) (*PanelClient, error) {
	return o.service.panelFor(ctx, id, o.conn)
}
func (o *AccessOwner) AvailableServer(ctx context.Context) (Server, error) {
	return o.service.availableServer(ctx, o.conn)
}
func (o *AccessOwner) TryLock(ctx context.Context) error {
	if o.locked {
		return nil
	}
	if e := o.conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('account-access:'||$1::text,0))`, o.account).Scan(&o.locked); e != nil {
		return e
	}
	if !o.locked {
		return ErrBusy
	}
	return nil
}
func (o *AccessOwner) Release() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// The dedicated session owns account-access and, for commands, idempotency.
	if _, e := o.conn.Exec(ctx, "SELECT pg_advisory_unlock_all()"); e != nil {
		o.conn.Conn().Close(ctx)
	}
	o.conn.Release()
}

// RetirePurchaseAccessTx stops future attempts only. Completed steps and the
// frozen target remain evidence of any partial write; live access is untouched.
func (s *Service) RetirePurchaseAccessTx(ctx context.Context, tx pgx.Tx, owner *AccessOwner, account, order, operation uuid.UUID) error {
	if tx == nil || owner == nil || !owner.locked || owner.account != account || tx.Conn() != owner.conn.Conn() || account == uuid.Nil || order == uuid.Nil || operation == uuid.Nil {
		return ErrIdentity
	}
	var status string
	var write, reset bool
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT status,write_started,reset_started,target FROM access_operations WHERE id=$1 AND account_id=$2 AND kind='purchase' AND purchase_order_id=$3 FOR UPDATE`, operation, account, order).Scan(&status, &write, &reset, &raw); err != nil {
		return err
	}
	if status == "applied" || status == "skipped" {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE access_operations SET status='skipped',lease_hash=NULL,lease_expires_at=NULL,review_reason='funding_refunded',updated_at=$2 WHERE id=$1`, operation, s.now())
	if err == nil && !write && !reset {
		var target AccessTarget
		if json.Unmarshal(raw, &target) != nil || target.OperationID != operation {
			return ErrIdentity
		}
		err = s.ReleaseServerReservationTx(ctx, tx, account, target.PanelID)
	}
	return err
}
