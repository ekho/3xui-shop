package support

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type BackupAttachment struct {
	ID     uuid.UUID `json:"id"`
	Size   int64     `json:"size"`
	SHA256 string    `json:"sha256"`
}

// BackupInventory reads the caller's exported snapshot and refuses remote-only media.
func BackupInventory(ctx context.Context, tx pgx.Tx) ([]BackupAttachment, error) {
	rows, err := tx.Query(ctx, `SELECT id, attachment_bytes, COALESCE(telegram_only,false) FROM support_messages ORDER BY id`)
	if err != nil {
		return nil, errors.New("BACKUP_INCOMPLETE")
	}
	defer rows.Close()
	attachments := make([]BackupAttachment, 0)
	for rows.Next() {
		var id uuid.UUID
		var b []byte
		var remoteOnly bool
		if rows.Scan(&id, &b, &remoteOnly) != nil || remoteOnly {
			return nil, errors.New("BACKUP_INCOMPLETE")
		}
		if b != nil {
			sum := sha256.Sum256(b)
			attachments = append(attachments, BackupAttachment{ID: id, Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])})
		}
	}
	if rows.Err() != nil {
		return nil, errors.New("BACKUP_INCOMPLETE")
	}
	return attachments, nil
}
