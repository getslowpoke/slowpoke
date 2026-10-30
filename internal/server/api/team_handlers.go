package api

import (
	"context"
	"errors"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/auth"
	"github.com/getslowpoke/slowpoke/internal/server/mail"
)

func (s *Server) handleTeam(w http.ResponseWriter, r *http.Request, u auth.User) {
	users, err := s.Auth.ListUsers(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	invites, err := s.Auth.ListInvites(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "invites": invites, "mail": s.mailOn(r.Context()), "me": u.ID, "demo": s.demo()})
}

// teamChange guards the changes to the team. On the public demo everybody is the same person:
// one visitor removing the demo user would lock every other visitor out until the nightly rebuild.
func (s *Server) teamChange(fn func(http.ResponseWriter, *http.Request, auth.User)) http.Handler {
	return s.authed(func(w http.ResponseWriter, r *http.Request, u auth.User) {
		if s.demo() {
			writeError(w, http.StatusForbidden, "the team cannot be changed on the demo")
			return
		}
		fn(w, r, u)
	})
}

type inviteRequest struct {
	Email  string `json:"email"`
	Locale string `json:"locale"`
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request, u auth.User) {
	var body inviteRequest
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.invite(w, r, u, body.Email, body.Locale)
}

// handleResendInvite makes a new link for the same address and sends it again: the old link
// stops working, so a lost email is never a second key in somebody's mailbox.
func (s *Server) handleResendInvite(w http.ResponseWriter, r *http.Request, u auth.User) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	inv, err := s.Auth.InviteByID(r.Context(), id)
	if errors.Is(err, auth.ErrInviteInvalid) {
		writeError(w, http.StatusNotFound, "invitation not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var body inviteRequest
	_ = decode(r, &body)
	s.invite(w, r, u, inv.Email, body.Locale)
}

func (s *Server) invite(w http.ResponseWriter, r *http.Request, u auth.User, email, locale string) {
	inv, token, err := s.Auth.CreateInvite(r.Context(), email, u.ID)
	if errors.Is(err, auth.ErrAlreadyMember) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	link := s.publicURL(r) + "/invite/" + token
	out := map[string]any{"invite": inv, "sent": false}
	if sender, source := s.mailer(r.Context()); source != "none" {
		inviter := u.Name
		if inviter == "" {
			inviter = "Slowpoke"
		}
		// The inviter waits on the button, not on a slow mail server: past a few seconds the
		// link is shown to pass along by hand.
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		err := sender.Send(ctx, inviteEmail(inv.Email, inviter, link, locale))
		cancel()
		if err == nil {
			out["sent"] = true
		} else {
			slog.Warn("invitation email not sent", "to", inv.Email, "error", err)
			out["mail_error"] = err.Error()
		}
	}
	// Without an email on its way the link is the invitation: whoever invites passes it on.
	if out["sent"] == false {
		out["link"] = link
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request, _ auth.User) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Auth.RevokeInvite(r.Context(), id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, u auth.User) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	switch err := s.Auth.DeleteUser(r.Context(), id, u.ID); {
	case errors.Is(err, auth.ErrRemoveSelf):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		serverError(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleInvite answers the page the link opens: who invited whom, before any account exists.
func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	inv, err := s.Auth.InviteByToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, auth.ErrInviteInvalid) {
		writeError(w, http.StatusGone, err.Error())
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": inv.Email, "invited_by": inv.InvitedBy, "expires_at": inv.ExpiresAt})
}

func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := decode(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, err := s.Auth.AcceptInvite(r.Context(), r.PathValue("token"), c.Name, c.Password)
	if errors.Is(err, auth.ErrInviteInvalid) {
		writeError(w, http.StatusGone, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.setSession(w, r, u.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

type inviteWords struct {
	subject, hello, body, button, expires, ignore string
}

var inviteCopy = map[string]inviteWords{
	"en": {
		subject: "{who} invited you to Slowpoke",
		hello:   "Hi,",
		body:    "{who} invited you to join the team on Slowpoke, where slow requests and slow queries become missions with a price in seconds lost per day.",
		button:  "Accept the invitation",
		expires: "The link works for 7 days and only once.",
		ignore:  "If you were not expecting this, you can ignore this email: nothing happens until you open the link.",
	},
	"it": {
		subject: "{who} ti ha invitato su Slowpoke",
		hello:   "Ciao,",
		body:    "{who} ti ha invitato nel team su Slowpoke, dove richieste e query lente diventano missioni con un prezzo in secondi persi al giorno.",
		button:  "Accetta l'invito",
		expires: "Il link vale 7 giorni e una volta sola.",
		ignore:  "Se non te lo aspettavi puoi ignorare questa email: non succede niente finché non apri il link.",
	},
}

func inviteEmail(to, who, link, locale string) mail.Message {
	words, ok := inviteCopy[strings.ToLower(strings.TrimSpace(locale))]
	if !ok {
		words = inviteCopy["en"]
	}
	fill := func(s string) string { return strings.ReplaceAll(s, "{who}", who) }
	text := strings.Join([]string{words.hello, "", fill(words.body), "", words.button + ": " + link, "", words.expires, words.ignore, "", "— Slowpoke"}, "\n")
	e := html.EscapeString
	htmlBody := `<!doctype html><html><body style="margin:0;padding:24px;background:#f6f3ef;font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#1f1b18">` +
		`<div style="max-width:520px;margin:0 auto;background:#ffffff;border-radius:16px;padding:32px;border:1px solid #ece6df">` +
		`<div style="font-size:28px">🐢</div>` +
		`<h1 style="font-size:20px;margin:12px 0 16px">` + e(fill(words.subject)) + `</h1>` +
		`<p style="line-height:1.55;margin:0 0 8px">` + e(words.hello) + `</p>` +
		`<p style="line-height:1.55;margin:0 0 24px">` + e(fill(words.body)) + `</p>` +
		`<p style="margin:0 0 24px"><a href="` + e(link) + `" style="display:inline-block;background:#ff6f8e;color:#ffffff;text-decoration:none;font-weight:600;padding:12px 20px;border-radius:12px">` + e(words.button) + `</a></p>` +
		`<p style="font-size:13px;color:#6b625b;line-height:1.5;margin:0 0 8px">` + e(words.expires) + `</p>` +
		`<p style="font-size:13px;color:#6b625b;line-height:1.5;margin:0 0 8px">` + e(words.ignore) + `</p>` +
		`<p style="font-size:12px;color:#9a918a;word-break:break-all;margin:16px 0 0">` + e(link) + `</p>` +
		`</div></body></html>`
	return mail.Message{To: to, Subject: fill(words.subject), Text: text, HTML: htmlBody}
}
