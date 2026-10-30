package api

import (
	"context"
	"net/http"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/auth"
	"github.com/getslowpoke/slowpoke/internal/server/mail"
	"github.com/getslowpoke/slowpoke/internal/server/project"
)

// mailer is the way out for an email, and where it was configured: the environment
// (SLOWPOKE_SMTP_*) wins, then the account saved in the panel, else none.
func (s *Server) mailer(ctx context.Context) (mail.Sender, string) {
	if s.Mailer != nil && s.Mailer.Enabled() {
		return s.Mailer, "env"
	}
	cfg, err := mail.Store{DB: s.DB}.Get(ctx)
	if err != nil || !cfg.Enabled() {
		return nil, "none"
	}
	if s.panelSender != nil {
		return s.panelSender(cfg), "panel"
	}
	return cfg.Sender(), "panel"
}

func (s *Server) mailOn(ctx context.Context) bool {
	_, source := s.mailer(ctx)
	return source != "none"
}

type mailAccount struct {
	Provider  string `json:"provider"`
	Host      string `json:"host"`
	Port      string `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	APIKey    string `json:"api_key"`
	Domain    string `json:"domain"`
	Region    string `json:"region"`
	AccountID string `json:"account_id"`
	From      string `json:"from"`
}

func (s *Server) handleGetMail(w http.ResponseWriter, r *http.Request, _ auth.User) {
	_, source := s.mailer(r.Context())
	var a mail.Account
	switch source {
	case "env":
		if smtp, ok := s.Mailer.(mail.SMTP); ok {
			a = mail.Account{Provider: "smtp", Host: smtp.Host, Port: smtp.Port, Username: smtp.Username, Password: smtp.Password, From: smtp.From}
		}
	case "panel":
		a, _ = mail.Store{DB: s.DB}.Get(r.Context())
	}
	provider := a.Provider
	if provider == "" && source != "none" {
		provider = "smtp"
	}
	mask := func(v string) string {
		if v == "" {
			return ""
		}
		return project.SecretMask
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": source, "provider": provider, "providers": mail.Providers,
		"host": a.Host, "port": a.Port, "username": a.Username, "password": mask(a.Password),
		"api_key": mask(a.APIKey), "domain": a.Domain, "region": a.Region, "account_id": a.AccountID, "from": a.From,
	})
}

func (s *Server) handlePutMail(w http.ResponseWriter, r *http.Request, _ auth.User) {
	if _, source := s.mailer(r.Context()); source == "env" {
		writeError(w, http.StatusConflict, "email is set on the server (SLOWPOKE_SMTP_*): change it there")
		return
	}
	var in mailAccount
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	a := mail.Account{Provider: strings.TrimSpace(in.Provider), From: strings.TrimSpace(in.From)}
	if a.Provider == "" {
		a.Provider = "smtp"
	}
	if a.API() {
		a.APIKey, a.Domain, a.Region, a.AccountID = strings.TrimSpace(in.APIKey), strings.TrimSpace(in.Domain), strings.TrimSpace(in.Region), strings.TrimSpace(in.AccountID)
	} else {
		a.Host, a.Port, a.Username, a.Password = strings.TrimSpace(in.Host), strings.TrimSpace(in.Port), strings.TrimSpace(in.Username), in.Password
		if a.Port == "" {
			a.Port = "587"
		}
	}
	store := mail.Store{DB: s.DB}
	old, err := store.Get(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	// The mask sent back keeps what is stored, as long as it is the same provider.
	if a.Password == project.SecretMask {
		a.Password = old.Password
	}
	if a.APIKey == project.SecretMask {
		a.APIKey = ""
		if old.Provider == a.Provider {
			a.APIKey = old.APIKey
		}
	}
	if msg := checkAccount(a); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if err := store.Save(r.Context(), a); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func checkAccount(a mail.Account) string {
	for _, v := range []string{a.Host, a.Port, a.Username, a.Password, a.APIKey, a.Domain, a.Region, a.AccountID, a.From} {
		if strings.ContainsAny(v, "\r\n") {
			return "a field cannot contain a line break"
		}
	}
	if _, err := netmail.ParseAddress(a.From); err != nil {
		return "the sender is not a valid address (e.g. Slowpoke <slowpoke@example.com>)"
	}
	if a.API() {
		known := false
		for _, p := range mail.Providers {
			known = known || p == a.Provider
		}
		switch {
		case !known:
			return "unknown provider"
		case a.APIKey == "":
			return "the API key is missing"
		case a.Provider == "mailgun" && (a.Domain == "" || strings.ContainsAny(a.Domain, " /")):
			return "Mailgun needs the sending domain (e.g. mg.example.com)"
		case a.Provider == "cloudflare" && (a.AccountID == "" || strings.ContainsAny(a.AccountID, " /")):
			return "Cloudflare needs the account ID (on the right of the domain overview)"
		}
		return ""
	}
	if a.Host == "" || strings.ContainsAny(a.Host, " /:") {
		return "the server name is missing or not valid (e.g. smtp.example.com)"
	}
	if p, err := strconv.Atoi(a.Port); err != nil || p < 1 || p > 65535 {
		return "the port must be a number (usually 587 or 465)"
	}
	return ""
}

func (s *Server) handleDeleteMail(w http.ResponseWriter, r *http.Request, _ auth.User) {
	if err := (mail.Store{DB: s.DB}).Clear(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTestMail sends a short email to whoever pressed the button, so they see it arrive.
func (s *Server) handleTestMail(w http.ResponseWriter, r *http.Request, u auth.User) {
	sender, source := s.mailer(r.Context())
	if source == "none" {
		writeError(w, http.StatusConflict, "no email account configured yet")
		return
	}
	to := u.Email
	if to == "" {
		writeError(w, http.StatusUnprocessableEntity, "your user has no email address to send the test to")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	err := sender.Send(ctx, mail.Message{
		To:      to,
		Subject: "Slowpoke can send email",
		Text:    "This is a test from Slowpoke (" + s.publicURL(r) + "). Team invitations will arrive like this one.",
		HTML:    `<p>This is a test from Slowpoke (<a href="` + s.publicURL(r) + `">` + s.publicURL(r) + `</a>).</p><p>Team invitations will arrive like this one.</p>`,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"sent": false, "to": to, "problem": mail.Explain(err), "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "to": to})
}
