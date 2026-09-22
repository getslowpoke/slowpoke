package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Nothing leaves the machine on its own: the panel asks for a new version only when somebody
// presses the button, and it says plainly what it found.
func TestCheckingForANewVersionHappensOnlyWhenAsked(t *testing.T) {
	var calls int
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","name":"Slowpoke 9.9.9","html_url":"https://example.test/releases/9.9.9","published_at":"2026-10-01T10:00:00Z"}`))
	}))
	defer feed.Close()

	c := newClient(t)
	c.login()
	c.project()
	c.srv.UpdateFeed = feed.URL

	// The page itself never asks anybody: it only says what this server is and which machines
	// are behind it.
	code, body := c.do("GET", "/api/updates", nil)
	if code != 200 || body["version"] != Version || body["can_check"] != true {
		t.Fatalf("updates %d %v", code, body)
	}
	if calls != 0 {
		t.Fatal("opening the page must not call anything outside")
	}

	code, body = c.do("POST", "/api/updates/check", nil)
	if code != 200 {
		t.Fatalf("check %d %v", code, body)
	}
	if body["latest"] != "9.9.9" || body["newer"] != true || body["url"] != "https://example.test/releases/9.9.9" {
		t.Fatalf("a newer version must be reported: %v", body)
	}
	if calls != 1 {
		t.Fatalf("the check asked %d times", calls)
	}

	// A feed that says the same version we run.
	same := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v` + baseVersion(Version) + `"}`))
	}))
	defer same.Close()
	c.srv.UpdateFeed = same.URL
	if _, body = c.do("POST", "/api/updates/check", nil); body["newer"] != false {
		t.Fatalf("same version: %v", body)
	}

	// A feed that cannot be reached says so, and breaks nothing.
	c.srv.UpdateFeed = "http://127.0.0.1:1/never"
	code, body = c.do("POST", "/api/updates/check", nil)
	if code != 502 || body["error"] == nil {
		t.Fatalf("unreachable feed: %d %v", code, body)
	}

	// With no feed configured the button is not even offered.
	c.srv.UpdateFeed = ""
	if _, body = c.do("GET", "/api/updates", nil); body["can_check"] != false {
		t.Fatalf("no feed: %v", body)
	}
	if code, _ := c.do("POST", "/api/updates/check", nil); code != 422 {
		t.Fatalf("checking without a feed: %d", code)
	}
}

func TestVersionsAreComparedOnTheNumbersOnly(t *testing.T) {
	cases := []struct {
		running, latest string
		newer           bool
	}{
		{"0.1.0-3af334d", "0.2.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.9", false},
		{"0.1.0-dev", "0.1.0", false},
		{"0.1.0", "v0.1.1", true},
		{"0.9.0", "0.10.0", true},
		{"0.1.0", "nonsense", false},
	}
	for _, c := range cases {
		if got := isNewer(c.running, c.latest); got != c.newer {
			t.Fatalf("%s -> %s: got %v", c.running, c.latest, got)
		}
	}
}
