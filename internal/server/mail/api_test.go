package mail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type seen struct {
	path, auth, key, token, contentType string
	body                                []byte
}

func provider(t *testing.T, status int, answer string) (*httptest.Server, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		got.key = r.Header.Get("api-key")
		got.token = r.Header.Get("X-Postmark-Server-Token")
		got.contentType = r.Header.Get("Content-Type")
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

var invitation = Message{To: "bob@example.com", Subject: "Ada invited you", Text: "open the link", HTML: "<p>open the link</p>"}

func asJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return out
}

func TestEachProviderGetsTheMessageItsWay(t *testing.T) {
	ctx := context.Background()
	from := "Slowpoke <slowpoke@slowpoke.example.com>"

	t.Run("brevo", func(t *testing.T) {
		srv, got := provider(t, 201, `{"messageId":"x"}`)
		if err := (API{Provider: "brevo", Key: "xkeysib-1", From: from, BaseURL: srv.URL}).Send(ctx, invitation); err != nil {
			t.Fatal(err)
		}
		b := asJSON(t, got.body)
		sender := b["sender"].(map[string]any)
		if got.path != "/v3/smtp/email" || got.key != "xkeysib-1" || sender["email"] != "slowpoke@slowpoke.example.com" || sender["name"] != "Slowpoke" ||
			b["to"].([]any)[0].(map[string]any)["email"] != "bob@example.com" || b["htmlContent"] != "<p>open the link</p>" || b["textContent"] != "open the link" {
			t.Fatalf("%s %s %s", got.path, got.key, got.body)
		}
	})

	t.Run("resend", func(t *testing.T) {
		srv, got := provider(t, 200, `{"id":"x"}`)
		if err := (API{Provider: "resend", Key: "re_1", From: from, BaseURL: srv.URL}).Send(ctx, invitation); err != nil {
			t.Fatal(err)
		}
		b := asJSON(t, got.body)
		if got.path != "/emails" || got.auth != "Bearer re_1" || b["from"] != from || b["to"].([]any)[0] != "bob@example.com" || b["html"] == nil {
			t.Fatalf("%s %s %s", got.path, got.auth, got.body)
		}
	})

	t.Run("postmark", func(t *testing.T) {
		srv, got := provider(t, 200, `{"ErrorCode":0}`)
		if err := (API{Provider: "postmark", Key: "pm-1", From: from, BaseURL: srv.URL}).Send(ctx, invitation); err != nil {
			t.Fatal(err)
		}
		b := asJSON(t, got.body)
		if got.path != "/email" || got.token != "pm-1" || b["From"] != from || b["To"] != "bob@example.com" || b["HtmlBody"] == nil || b["MessageStream"] != "outbound" {
			t.Fatalf("%s %s %s", got.path, got.token, got.body)
		}
	})

	t.Run("sendgrid", func(t *testing.T) {
		srv, got := provider(t, 202, ``)
		if err := (API{Provider: "sendgrid", Key: "SG.1", From: from, BaseURL: srv.URL}).Send(ctx, invitation); err != nil {
			t.Fatal(err)
		}
		b := asJSON(t, got.body)
		to := b["personalizations"].([]any)[0].(map[string]any)["to"].([]any)[0].(map[string]any)
		content := b["content"].([]any)
		if got.path != "/v3/mail/send" || got.auth != "Bearer SG.1" || to["email"] != "bob@example.com" || len(content) != 2 ||
			content[0].(map[string]any)["type"] != "text/plain" || b["from"].(map[string]any)["email"] != "slowpoke@slowpoke.example.com" {
			t.Fatalf("%s %s %s", got.path, got.auth, got.body)
		}
	})

	t.Run("mailgun", func(t *testing.T) {
		srv, got := provider(t, 200, `{"id":"x"}`)
		if err := (API{Provider: "mailgun", Key: "key-1", Domain: "mg.example.com", From: from, BaseURL: srv.URL}).Send(ctx, invitation); err != nil {
			t.Fatal(err)
		}
		form, _ := url.ParseQuery(string(got.body))
		if got.path != "/v3/mg.example.com/messages" || !strings.HasPrefix(got.auth, "Basic ") || form.Get("to") != "bob@example.com" ||
			form.Get("from") != from || form.Get("html") == "" || !strings.HasPrefix(got.contentType, "application/x-www-form-urlencoded") {
			t.Fatalf("%s %s %s", got.path, got.auth, got.body)
		}
	})

	t.Run("cloudflare", func(t *testing.T) {
		srv, got := provider(t, 200, `{"success":true,"errors":[],"result":{"delivered":["bob@example.com"]}}`)
		if err := (API{Provider: "cloudflare", Key: "cf-1", AccountID: "acc123", From: from, BaseURL: srv.URL}).Send(ctx, invitation); err != nil {
			t.Fatal(err)
		}
		b := asJSON(t, got.body)
		if got.path != "/client/v4/accounts/acc123/email/sending/send" || got.auth != "Bearer cf-1" || b["to"] != "bob@example.com" || b["from"] != from || b["text"] != "open the link" {
			t.Fatalf("%s %s %s", got.path, got.auth, got.body)
		}
	})
}

func TestAProviderSayingNoIsExplained(t *testing.T) {
	ctx := context.Background()
	srv, _ := provider(t, 401, `{"code":"unauthorized","message":"Key not found"}`)
	err := (API{Provider: "brevo", Key: "wrong", From: "a@example.com", BaseURL: srv.URL}).Send(ctx, invitation)
	if err == nil || Explain(err) != "auth" || !strings.Contains(err.Error(), "Key not found") {
		t.Fatalf("%v -> %s", err, Explain(err))
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatal("the key must never end up in an error")
	}

	srv, _ = provider(t, 400, `{"message":"Sender slowpoke@x.com is not valid. Validate your sender or authenticate your domain"}`)
	err = (API{Provider: "brevo", Key: "k", From: "slowpoke@x.com", BaseURL: srv.URL}).Send(ctx, invitation)
	if Explain(err) != "sender" {
		t.Fatalf("%v -> %s", err, Explain(err))
	}

	// Cloudflare answers 200 with success false when nothing was delivered.
	srv, _ = provider(t, 200, `{"success":false,"errors":[{"code":10001,"message":"email.sending.error.sender_domain_not_verified"}]}`)
	err = (API{Provider: "cloudflare", Key: "k", AccountID: "a", From: "a@example.com", BaseURL: srv.URL}).Send(ctx, invitation)
	if err == nil || Explain(err) != "sender" {
		t.Fatalf("%v -> %s", err, Explain(err))
	}
}

func TestAnAPIAccountNeedsWhatItsProviderNeeds(t *testing.T) {
	if (API{Provider: "brevo", From: "a@example.com"}).Enabled() {
		t.Fatal("no key")
	}
	if (API{Provider: "mailgun", Key: "k", From: "a@example.com"}).Enabled() {
		t.Fatal("mailgun without a domain")
	}
	if (API{Provider: "cloudflare", Key: "k", From: "a@example.com"}).Enabled() {
		t.Fatal("cloudflare without an account id")
	}
	if (API{Provider: "nope", Key: "k", From: "a@example.com"}).Enabled() {
		t.Fatal("unknown provider")
	}
	if !(API{Provider: "resend", Key: "k", From: "a@example.com"}).Enabled() {
		t.Fatal("resend with key and sender")
	}
}

func TestAccountPicksTheWayOut(t *testing.T) {
	if _, ok := (Account{Host: "smtp.example.com", From: "a@example.com"}).Sender().(SMTP); !ok {
		t.Fatal("no provider means SMTP, as saved before providers existed")
	}
	if _, ok := (Account{Provider: "resend", APIKey: "k", From: "a@example.com"}).Sender().(API); !ok {
		t.Fatal("resend goes through the API")
	}
}
