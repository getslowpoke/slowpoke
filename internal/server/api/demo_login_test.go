package api

import (
	"net/http"
	"strings"
	"testing"
)

// A public demo says its credentials on the login page and has a link that logs straight in: the
// person who clicked "see the demo" on the site should land on the panel, not on a form.
func TestAPublicDemoOpensWithOneLink(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	_, _ = c.do("POST", "/api/logout", nil)

	// Without the demo configured, nothing changes: no link, no credentials anywhere.
	if code, _ := c.do("GET", "/demo", nil); code != http.StatusNotFound {
		t.Fatalf("/demo without a demo: %d", code)
	}
	if _, body := c.do("GET", "/api/setup", nil); body["demo"] != nil {
		t.Fatalf("credentials shown without a demo: %v", body["demo"])
	}

	c.srv.DemoEmail, c.srv.DemoPassword, c.srv.DemoProject = "ada@example.com", "correct horse battery", "shop"
	_, body := c.do("GET", "/api/setup", nil)
	demo, _ := body["demo"].(map[string]any)
	if demo["email"] != "ada@example.com" || demo["password"] != "correct horse battery" || demo["url"] != "/demo" {
		t.Fatalf("demo on the login page: %v", body["demo"])
	}

	noFollow := *c.http
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := noFollow.Get(c.base + "/demo")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/projects/shop" {
		t.Fatalf("/demo: %d to %q", res.StatusCode, res.Header.Get("Location"))
	}
	if code, me := c.do("GET", "/api/me", nil); code != 200 || !strings.Contains(me["user"].(map[string]any)["email"].(string), "ada@") {
		t.Fatalf("not logged in after /demo: %d %v", code, me)
	}

	// Credentials that no longer work (the nightly rebuild is halfway) send to the login page
	// instead of failing.
	_, _ = c.do("POST", "/api/logout", nil)
	c.srv.DemoPassword = "not the password"
	res, err = noFollow.Get(c.base + "/demo")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/login" {
		t.Fatalf("/demo with a wrong password: %d to %q", res.StatusCode, res.Header.Get("Location"))
	}
}

// The site lets the visitor pick which demo to open: /demo?project= opens one of the demo's own
// projects, and anything else (a typo, someone else's slug) falls back to the first one.
func TestThePublicDemoOpensTheProjectTheVisitorPicked(t *testing.T) {
	c := newClient(t)
	c.login()
	c.project()
	_, _ = c.do("POST", "/api/logout", nil)
	c.srv.DemoEmail, c.srv.DemoPassword, c.srv.DemoProject = "ada@example.com", "correct horse battery", "demo-shop, nestwise"

	noFollow := *c.http
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for query, want := range map[string]string{
		"":                            "/projects/demo-shop",
		"?project=nestwise":           "/projects/nestwise",
		"?project=demo-shop":          "/projects/demo-shop",
		"?project=secret":             "/projects/demo-shop",
		"?project=%2F%2Fevil.example": "/projects/demo-shop",
	} {
		res, err := noFollow.Get(c.base + "/demo" + query)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != want {
			t.Errorf("/demo%s: %d to %q, want %q", query, res.StatusCode, res.Header.Get("Location"), want)
		}
	}
}
