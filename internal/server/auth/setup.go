package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
)

// The setup code closes the window between "the panel is up" and "an administrator exists":
// the installer publishes port 8080 on every interface, and without it whoever reached the
// panel first would become its administrator. The code is printed by the installer, in the
// server log and by `slowpoke-server setup-code`, which is to say only to someone who can
// already read the server.
const setupCodeKey = "setup_code"

// No 0/O, no 1/I: it is read off a terminal and typed by hand.
const setupAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// SetupCode returns the code that creating the first administrator needs, making one the
// first time. Every replica and the CLI read the same row, so they all print the same code.
func (s Service) SetupCode(ctx context.Context) (string, error) {
	if code, err := s.storedSetupCode(ctx); err != nil || code != "" {
		return code, err
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, c := range raw {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(setupAlphabet[int(c)%len(setupAlphabet)])
	}
	// Two replicas starting together: the first insert wins and both return what is stored.
	if _, err := s.DB.Q().Exec(ctx, "INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO NOTHING",
		setupCodeKey, b.String()); err != nil {
		return "", err
	}
	return s.storedSetupCode(ctx)
}

// CheckSetupCode accepts the code however it was typed: lower case, spaces, no dashes.
func (s Service) CheckSetupCode(ctx context.Context, typed string) bool {
	want, err := s.storedSetupCode(ctx)
	if err != nil || want == "" {
		return false
	}
	got := normalizeSetupCode(typed)
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(normalizeSetupCode(want))) == 1
}

// ClearSetupCode forgets the code once the administrator exists: it opens nothing any more.
func (s Service) ClearSetupCode(ctx context.Context) error {
	_, err := s.DB.Q().Exec(ctx, "DELETE FROM app_settings WHERE key = ?", setupCodeKey)
	return err
}

func (s Service) storedSetupCode(ctx context.Context) (string, error) {
	var code string
	err := s.DB.Q().Row(ctx, "SELECT value FROM app_settings WHERE key = ?", setupCodeKey).Scan(&code)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return code, err
}

func normalizeSetupCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if strings.ContainsRune(setupAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
