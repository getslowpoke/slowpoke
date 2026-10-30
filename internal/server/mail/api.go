package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"
)

// API sends through a provider's HTTP API. Many clouds close the SMTP ports of a new server;
// HTTPS always gets out, and a key is all a person has to paste.
type API struct {
	Provider  string // brevo, resend, postmark, sendgrid, mailgun, cloudflare
	Key       string
	From      string
	Domain    string // mailgun: the sending domain
	Region    string // mailgun: "eu" for the European endpoint
	AccountID string // cloudflare
	// BaseURL replaces the provider's address, for tests.
	BaseURL string
	HTTP    *http.Client
}

// Providers are the APIs Slowpoke speaks.
var Providers = []string{"brevo", "resend", "postmark", "sendgrid", "mailgun", "cloudflare"}

func knownProvider(p string) bool {
	for _, k := range Providers {
		if k == p {
			return true
		}
	}
	return false
}

func (a API) Enabled() bool {
	if !knownProvider(a.Provider) || a.Key == "" || a.From == "" {
		return false
	}
	switch a.Provider {
	case "mailgun":
		return a.Domain != ""
	case "cloudflare":
		return a.AccountID != ""
	}
	return true
}

func (a API) base(standard string) string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	return standard
}

func (a API) Send(ctx context.Context, m Message) error {
	if !a.Enabled() {
		return errors.New("mail: the email account is not complete")
	}
	for _, v := range []string{m.To, m.Subject, a.From} {
		if err := noNewline(v); err != nil {
			return err
		}
	}
	from, err := netmail.ParseAddress(a.From)
	if err != nil {
		return fmt.Errorf("mail: sender address: %w", err)
	}
	var req *http.Request
	jsonReq := func(u string, body any) (*http.Request, error) {
		raw, _ := json.Marshal(body)
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
		if err == nil {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json")
		}
		return r, err
	}
	who := func(name, email string) map[string]string {
		out := map[string]string{"email": email}
		if name != "" {
			out["name"] = name
		}
		return out
	}
	switch a.Provider {
	case "brevo":
		req, err = jsonReq(a.base("https://api.brevo.com")+"/v3/smtp/email", map[string]any{
			"sender": who(from.Name, from.Address), "to": []any{who("", m.To)},
			"subject": m.Subject, "textContent": m.Text, "htmlContent": m.HTML,
		})
		if err == nil {
			req.Header.Set("api-key", a.Key)
		}
	case "resend":
		req, err = jsonReq(a.base("https://api.resend.com")+"/emails", map[string]any{
			"from": a.From, "to": []string{m.To}, "subject": m.Subject, "text": m.Text, "html": m.HTML,
		})
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+a.Key)
		}
	case "postmark":
		req, err = jsonReq(a.base("https://api.postmarkapp.com")+"/email", map[string]any{
			"From": a.From, "To": m.To, "Subject": m.Subject, "TextBody": m.Text, "HtmlBody": m.HTML, "MessageStream": "outbound",
		})
		if err == nil {
			req.Header.Set("X-Postmark-Server-Token", a.Key)
		}
	case "sendgrid":
		content := []map[string]string{{"type": "text/plain", "value": m.Text}}
		if m.HTML != "" {
			content = append(content, map[string]string{"type": "text/html", "value": m.HTML})
		}
		req, err = jsonReq(a.base("https://api.sendgrid.com")+"/v3/mail/send", map[string]any{
			"personalizations": []any{map[string]any{"to": []any{who("", m.To)}}},
			"from":             who(from.Name, from.Address), "subject": m.Subject, "content": content,
		})
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+a.Key)
		}
	case "mailgun":
		host := "https://api.mailgun.net"
		if strings.EqualFold(a.Region, "eu") {
			host = "https://api.eu.mailgun.net"
		}
		form := url.Values{"from": {a.From}, "to": {m.To}, "subject": {m.Subject}, "text": {m.Text}}
		if m.HTML != "" {
			form.Set("html", m.HTML)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, a.base(host)+"/v3/"+url.PathEscape(a.Domain)+"/messages", strings.NewReader(form.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetBasicAuth("api", a.Key)
		}
	case "cloudflare":
		req, err = jsonReq(a.base("https://api.cloudflare.com")+"/client/v4/accounts/"+url.PathEscape(a.AccountID)+"/email/sending/send", map[string]any{
			"from": a.From, "to": m.To, "subject": m.Subject, "text": m.Text, "html": m.HTML,
		})
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+a.Key)
		}
	}
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		// The URL carries no secret, but the message is said without it anyway.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("mail: %s: %w", a.Provider, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("mail: %s answered %d: %s", a.Provider, res.StatusCode, providerMessage(raw))
	}
	// Cloudflare can answer 200 and still refuse.
	if a.Provider == "cloudflare" {
		var out struct {
			Success *bool `json:"success"`
		}
		if json.Unmarshal(raw, &out) == nil && out.Success != nil && !*out.Success {
			return fmt.Errorf("mail: %s answered %d: %s", a.Provider, res.StatusCode, providerMessage(raw))
		}
	}
	return nil
}

// providerMessage pulls the human part out of a provider's error, whatever its shape.
func providerMessage(raw []byte) string {
	var v struct {
		Message string `json:"message"`
		Msg     string `json:"Message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &v) == nil {
		var parts []string
		for _, s := range []string{v.Message, v.Msg} {
			if s != "" {
				parts = append(parts, s)
			}
		}
		for _, e := range v.Errors {
			if e.Message != "" {
				parts = append(parts, e.Message)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		return "no details"
	}
	return s
}

// Account is what the panel saves: an SMTP server or a provider's API.
type Account struct {
	Provider           string // "" or "smtp" for a server, else one of Providers
	Host, Port         string
	Username, Password string
	APIKey             string
	Domain, Region     string
	AccountID          string
	From               string
}

func (a Account) API() bool { return a.Provider != "" && a.Provider != "smtp" }

func (a Account) Sender() Sender {
	if a.API() {
		return API{Provider: a.Provider, Key: a.APIKey, From: a.From, Domain: a.Domain, Region: a.Region, AccountID: a.AccountID}
	}
	return SMTP{Host: a.Host, Port: a.Port, Username: a.Username, Password: a.Password, From: a.From}
}

func (a Account) Enabled() bool { return a.Sender().Enabled() }
