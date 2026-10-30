package api

import (
	"context"
	"errors"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/server/mail"
)

// capturing records the account the panel would send with, instead of dialling it.
type capturing struct {
	cfg  mail.Account
	sent []mail.Message
	err  error
}

func (c *capturing) sender(cfg mail.Account) mail.Sender {
	c.cfg = cfg
	return capturingSender{c}
}

type capturingSender struct{ c *capturing }

func (s capturingSender) Enabled() bool { return s.c.cfg.Enabled() }
func (s capturingSender) Send(_ context.Context, m mail.Message) error {
	if s.c.err != nil {
		return s.c.err
	}
	s.c.sent = append(s.c.sent, m)
	return nil
}

func TestMailIsSetFromThePanel(t *testing.T) {
	c := newClient(t)
	cap := &capturing{}
	c.srv.panelSender = cap.sender
	c.login()

	if _, body := c.do("GET", "/api/mail", nil); body["source"] != "none" {
		t.Fatalf("nothing configured: %v", body)
	}
	if _, team := c.do("GET", "/api/team", nil); team["mail"] != false {
		t.Fatalf("%v", team)
	}

	account := map[string]string{"host": "smtp-relay.brevo.com", "port": "587", "username": "me@example.com", "password": "xsmtpsib-secret", "from": "Slowpoke <slowpoke@example.com>"}
	if code, body := c.do("PUT", "/api/mail", account); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	_, body := c.do("GET", "/api/mail", nil)
	if body["source"] != "panel" || body["host"] != "smtp-relay.brevo.com" || body["password"] != "••••••••" {
		t.Fatalf("the password must never come back: %v", body)
	}

	// Saving again with the mask keeps the stored password.
	account["password"] = "••••••••"
	account["from"] = "slowpoke@example.com"
	c.do("PUT", "/api/mail", account)
	if code, body := c.do("POST", "/api/mail/test", nil); code != 200 || body["sent"] != true {
		t.Fatalf("%d %v", code, body)
	}
	if cap.cfg.Password != "xsmtpsib-secret" || cap.cfg.From != "slowpoke@example.com" {
		t.Fatalf("%+v", cap.cfg)
	}
	if len(cap.sent) != 1 || cap.sent[0].To != "ada@example.com" {
		t.Fatalf("the test goes to whoever pressed the button: %+v", cap.sent)
	}

	// Invitations now leave through the account saved in the panel.
	if _, body := c.do("POST", "/api/team/invites", map[string]string{"email": "bob@example.com"}); body["sent"] != true {
		t.Fatalf("%v", body)
	}

	// A failure says what to fix.
	cap.err = errors.New("mail: login: 535 5.7.8 Authentication failed")
	_, body = c.do("POST", "/api/mail/test", nil)
	if body["sent"] != false || body["problem"] != "auth" || body["error"] == nil {
		t.Fatalf("%v", body)
	}

	if code, _ := c.do("DELETE", "/api/mail", nil); code != 200 {
		t.Fatal(code)
	}
	if _, body := c.do("GET", "/api/mail", nil); body["source"] != "none" {
		t.Fatalf("%v", body)
	}
}

func TestMailFromTheEnvironmentWins(t *testing.T) {
	c := newClient(t)
	c.srv.Mailer = mail.SMTP{Host: "smtp.example.com", Port: "465", Username: "api_token", Password: "env-secret", From: "slowpoke@example.com"}
	c.login()
	_, body := c.do("GET", "/api/mail", nil)
	if body["source"] != "env" || body["host"] != "smtp.example.com" || body["password"] != "••••••••" {
		t.Fatalf("%v", body)
	}
	if code, _ := c.do("PUT", "/api/mail", map[string]string{"host": "other.example.com", "from": "a@example.com"}); code != 409 {
		t.Fatalf("the panel cannot override the environment: %d", code)
	}
}

func TestMailSettingsAreChecked(t *testing.T) {
	c := newClient(t)
	c.login()
	for _, bad := range []map[string]string{
		{"host": "", "from": "slowpoke@example.com"},
		{"host": "smtp.example.com", "from": "not an address"},
		{"host": "smtp.example.com", "port": "nope", "from": "slowpoke@example.com"},
		{"host": "smtp.example.com\r\nX: y", "from": "slowpoke@example.com"},
	} {
		if code, _ := c.do("PUT", "/api/mail", bad); code != 422 {
			t.Fatalf("%v accepted: %d", bad, code)
		}
	}
	c.srv.DemoEmail, c.srv.DemoPassword = "ada@example.com", "correct horse battery"
	if code, _ := c.do("PUT", "/api/mail", map[string]string{"host": "smtp.example.com", "from": "a@example.com"}); code != 403 {
		t.Fatalf("the demo mail account changed: %d", code)
	}
}

func TestMailThroughAProviderAPI(t *testing.T) {
	c := newClient(t)
	cap := &capturing{}
	c.srv.panelSender = cap.sender
	c.login()

	if code, _ := c.do("PUT", "/api/mail", map[string]string{"provider": "brevo", "from": "slowpoke@example.com"}); code != 422 {
		t.Fatalf("no key: %d", code)
	}
	if code, _ := c.do("PUT", "/api/mail", map[string]string{"provider": "mailgun", "api_key": "k", "from": "slowpoke@example.com"}); code != 422 {
		t.Fatalf("mailgun without a domain: %d", code)
	}
	if code, _ := c.do("PUT", "/api/mail", map[string]string{"provider": "carrier-pigeon", "api_key": "k", "from": "slowpoke@example.com"}); code != 422 {
		t.Fatalf("unknown provider: %d", code)
	}
	account := map[string]string{"provider": "brevo", "api_key": "xkeysib-secret", "from": "Slowpoke <slowpoke@example.com>"}
	if code, body := c.do("PUT", "/api/mail", account); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	_, body := c.do("GET", "/api/mail", nil)
	if body["provider"] != "brevo" || body["api_key"] != "••••••••" || body["source"] != "panel" {
		t.Fatalf("%v", body)
	}
	account["api_key"] = "••••••••"
	c.do("PUT", "/api/mail", account)
	c.do("POST", "/api/mail/test", nil)
	if cap.cfg.APIKey != "xkeysib-secret" || cap.cfg.Provider != "brevo" {
		t.Fatalf("the mask must keep the stored key: %+v", cap.cfg)
	}
	// Switching provider with the mask does not carry the old key to a different company.
	if code, _ := c.do("PUT", "/api/mail", map[string]string{"provider": "resend", "api_key": "••••••••", "from": "slowpoke@example.com"}); code != 422 {
		t.Fatalf("a Brevo key sent to Resend: %d", code)
	}
}
