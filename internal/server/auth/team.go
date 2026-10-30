package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"
)

// InviteTTL is how long an invitation link works: long enough for somebody on holiday, short
// enough that a link forgotten in a mailbox does not open the panel months later.
const InviteTTL = 7 * 24 * time.Hour

var (
	ErrInviteInvalid = errors.New("this invitation is no longer valid: ask for a new one")
	ErrRemoveSelf    = errors.New("you cannot remove yourself")
	ErrUserNotFound  = errors.New("user not found")
	ErrAlreadyMember = errors.New("this person is already in the team")
)

// Member is a person of the team as the team page shows them.
type Member struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	CreatedAt int64  `json:"created_at"`
}

// Invite is a pending invitation. Only its hash is stored: the link lives in the email alone.
type Invite struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	InvitedBy string `json:"invited_by"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Expired   bool   `json:"expired"`
}

func (s Service) ListUsers(ctx context.Context) ([]Member, error) {
	rows, err := s.DB.Q().Query(ctx, "SELECT id, name, email, created_at FROM users ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteUser removes a person and every way they had in: sessions and personal tokens. Agent
// tokens they created stay, because machines keep measuring whoever set them up.
func (s Service) DeleteUser(ctx context.Context, id, by int64) error {
	if id == by {
		return ErrRemoveSelf
	}
	var n int
	if err := s.DB.Q().Row(ctx, "SELECT COUNT(*) FROM users WHERE id = ?", id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrUserNotFound
	}
	if _, err := s.DB.Q().Exec(ctx, "UPDATE access_tokens SET revoked_at = ? WHERE user_id = ? AND kind = 'personal' AND revoked_at IS NULL", s.now().Unix(), id); err != nil {
		return err
	}
	if _, err := s.DB.Q().Exec(ctx, "DELETE FROM sessions WHERE user_id = ?", id); err != nil {
		return err
	}
	_, err := s.DB.Q().Exec(ctx, "DELETE FROM users WHERE id = ?", id)
	return err
}

// CreateInvite makes a fresh link for an address. Inviting the same address again replaces the
// previous link, so "send it again" never leaves two working links around.
func (s Service) CreateInvite(ctx context.Context, email string, by int64) (Invite, string, error) {
	email = normalizeEmail(email)
	if _, err := mail.ParseAddress(email); err != nil || !strings.Contains(email, "@") {
		return Invite{}, "", errors.New("invalid email")
	}
	var exists int
	if err := s.DB.Q().Row(ctx, "SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&exists); err != nil {
		return Invite{}, "", err
	}
	if exists > 0 {
		return Invite{}, "", ErrAlreadyMember
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Invite{}, "", err
	}
	token := hex.EncodeToString(b)
	now := s.now()
	if _, err := s.DB.Q().Exec(ctx, "DELETE FROM invites WHERE email = ?", email); err != nil {
		return Invite{}, "", err
	}
	id, err := s.DB.Q().InsertID(ctx, "INSERT INTO invites (email, token_hash, invited_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		email, hashToken(token), by, now.Unix(), now.Add(InviteTTL).Unix())
	if err != nil {
		return Invite{}, "", err
	}
	inv, err := s.inviteWhere(ctx, "i.id = ?", id)
	return inv, token, err
}

func (s Service) inviteWhere(ctx context.Context, where string, arg any) (Invite, error) {
	var inv Invite
	var by sql.NullString
	err := s.DB.Q().Row(ctx, `SELECT i.id, i.email, u.name, i.created_at, i.expires_at FROM invites i
		LEFT JOIN users u ON u.id = i.invited_by WHERE `+where, arg).Scan(&inv.ID, &inv.Email, &by, &inv.CreatedAt, &inv.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, ErrInviteInvalid
	}
	inv.InvitedBy = by.String
	inv.Expired = inv.ExpiresAt <= s.now().Unix()
	return inv, err
}

// ListInvites returns the invitations nobody has accepted yet, expired ones included: the team
// page says they expired rather than letting them vanish.
func (s Service) ListInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.DB.Q().Query(ctx, `SELECT i.id, i.email, u.name, i.created_at, i.expires_at FROM invites i
		LEFT JOIN users u ON u.id = i.invited_by ORDER BY i.created_at DESC, i.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var inv Invite
		var by sql.NullString
		if err := rows.Scan(&inv.ID, &inv.Email, &by, &inv.CreatedAt, &inv.ExpiresAt); err != nil {
			return nil, err
		}
		inv.InvitedBy = by.String
		inv.Expired = inv.ExpiresAt <= s.now().Unix()
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s Service) InviteByID(ctx context.Context, id int64) (Invite, error) {
	return s.inviteWhere(ctx, "i.id = ?", id)
}

// InviteByToken finds the invitation behind a link, only while it still works.
func (s Service) InviteByToken(ctx context.Context, token string) (Invite, error) {
	if token == "" {
		return Invite{}, ErrInviteInvalid
	}
	inv, err := s.inviteWhere(ctx, "i.token_hash = ?", hashToken(token))
	if err != nil {
		return Invite{}, err
	}
	if inv.Expired {
		return Invite{}, ErrInviteInvalid
	}
	return inv, nil
}

func (s Service) RevokeInvite(ctx context.Context, id int64) error {
	_, err := s.DB.Q().Exec(ctx, "DELETE FROM invites WHERE id = ?", id)
	return err
}

// AcceptInvite turns a working link into a person with the address it was sent to: whoever holds
// the link chooses a name and a password, never the address.
func (s Service) AcceptInvite(ctx context.Context, token, name, password string) (User, error) {
	inv, err := s.InviteByToken(ctx, token)
	if err != nil {
		return User{}, err
	}
	if err := CheckPassword(password); err != nil {
		return User{}, err
	}
	u, err := s.CreateUser(ctx, name, inv.Email, password)
	if err != nil {
		return User{}, err
	}
	_, err = s.DB.Q().Exec(ctx, "DELETE FROM invites WHERE email = ?", inv.Email)
	return u, err
}
