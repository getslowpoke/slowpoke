package project

import (
	"context"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestCreateFindUpdateRotate(t *testing.T) {
	ctx := context.Background()
	r := Repo{DB: store.OpenTest(t)}

	p, err := r.Create(ctx, "My Shop", "Europe/Rome")
	if err != nil {
		t.Fatal(err)
	}
	dup, err := r.Create(ctx, "My shop", "Not/AZone")
	if err != nil || dup.Slug != "my-shop-2" || dup.Timezone != "UTC" {
		t.Fatalf("dup %+v err %v", dup, err)
	}

	byToken, err := r.ByToken(ctx, p.Token)
	if err != nil || byToken.Slug != "my-shop" || byToken.Location().String() != "Europe/Rome" {
		t.Fatalf("%+v %v", byToken, err)
	}
	if _, err := r.ByToken(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("want not found, got %v", err)
	}

	p.Settings.Missions.WindowHours = 1
	if err := r.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Settings.Missions.WindowHours = 0
	if err := r.Update(ctx, p); err == nil {
		t.Fatal("invalid settings must be refused")
	}
	again, _ := r.BySlug(ctx, "my-shop")
	if again.Settings.Missions.WindowHours != 1 {
		t.Fatalf("settings not saved: %+v", again.Settings.Missions)
	}

	newToken, err := r.RotateToken(ctx, p.ID)
	if err != nil || newToken == p.Token {
		t.Fatal("token not rotated")
	}
	if _, err := r.ByToken(ctx, p.Token); err != ErrNotFound {
		t.Fatal("old token must stop working")
	}
}
