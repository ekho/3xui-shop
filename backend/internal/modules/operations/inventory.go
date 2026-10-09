package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

func publicNames(ctx context.Context, tx pgx.Tx, kinds string) ([]string, error) {
	filter := `c.relkind IN ('r','p')`
	if kinds == "S" {
		filter = `c.relkind='S'`
	}
	rows, err := tx.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND `+filter+` ORDER BY c.relname`)
	if err != nil {
		return nil, errors.New("BACKUP_INCONSISTENT")
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			return nil, errors.New("BACKUP_INCONSISTENT")
		}
		names = append(names, name)
	}
	if rows.Err() != nil {
		return nil, errors.New("BACKUP_INCONSISTENT")
	}
	return names, nil
}

func inventoryTables(ctx context.Context, tx pgx.Tx) ([]tableInventory, error) {
	names, err := publicNames(ctx, tx, "rp")
	if err != nil || len(names) == 0 {
		return nil, errors.New("BACKUP_INCONSISTENT")
	}
	out := make([]tableInventory, 0, len(names))
	for _, name := range names {
		h := sha256.New()
		q := `COPY (SELECT to_jsonb(t)::text FROM ` + pgx.Identifier{"public", name}.Sanitize() + ` t ORDER BY to_jsonb(t)::text COLLATE "C") TO STDOUT`
		tag, err := tx.Conn().PgConn().CopyTo(ctx, h, q)
		if err != nil {
			return nil, errors.New("BACKUP_INCONSISTENT")
		}
		out = append(out, tableInventory{Name: name, Count: tag.RowsAffected(), SHA256: hex.EncodeToString(h.Sum(nil))})
	}
	return out, nil
}

func inventorySequences(ctx context.Context, tx pgx.Tx) ([]sequenceInventory, error) {
	names, err := publicNames(ctx, tx, "S")
	if err != nil {
		return nil, err
	}
	out := make([]sequenceInventory, 0, len(names))
	for _, name := range names {
		var last int64
		var called bool
		q := `SELECT last_value,is_called FROM ` + pgx.Identifier{"public", name}.Sanitize()
		if tx.QueryRow(ctx, q).Scan(&last, &called) != nil {
			return nil, errors.New("BACKUP_INCONSISTENT")
		}
		out = append(out, sequenceInventory{Name: name, Last: last, IsCalled: called})
	}
	return out, nil
}

func lockTables(ctx context.Context, tx pgx.Tx, names []string) error {
	if len(names) == 0 {
		return errors.New("BACKUP_INCONSISTENT")
	}
	var qualified []string
	for _, name := range names {
		qualified = append(qualified, pgx.Identifier{"public", name}.Sanitize())
	}
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
		return errors.New("BACKUP_INCONSISTENT")
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE `+strings.Join(qualified, ",")+` IN SHARE MODE`); err != nil {
		return errors.New("BACKUP_INCONSISTENT")
	}
	return nil
}

func setInventorySession(ctx context.Context, tx pgx.Tx) error {
	for _, q := range []string{`SET LOCAL TIME ZONE 'UTC'`, `SET LOCAL DateStyle TO ISO`, `SET LOCAL extra_float_digits TO 3`} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return errors.New("BACKUP_INCONSISTENT")
		}
	}
	return nil
}
