package api

import (
	"context"
	"testing"
)

// Regression for the case the site now warns about: the installer publishes port 8080 on every
// interface, and until an administrator existed, whoever opened the panel first became one.
func TestFirstAdministratorNeedsTheSetupCode(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	user := map[string]string{"name": "Eve", "email": "eve@example.com", "password": "correct horse battery"}

	if code, _ := c.do("POST", "/api/setup", user); code != 403 {
		t.Fatalf("setup without a code: got %d, want 403", code)
	}
	user["setup_code"] = "AAAA-BBBB-CCCC"
	if code, _ := c.do("POST", "/api/setup", user); code != 403 {
		t.Fatalf("setup with a wrong code: got %d, want 403", code)
	}
	if n, _ := c.srv.Auth.CountUsers(ctx); n != 0 {
		t.Fatalf("%d user(s) created without the code", n)
	}

	setupCode, err := c.srv.Auth.SetupCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	user["name"], user["email"], user["setup_code"] = "Ada", "ada@example.com", setupCode
	if code, body := c.do("POST", "/api/setup", user); code != 200 {
		t.Fatalf("setup with the right code: %d %v", code, body)
	}
	// Used once: the code opens nothing any more, and the panel is set up.
	if c.srv.Auth.CheckSetupCode(ctx, setupCode) {
		t.Fatal("the code still works after the administrator was created")
	}
	if _, body := c.do("GET", "/api/setup", nil); body["needs_setup"] != false {
		t.Fatalf("%v", body)
	}
}

func TestGuessingTheSetupCodeIsSlowedDown(t *testing.T) {
	c := newClient(t)
	if _, err := c.srv.Auth.SetupCode(context.Background()); err != nil {
		t.Fatal(err)
	}
	user := map[string]string{"name": "Eve", "email": "eve@example.com", "password": "correct horse battery", "setup_code": "AAAA-BBBB-CCCC"}
	last := 0
	for i := 0; i < 11; i++ {
		last, _ = c.do("POST", "/api/setup", user)
	}
	if last != 429 {
		t.Fatalf("after 11 wrong codes: got %d, want 429", last)
	}
}
