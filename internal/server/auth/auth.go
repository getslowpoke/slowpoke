// Package auth manages users and browser sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

const SessionTTL = 30 * 24 * time.Hour

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrNoSession          = errors.New("no valid session")
)

type User struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Service struct {
	DB  *store.DB
	Now func() time.Time
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// MinPasswordLength is the only rule Slowpoke has about passwords: length beats cleverness, and a
// rule nobody can satisfy ends up on a sticky note.
const MinPasswordLength = 10

func CheckPassword(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("the password needs at least %d characters", MinPasswordLength)
	}
	return nil
}

func (s Service) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.Q().Row(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

func (s Service) CreateUser(ctx context.Context, name, email, password string) (User, error) {
	email = normalizeEmail(email)
	if _, err := mail.ParseAddress(email); err != nil || !strings.Contains(email, "@") {
		return User{}, errors.New("invalid email")
	}
	if err := CheckPassword(password); err != nil {
		return User{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = strings.Split(email, "@")[0]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	var exists int
	if err := s.DB.Q().Row(ctx, "SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&exists); err != nil {
		return User{}, err
	}
	if exists > 0 {
		return User{}, errors.New("a user with this email already exists")
	}
	id, err := s.DB.Q().InsertID(ctx, "INSERT INTO users (email, name, password_hash, created_at) VALUES (?, ?, ?, ?)", email, strings.TrimSpace(name), string(hash), s.now().Unix())
	return User{ID: id, Name: strings.TrimSpace(name), Email: email}, err
}

// A valid hash of a random password: comparing against it keeps the timing of unknown emails the same.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("slowpoke-timing-equalizer"), bcrypt.DefaultCost)

func (s Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	var u User
	var hash string
	err := s.DB.Q().Row(ctx, "SELECT id, name, email, password_hash FROM users WHERE email = ?", normalizeEmail(email)).Scan(&u.ID, &u.Name, &u.Email, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s Service) CreateSession(ctx context.Context, userID int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	_, err := s.DB.Q().Exec(ctx, "INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)", hashToken(token), userID, s.now().Add(SessionTTL).Unix())
	return token, err
}

// UserByEmail finds a person by their address, for the commands that run without a browser.
func (s Service) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.DB.Q().Row(ctx, "SELECT id, name, email FROM users WHERE email = ?", normalizeEmail(email)).Scan(&u.ID, &u.Name, &u.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("no user with address %s", strings.TrimSpace(email))
	}
	return u, err
}

// SetPassword replaces somebody's password and closes every session they have open, because a
// password is usually changed when the old one is in the wrong hands. It runs on the machine, as
// the way back in for a person locked out of a panel that cannot send email.
func (s Service) SetPassword(ctx context.Context, email, password string) (User, error) {
	if err := CheckPassword(password); err != nil {
		return User{}, err
	}
	u, err := s.UserByEmail(ctx, email)
	if err != nil {
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	if _, err := s.DB.Q().Exec(ctx, "UPDATE users SET password_hash = ? WHERE id = ?", string(hash), u.ID); err != nil {
		return User{}, err
	}
	_, err = s.DB.Q().Exec(ctx, "DELETE FROM sessions WHERE user_id = ?", u.ID)
	return u, err
}

func (s Service) UserBySession(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNoSession
	}
	var u User
	err := s.DB.Q().Row(ctx, `SELECT u.id, u.name, u.email FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND s.expires_at > ?`,
		hashToken(token), s.now().Unix()).Scan(&u.ID, &u.Name, &u.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	return u, err
}

func (s Service) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.Q().Exec(ctx, "DELETE FROM sessions WHERE token_hash = ?", hashToken(token))
	return err
}

func (s Service) PruneSessions(ctx context.Context) error {
	_, err := s.DB.Q().Exec(ctx, "DELETE FROM sessions WHERE expires_at <= ?", s.now().Unix())
	return err
}
