package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestInviteIsAcceptedOnceAndMakesAUser(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	s := Service{DB: store.OpenTest(t), Now: func() time.Time { return now }}
	ada, _ := s.CreateUser(ctx, "Ada", "ada@example.com", "correct horse battery")

	if _, _, err := s.CreateInvite(ctx, "ADA@example.com", ada.ID); err == nil {
		t.Fatal("somebody already in the team was invited")
	}
	if _, _, err := s.CreateInvite(ctx, "not-an-email", ada.ID); err == nil {
		t.Fatal("a bad address was invited")
	}
	inv, token, err := s.CreateInvite(ctx, " Bob@Example.com", ada.ID)
	if err != nil || inv.Email != "bob@example.com" || token == "" {
		t.Fatalf("%+v %q %v", inv, token, err)
	}
	found, err := s.InviteByToken(ctx, token)
	if err != nil || found.Email != "bob@example.com" || found.InvitedBy != "Ada" {
		t.Fatalf("%+v %v", found, err)
	}

	// Inviting the same address again replaces the old link: only the newest one works.
	_, second, err := s.CreateInvite(ctx, "bob@example.com", ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InviteByToken(ctx, token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("the old link still works: %v", err)
	}
	if list, _ := s.ListInvites(ctx); len(list) != 1 {
		t.Fatalf("two invites for one address: %+v", list)
	}

	if _, err := s.AcceptInvite(ctx, second, "Bob", "short"); err == nil {
		t.Fatal("weak password accepted")
	}
	bob, err := s.AcceptInvite(ctx, second, "Bob", "another horse battery")
	if err != nil || bob.Email != "bob@example.com" || bob.Name != "Bob" {
		t.Fatalf("%+v %v", bob, err)
	}
	if _, err := s.Authenticate(ctx, "bob@example.com", "another horse battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptInvite(ctx, second, "Eve", "another horse battery"); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("an invite worked twice: %v", err)
	}
	if list, _ := s.ListInvites(ctx); len(list) != 0 {
		t.Fatalf("an accepted invite is still pending: %+v", list)
	}
}

func TestInviteExpiresAndCanBeRevoked(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	s := Service{DB: store.OpenTest(t), Now: func() time.Time { return now }}
	ada, _ := s.CreateUser(ctx, "Ada", "ada@example.com", "correct horse battery")

	_, token, _ := s.CreateInvite(ctx, "bob@example.com", ada.ID)
	now = now.Add(InviteTTL + time.Minute)
	if _, err := s.InviteByToken(ctx, token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("an expired invite works: %v", err)
	}
	if list, _ := s.ListInvites(ctx); len(list) != 1 || !list[0].Expired {
		t.Fatalf("the expired invite must be listed as expired: %+v", list)
	}

	inv, token, _ := s.CreateInvite(ctx, "carl@example.com", ada.ID)
	if err := s.RevokeInvite(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InviteByToken(ctx, token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("a revoked invite works: %v", err)
	}
	if _, err := s.InviteByToken(ctx, "nonsense"); !errors.Is(err, ErrInviteInvalid) {
		t.Fatal(err)
	}
}

func TestRemovingSomebodyClosesTheirWayIn(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	s := Service{DB: db, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	ada, _ := s.CreateUser(ctx, "Ada", "ada@example.com", "correct horse battery")
	bob, _ := s.CreateUser(ctx, "Bob", "bob@example.com", "correct horse battery")
	session, _ := s.CreateSession(ctx, bob.ID)

	if err := s.DeleteUser(ctx, ada.ID, ada.ID); !errors.Is(err, ErrRemoveSelf) {
		t.Fatalf("somebody removed themselves: %v", err)
	}
	if users, _ := s.ListUsers(ctx); len(users) != 2 {
		t.Fatalf("%+v", users)
	}
	if err := s.DeleteUser(ctx, bob.ID, ada.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBySession(ctx, session); !errors.Is(err, ErrNoSession) {
		t.Fatalf("a removed person is still logged in: %v", err)
	}
	if err := s.DeleteUser(ctx, bob.ID, ada.ID); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("got %v", err)
	}
}
