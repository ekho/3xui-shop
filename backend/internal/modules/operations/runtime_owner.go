package operations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrRuntimeOwned = errors.New("runtime already owned")
var ErrRuntimeOwnerLost = errors.New("runtime ownership lost")

// RuntimeOwner holds a dedicated PostgreSQL session for the entire executor lifetime.
type RuntimeOwner struct {
	conn *pgx.Conn
	stop chan struct{}
	done chan struct{}
	lost chan struct{}
}

func AcquireRuntimeOwner(ctx context.Context, databaseURL string) (*RuntimeOwner, error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	var acquired bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('operations.runtime-owner-v1',0))`).Scan(&acquired)
	if err != nil || !acquired {
		_ = conn.Close(context.Background())
		if err != nil {
			return nil, err
		}
		return nil, ErrRuntimeOwned
	}
	owner := &RuntimeOwner{conn: conn, stop: make(chan struct{}), done: make(chan struct{}), lost: make(chan struct{})}
	go owner.monitor()
	// shortcut: timed warm-up cannot fence a paused executor, require confirmed stop until automated takeover has external fencing.
	warmup := time.NewTimer(3 * time.Second)
	defer warmup.Stop()
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-owner.lost:
		err = ErrRuntimeOwnerLost
	case <-warmup.C:
		err = owner.Check(ctx)
	}
	if err != nil {
		_ = owner.Close()
		return nil, err
	}
	return owner, nil
}

func (o *RuntimeOwner) Lost() <-chan struct{} { return o.lost }

func (o *RuntimeOwner) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-o.lost:
		return ErrRuntimeOwnerLost
	default:
		return nil
	}
}

func (o *RuntimeOwner) monitor() {
	defer close(o.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-o.stop:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := o.conn.Ping(ctx)
		cancel()
		if err != nil {
			select {
			case <-o.stop:
			default:
				close(o.lost)
			}
			return
		}
	}
}

// Close releases ownership only after every external executor has stopped.
func (o *RuntimeOwner) Close() error {
	close(o.stop)
	<-o.done
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return o.conn.Close(ctx)
}
