package platform

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"fmt"
	"golang.org/x/crypto/argon2"
	"strings"
	"unicode/utf8"
)

//go:embed passwords.txt
var passwordList string
var commonPasswords = func() map[string]struct{} {
	m := make(map[string]struct{})
	for _, p := range strings.Split(passwordList, "\n") {
		m[strings.TrimSuffix(p, "\r")] = struct{}{}
	}
	return m
}()

func validatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	_, common := commonPasswords[pw]
	if !utf8.ValidString(pw) || n < 15 || n > 128 {
		return failure(400, "INVALID_INPUT")
	}
	if common {
		return failure(400, "COMMON_PASSWORD")
	}
	return nil
}
func (s *Service) hashPassword(ctx context.Context, pw string) (string, error) {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	case <-ctx.Done():
		return "", unavailable()
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	hash := argon2.IDKey([]byte(pw), salt, 2, 19456, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}
func (s *Service) checkPassword(ctx context.Context, pw, encoded string) (bool, error) {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	case <-ctx.Done():
		return false, unavailable()
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return false, unavailable()
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[4])
	if e != nil || len(salt) != 16 {
		return false, unavailable()
	}
	expected, e := base64.RawStdEncoding.DecodeString(parts[5])
	if e != nil || len(expected) != 32 {
		return false, unavailable()
	}
	hash := argon2.IDKey([]byte(pw), salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(hash, expected) == 1, nil
}
