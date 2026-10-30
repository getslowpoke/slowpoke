package auth

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestSetupCodeIsStableAndReadsTheWayPeopleTypeIt(t *testing.T) {
	ctx := context.Background()
	s := Service{DB: store.OpenTest(t)}

	code, err := s.SetupCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$`).MatchString(code) {
		t.Fatalf("code %q: three groups of four, no 0/O or 1/I to misread", code)
	}
	// The installer, the log and the CLI must all print the same code until it is used.
	if again, _ := s.SetupCode(ctx); again != code {
		t.Fatalf("code changed between calls: %q then %q", code, again)
	}
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	if !s.CheckSetupCode(ctx, typed) {
		t.Fatalf("%q typed as %q was refused", code, typed)
	}
	if s.CheckSetupCode(ctx, "") || s.CheckSetupCode(ctx, "AAAA-BBBB-CCCC") {
		t.Fatal("a wrong or empty code was accepted")
	}
	if err := s.ClearSetupCode(ctx); err != nil {
		t.Fatal(err)
	}
	if s.CheckSetupCode(ctx, code) {
		t.Fatal("a used code still works")
	}
}

func TestNoSetupCodeMeansNothingIsAccepted(t *testing.T) {
	// A database where nobody ever asked for a code must not accept an empty one.
	s := Service{DB: store.OpenTest(t)}
	if s.CheckSetupCode(context.Background(), "") {
		t.Fatal("empty code accepted with no code stored")
	}
}
