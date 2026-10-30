package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/server/mail"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	off  bool
	fail bool
}

func (f *fakeMailer) Enabled() bool { return !f.off }

func (f *fakeMailer) Send(_ context.Context, m mail.Message) error {
	if f.fail {
		return errors.New("connection refused")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

var inviteLink = regexp.MustCompile(`https?://[^\s"<]+/invite/([0-9a-f]{64})`)

func (f *fakeMailer) lastToken(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("no email was sent")
	}
	m := inviteLink.FindStringSubmatch(f.sent[len(f.sent)-1].Text)
	if m == nil {
		t.Fatalf("no link in %q", f.sent[len(f.sent)-1].Text)
	}
	return m[1]
}

func TestInviteByEmailAndAccept(t *testing.T) {
	c := newClient(t)
	mailer := &fakeMailer{}
	c.srv.Mailer = mailer
	c.srv.PublicURL = "https://slowpoke.example.com"
	c.login()

	code, body := c.do("POST", "/api/team/invites", map[string]string{"email": "bob@example.com", "locale": "it"})
	if code != 201 || body["sent"] != true || body["link"] != nil {
		t.Fatalf("%d %v", code, body)
	}
	if got := mailer.sent[0]; got.To != "bob@example.com" || !strings.Contains(got.Subject, "Ada") || !strings.Contains(got.Text, "https://slowpoke.example.com/invite/") {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(mailer.sent[0].Subject, "invitato") {
		t.Fatalf("the email must speak the inviter's language: %q", mailer.sent[0].Subject)
	}
	token := mailer.lastToken(t)

	_, team := c.do("GET", "/api/team", nil)
	if len(team["users"].([]any)) != 1 || len(team["invites"].([]any)) != 1 || team["mail"] != true {
		t.Fatalf("%v", team)
	}

	// The person invited is somebody else, in another browser.
	jar, _ := cookiejar.New(nil)
	bob := &client{t: t, base: c.base, http: &http.Client{Jar: jar}, srv: c.srv}
	if code, body := bob.do("GET", "/api/invites/"+token, nil); code != 200 || body["email"] != "bob@example.com" || body["invited_by"] != "Ada" {
		t.Fatalf("%d %v", code, body)
	}
	if code, _ := bob.do("POST", "/api/invites/"+token+"/accept", map[string]string{"name": "Bob", "password": "short"}); code != 422 {
		t.Fatalf("weak password: %d", code)
	}
	if code, body := bob.do("POST", "/api/invites/"+token+"/accept", map[string]string{"name": "Bob", "password": "another horse battery"}); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	if code, body := bob.do("GET", "/api/me", nil); code != 200 || body["user"].(map[string]any)["email"] != "bob@example.com" {
		t.Fatalf("accepting logs in: %d %v", code, body)
	}
	if code, _ := bob.do("POST", "/api/invites/"+token+"/accept", map[string]string{"name": "Eve", "password": "another horse battery"}); code != 410 {
		t.Fatalf("a used link must be gone: %d", code)
	}
	if code, _ := bob.do("GET", "/api/invites/"+token, nil); code != 410 {
		t.Fatalf("a used link must be gone: %d", code)
	}
	_, team = c.do("GET", "/api/team", nil)
	if len(team["users"].([]any)) != 2 || len(team["invites"].([]any)) != 0 {
		t.Fatalf("%v", team)
	}
}

func TestInviteWithoutMailGivesTheLinkToPassOn(t *testing.T) {
	c := newClient(t)
	c.srv.Mailer = &fakeMailer{off: true}
	c.login()
	code, body := c.do("POST", "/api/team/invites", map[string]string{"email": "bob@example.com"})
	link, _ := body["link"].(string)
	if code != 201 || body["sent"] != false || !inviteLink.MatchString(link) {
		t.Fatalf("%d %v", code, body)
	}

	// A mail server that fails still leaves the invitation and the link, with the reason.
	c.srv.Mailer = &fakeMailer{fail: true}
	code, body = c.do("POST", "/api/team/invites", map[string]string{"email": "carl@example.com"})
	if code != 201 || body["sent"] != false || body["link"] == nil || body["mail_error"] == nil {
		t.Fatalf("%d %v", code, body)
	}
}

func TestInviteResendRevokeAndErrors(t *testing.T) {
	c := newClient(t)
	mailer := &fakeMailer{}
	c.srv.Mailer = mailer
	c.login()

	if code, _ := c.do("POST", "/api/team/invites", map[string]string{"email": "ada@example.com"}); code != 409 {
		t.Fatalf("inviting a member: %d", code)
	}
	if code, _ := c.do("POST", "/api/team/invites", map[string]string{"email": "nope"}); code != 422 {
		t.Fatalf("bad address: %d", code)
	}
	_, body := c.do("POST", "/api/team/invites", map[string]string{"email": "bob@example.com"})
	id := int64(body["invite"].(map[string]any)["id"].(float64))
	first := mailer.lastToken(t)

	code, _ := c.do("POST", fmt.Sprintf("/api/team/invites/%d/resend", id), nil)
	if code != 201 || len(mailer.sent) != 2 {
		t.Fatalf("resend: %d, %d emails", code, len(mailer.sent))
	}
	second := mailer.lastToken(t)
	if first == second {
		t.Fatal("a resend must make a new link")
	}
	if code, _ := c.do("GET", "/api/invites/"+first, nil); code != 410 {
		t.Fatalf("the old link still works: %d", code)
	}

	_, team := c.do("GET", "/api/team", nil)
	newID := int64(team["invites"].([]any)[0].(map[string]any)["id"].(float64))
	if code, _ := c.do("DELETE", fmt.Sprintf("/api/team/invites/%d", newID), nil); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := c.do("GET", "/api/invites/"+second, nil); code != 410 {
		t.Fatalf("a revoked link still works: %d", code)
	}
	if code, _ := c.do("POST", fmt.Sprintf("/api/team/invites/%d/resend", newID), nil); code != 404 {
		t.Fatalf("resend of a revoked invite: %d", code)
	}
}

func TestRemovingATeamMember(t *testing.T) {
	c := newClient(t)
	mailer := &fakeMailer{}
	c.srv.Mailer = mailer
	c.login()
	c.do("POST", "/api/team/invites", map[string]string{"email": "bob@example.com"})
	jar, _ := cookiejar.New(nil)
	bob := &client{t: t, base: c.base, http: &http.Client{Jar: jar}, srv: c.srv}
	_, body := bob.do("POST", "/api/invites/"+mailer.lastToken(t)+"/accept", map[string]string{"name": "Bob", "password": "another horse battery"})
	bobID := int64(body["user"].(map[string]any)["id"].(float64))

	// Bob's personal token dies with him.
	p := c.project()
	_, tok := bob.do("POST", "/api/projects/"+p["slug"].(string)+"/tokens", map[string]string{"kind": "personal", "name": "laptop"})
	plain, _ := tok["secret"].(string)
	if plain == "" {
		t.Fatalf("no personal token: %v", tok)
	}

	_, me := c.do("GET", "/api/me", nil)
	adaID := int64(me["user"].(map[string]any)["id"].(float64))
	if code, _ := c.do("DELETE", fmt.Sprintf("/api/team/users/%d", adaID), nil); code != 409 {
		t.Fatalf("removing yourself: %d", code)
	}
	if code, _ := c.do("DELETE", fmt.Sprintf("/api/team/users/%d", bobID), nil); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	if code, _ := bob.do("GET", "/api/me", nil); code != 401 {
		t.Fatalf("a removed person is still in: %d", code)
	}
	if code, _ := bob.do("GET", "/api/projects", nil, "Authorization", "Bearer "+plain); code != 401 {
		t.Fatalf("a removed person's token still works: %d", code)
	}
	if code, _ := c.do("DELETE", fmt.Sprintf("/api/team/users/%d", bobID), nil); code != 404 {
		t.Fatalf("remove twice: %d", code)
	}
}

func TestTeamNeedsLogin(t *testing.T) {
	c := newClient(t)
	c.login()
	c.do("POST", "/api/logout", nil)
	for _, r := range [][2]string{{"GET", "/api/team"}, {"POST", "/api/team/invites"}, {"DELETE", "/api/team/users/1"}} {
		if code, _ := c.do(r[0], r[1], map[string]string{"email": "x@example.com"}); code != 401 {
			t.Fatalf("%s %s: %d", r[0], r[1], code)
		}
	}
}

// On the public demo everybody logs in as the same person: one visitor inviting a friend and
// removing the demo user would lock every other visitor out until the nightly rebuild.
func TestThePublicDemoTeamIsReadOnly(t *testing.T) {
	c := newClient(t)
	c.srv.Mailer = &fakeMailer{}
	c.login()
	c.srv.DemoEmail, c.srv.DemoPassword = "ada@example.com", "correct horse battery"
	if code, body := c.do("GET", "/api/team", nil); code != 200 || body["demo"] != true {
		t.Fatalf("the demo team is still visible: %d %v", code, body)
	}
	for _, r := range [][2]string{{"POST", "/api/team/invites"}, {"POST", "/api/team/invites/1/resend"}, {"DELETE", "/api/team/invites/1"}, {"DELETE", "/api/team/users/2"}} {
		if code, _ := c.do(r[0], r[1], map[string]string{"email": "eve@example.com"}); code != 403 {
			t.Fatalf("%s %s on the demo: %d", r[0], r[1], code)
		}
	}
}
