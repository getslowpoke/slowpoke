package tokens

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestCreateAuthenticateRevoke(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	other, _ := project.Repo{DB: db}.Create(ctx, "Other", "UTC")
	svc := Service{DB: db, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}

	agentTok, plain, err := svc.Create(ctx, p.ID, KindAgent, "api-1", 0)
	if err != nil || !strings.HasPrefix(plain, "spa_") || agentTok.Prefix != plain[:12] {
		t.Fatalf("%+v %q %v", agentTok, plain, err)
	}
	_, personal, _ := svc.Create(ctx, p.ID, KindPersonal, "Ada's Claude Code", 0)
	if !strings.HasPrefix(personal, "spu_") {
		t.Fatal(personal)
	}
	var stored string
	_ = db.Q().Row(ctx, "SELECT token_hash FROM access_tokens WHERE id = ?", agentTok.ID).Scan(&stored)
	if stored == plain || strings.Contains(stored, plain) {
		t.Fatal("tokens must be stored hashed")
	}

	got, err := svc.Authenticate(ctx, plain, KindAgent)
	if err != nil || got.ProjectID != p.ID || got.Name != "api-1" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := svc.Authenticate(ctx, plain, KindPersonal); err != ErrInvalid {
		t.Fatal("an agent token must not work as a personal token")
	}
	if _, err := svc.Authenticate(ctx, "spa_nope", KindAgent); err != ErrInvalid {
		t.Fatal("unknown token")
	}

	list, _ := svc.List(ctx, p.ID)
	if len(list) != 2 || list[0].LastUsedAt == nil {
		t.Fatalf("list %+v", list)
	}
	if err := svc.Revoke(ctx, other.ID, agentTok.ID); err != ErrNotFound {
		t.Fatal("a token can only be revoked from its project")
	}
	if err := svc.Revoke(ctx, p.ID, agentTok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, plain, KindAgent); err != ErrInvalid {
		t.Fatal("revoked token still works")
	}
}
