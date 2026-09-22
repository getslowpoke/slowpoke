package store

import (
	"context"
	"testing"
)

func TestTryLockIsExclusive(t *testing.T) {
	db := OpenTest(t)
	ctx := context.Background()
	unlock, ok, err := db.TryLock(ctx, "game:1")
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	if _, ok2, _ := db.TryLock(ctx, "game:1"); ok2 {
		t.Fatal("second lock must fail while held")
	}
	if unlock2, ok3, _ := db.TryLock(ctx, "game:2"); !ok3 {
		t.Fatal("other names are independent")
	} else {
		unlock2()
	}
	unlock()
	if unlock4, ok4, _ := db.TryLock(ctx, "game:1"); !ok4 {
		t.Fatal("lock must be free after unlock")
	} else {
		unlock4()
	}
}
