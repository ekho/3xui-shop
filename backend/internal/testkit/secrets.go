package testkit

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func MailSecrets(t *testing.T, pool *pgxpool.Pool, key []byte, challenge uuid.UUID) (uuid.UUID, string, string) {
	return mailSecrets(t, pool, key, challenge, false)
}
func CredentialMailSecrets(t *testing.T, pool *pgxpool.Pool, key []byte, challenge uuid.UUID) (uuid.UUID, string, string) {
	return mailSecrets(t, pool, key, challenge, true)
}
func mailSecrets(t *testing.T, pool *pgxpool.Pool, key []byte, challenge uuid.UUID, credential bool) (uuid.UUID, string, string) {
	t.Helper()
	var id uuid.UUID
	var encrypted []byte
	query := `SELECT id,ciphertext FROM mail_deliveries WHERE challenge_id=$1`
	if credential {
		query = `SELECT id,ciphertext FROM mail_deliveries WHERE credential_challenge_id=$1`
	}
	if err := pool.QueryRow(context.Background(), query, challenge).Scan(&id, &encrypted); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	if len(encrypted) < gcm.NonceSize() {
		t.Fatal("mail payload missing")
	}
	plain, err := gcm.Open(nil, encrypted[:gcm.NonceSize()], encrypted[gcm.NonceSize():], []byte(id.String()))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ Token, Code string }
	if json.Unmarshal(plain, &payload) != nil {
		t.Fatal("invalid mail payload")
	}
	return id, payload.Token, payload.Code
}
