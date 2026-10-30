package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/auth"
)

type credentials struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := s.Auth.CreateSession(r.Context(), userID)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.SecureCookies, Expires: time.Now().Add(auth.SessionTTL),
	})
	return nil
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.Auth.CountUsers(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	out := map[string]any{"needs_setup": n == 0, "version": Version, "locale": s.Locale}
	if s.demo() {
		out["demo"] = map[string]string{"email": s.DemoEmail, "password": s.DemoPassword, "url": "/demo"}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) demo() bool { return s.DemoEmail != "" && s.DemoPassword != "" }

// handleDemo is the link a public demo is visited from: it logs in with the demo's own
// credentials and opens its project. It exists only when the demo is configured.
func (s *Server) handleDemo(w http.ResponseWriter, r *http.Request) {
	if !s.demo() {
		http.NotFound(w, r)
		return
	}
	u, err := s.Auth.Authenticate(r.Context(), s.DemoEmail, s.DemoPassword)
	if err != nil {
		// The nightly rebuild swaps the database: send to the form rather than to an error.
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if err := s.setSession(w, r, u.ID); err != nil {
		serverError(w, err)
		return
	}
	next := "/"
	if p := s.demoProject(r.URL.Query().Get("project")); p != "" {
		next = "/projects/" + url.PathEscape(p)
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// demoProject is the project /demo opens. DemoProject may list several, comma separated, so the
// site can let the visitor choose with ?project=; only those are ever opened, anything else gets
// the first one.
func (s *Server) demoProject(asked string) string {
	first := ""
	for _, p := range strings.Split(s.DemoProject, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if first == "" {
			first = p
		}
		if p == asked {
			return p
		}
	}
	return first
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	n, err := s.Auth.CountUsers(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "already set up")
		return
	}
	// Wrong codes count like wrong passwords: 10 per IP every 15 minutes leaves a 60-bit
	// code out of reach of guessing.
	ip := s.clientIP(r)
	if s.limiter.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, retry in 15 minutes")
		return
	}
	var c struct {
		credentials
		SetupCode string `json:"setup_code"`
	}
	if err := decode(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !s.Auth.CheckSetupCode(r.Context(), c.SetupCode) {
		s.limiter.fail(ip)
		writeError(w, http.StatusForbidden, "wrong setup code: the installer printed it, and slowpoke-server setup-code prints it again")
		return
	}
	u, err := s.Auth.CreateUser(r.Context(), c.Name, c.Email, c.Password)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// The administrator exists, so the code would open nothing: forget it anyway.
	if err := s.Auth.ClearSetupCode(r.Context()); err != nil {
		slog.Warn("cannot forget the setup code", "err", err)
	}
	if err := s.setSession(w, r, u.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limiter.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, retry in 15 minutes")
		return
	}
	var c credentials
	if err := decode(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, err := s.Auth.Authenticate(r.Context(), c.Email, c.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.limiter.fail(ip)
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err := s.setSession(w, r, u.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_ = s.Auth.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, u auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "version": Version, "locale": s.Locale})
}
