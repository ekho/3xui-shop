package vpn

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrBusy = errors.New("account access busy")

type AccessOwner struct {
	conn    *pgxpool.Conn
	account uuid.UUID
	locked  bool
}

func (s *Service) OpenAccessOwner(ctx context.Context, account uuid.UUID) (*AccessOwner, error) {
	c, e := s.pool.Acquire(ctx)
	if e != nil {
		return nil, e
	}
	return &AccessOwner{conn: c, account: account}, nil
}
func (o *AccessOwner) Begin(ctx context.Context) (pgx.Tx, error) { return o.conn.Begin(ctx) }
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
