// Package tokens manages revocable access tokens: one per agent (machine) and
// personal tokens for coding assistants (MCP). Only a hash is stored.
package tokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

const (
	KindAgent    = "agent"
	KindPersonal = "personal"
)

var (
	ErrInvalid  = errors.New("invalid or revoked token")
	ErrNotFound = errors.New("token not found")
)

type Token struct {
	ID         int64  `json:"id"`
	ProjectID  int64  `json:"project_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	UserID     *int64 `json:"user_id,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	RevokedAt  *int64 `json:"revoked_at"`
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

func hash(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func (s Service) Create(ctx context.Context, projectID int64, kind, name string, userID int64) (Token, string, error) {
	if kind != KindAgent && kind != KindPersonal {
		return Token{}, "", errors.New("unknown token kind")
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return Token{}, "", err
	}
	prefix := map[string]string{KindAgent: "spa_", KindPersonal: "spu_"}[kind]
	plain := prefix + hex.EncodeToString(b)
	name = strings.TrimSpace(name)
	if name == "" {
		name = kind
	}
	if len(name) > 120 {
		name = name[:120]
	}
	t := Token{ProjectID: projectID, Kind: kind, Name: name, Prefix: plain[:12], CreatedAt: s.now().Unix()}
	var user any
	if userID > 0 {
		user = userID
		t.UserID = &userID
	}
	id, err := s.DB.Q().InsertID(ctx, `INSERT INTO access_tokens (project_id, kind, name, prefix, token_hash, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID, kind, name, t.Prefix, hash(plain), user, t.CreatedAt)
	t.ID = id
	return t, plain, err
}

// Authenticate returns the token if it is valid and of the expected kind, and records its use.
func (s Service) Authenticate(ctx context.Context, plain, kind string) (Token, error) {
	var t Token
	err := s.DB.Q().Row(ctx, `SELECT id, project_id, kind, name, prefix, user_id, created_at, last_used_at, revoked_at FROM access_tokens WHERE token_hash = ?`, hash(plain)).
		Scan(&t.ID, &t.ProjectID, &t.Kind, &t.Name, &t.Prefix, &t.UserID, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (t.RevokedAt != nil || t.Kind != kind)) {
		return Token{}, ErrInvalid
	}
	if err != nil {
		return Token{}, err
	}
	now := s.now().Unix()
	// One write per minute at most: agents authenticate every 30 seconds.
	if t.LastUsedAt == nil || now-*t.LastUsedAt >= 60 {
		_, _ = s.DB.Q().Exec(ctx, "UPDATE access_tokens SET last_used_at = ? WHERE id = ?", now, t.ID)
		t.LastUsedAt = &now
	}
	return t, nil
}

func (s Service) List(ctx context.Context, projectID int64) ([]Token, error) {
	rows, err := s.DB.Q().Query(ctx, `SELECT id, project_id, kind, name, prefix, user_id, created_at, last_used_at, revoked_at FROM access_tokens
		WHERE project_id = ? ORDER BY revoked_at IS NOT NULL, created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Kind, &t.Name, &t.Prefix, &t.UserID, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s Service) Revoke(ctx context.Context, projectID, id int64) error {
	res, err := s.DB.Q().Exec(ctx, "UPDATE access_tokens SET revoked_at = ? WHERE id = ? AND project_id = ? AND revoked_at IS NULL", s.now().Unix(), id, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
