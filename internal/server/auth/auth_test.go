package auth

import (
	"context"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestUsersAndSessions(t *testing.T) {
	ctx := context.Background()
	s := Service{DB: store.OpenTest(t), Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}

	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatal("empty")
	}
	if _, err := s.CreateUser(ctx, "Ada", "ADA@example.com ", "short"); err == nil {
		t.Fatal("weak password accepted")
	}
	if _, err := s.CreateUser(ctx, "Ada", "not-an-email", "correct horse battery"); err == nil {
		t.Fatal("bad email accepted")
	}
	u, err := s.CreateUser(ctx, "Ada", "ADA@example.com ", "correct horse battery")
	if err != nil || u.Email != "ada@example.com" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := s.CreateUser(ctx, "Ada 2", "ada@example.com", "correct horse battery"); err == nil {
		t.Fatal("duplicate email accepted")
	}

	if _, err := s.Authenticate(ctx, "ada@example.com", "wrong password"); err != ErrInvalidCredentials {
		t.Fatalf("got %v", err)
	}
	if _, err := s.Authenticate(ctx, "nobody@example.com", "correct horse battery"); err != ErrInvalidCredentials {
		t.Fatalf("got %v", err)
	}
	logged, err := s.Authenticate(ctx, " Ada@Example.com", "correct horse battery")
	if err != nil || logged.ID != u.ID {
		t.Fatalf("%+v %v", logged, err)
	}

	token, err := s.CreateSession(ctx, u.ID)
	if err != nil || len(token) < 40 {
		t.Fatalf("token %q %v", token, err)
	}
	if got, err := s.UserBySession(ctx, token); err != nil || got.ID != u.ID {
		t.Fatalf("%+v %v", got, err)
	}
	var stored string
	_ = s.DB.Q().Row(ctx, "SELECT token_hash FROM sessions").Scan(&stored)
	if stored == token {
		t.Fatal("session tokens must be stored hashed")
	}

	s.Now = func() time.Time { return time.Unix(1_800_000_000, 0).Add(31 * 24 * time.Hour) }
	if _, err := s.UserBySession(ctx, token); err == nil {
		t.Fatal("expired session accepted")
	}
	s.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	if err := s.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBySession(ctx, token); err == nil {
		t.Fatal("deleted session accepted")
	}
}

// A token that belongs to a person can be created without a browser: headless installations and
// the fleet command need it.
func TestUserByEmail(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	s := Service{DB: db}
	created, err := s.CreateUser(ctx, "Ada", "Ada@Example.com", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	found, err := s.UserByEmail(ctx, "  ada@example.com ")
	if err != nil || found.ID != created.ID || found.Name != "Ada" {
		t.Fatalf("%+v %v", found, err)
	}
	if _, err := s.UserByEmail(ctx, "nobody@example.com"); err == nil {
		t.Fatal("an unknown address is an error, not an empty user")
	}
}

// Somebody locked out of their own panel has no way back in from the browser: there is no email
// to send a reset to, because Slowpoke does not know how to send email. The way back in is on the
// machine, and it must also close the sessions of whoever still holds the old password.
func TestSetPasswordReplacesTheOldOneAndClosesEverySession(t *testing.T) {
	ctx := context.Background()
	s := Service{DB: store.OpenTest(t), Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	if _, err := s.CreateUser(ctx, "Ada", "ada@example.com", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, mustUser(t, s, "ada@example.com").ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetPassword(ctx, "ada@example.com", "short"); err == nil {
		t.Fatal("a weak password was accepted")
	}
	if _, err := s.SetPassword(ctx, "nobody@example.com", "a brand new password"); err == nil {
		t.Fatal("a password was set for an address nobody has")
	}
	if _, err := s.Authenticate(ctx, "ada@example.com", "correct horse battery"); err != nil {
		t.Fatal("a refused change must leave the old password working:", err)
	}

	// The address is matched the way the login form matches it, spaces and capitals included.
	u, err := s.SetPassword(ctx, " ADA@example.com ", "a brand new password")
	if err != nil || u.Email != "ada@example.com" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := s.Authenticate(ctx, "ada@example.com", "a brand new password"); err != nil {
		t.Fatal("the new password does not work:", err)
	}
	if _, err := s.Authenticate(ctx, "ada@example.com", "correct horse battery"); err == nil {
		t.Fatal("the old password still works")
	}
	if _, err := s.UserBySession(ctx, session); err == nil {
		t.Fatal("a session opened with the old password survived the change")
	}
}

func mustUser(t *testing.T, s Service, email string) User {
	t.Helper()
	u, err := s.UserByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
