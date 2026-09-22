// Package api exposes the HTTP API used by agents, packages and the web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/auth"
	"github.com/getslowpoke/slowpoke/internal/server/game"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/mcp"
	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/notify"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
	"github.com/getslowpoke/slowpoke/internal/server/tokens"
)

var Version = "0.1.0-dev"

const sessionCookie = "slowpoke_session"

type Server struct {
	DB       *store.DB
	Projects project.Repo
	Auth     auth.Service
	Ingest   *ingest.Service
	Queue    *ingest.Queue
	Tokens   tokens.Service
	Worker   *ingest.Worker
	// MaxQueue is the queue depth above which agents get 429 and retry later.
	MaxQueue int
	Engine   *game.Engine
	Metrics  metrics.Reader
	// Lingua in cui si apre il pannello quando chi guarda non ne ha ancora scelta una
	// (SLOWPOKE_LOCALE). Vuota: decide il browser.
	Locale string
	// A public demo (SLOWPOKE_DEMO_EMAIL / SLOWPOKE_DEMO_PASSWORD): the login page shows the
	// credentials and GET /demo logs straight in, into DemoProject. Empty on any real server.
	DemoEmail, DemoPassword, DemoProject string
	Notifier *notify.Notifier
	Static   fs.FS
	// PublicURL is used in links (notifications, tasks); empty = the request host.
	PublicURL string
	// TrustedProxies may set X-Forwarded-For (reverse proxy in front of the server).
	TrustedProxies []netip.Prefix
	// SecureCookies sets the Secure flag (behind HTTPS).
	SecureCookies bool
	// AgentDir holds the agent binaries this server hands out to the machines it measures.
	AgentDir string
	// UpdateFeed is asked for the latest release, only when somebody presses the button.
	UpdateFeed string

	limiter         *loginLimiter
	explainThrottle throttle
	depthMu         sync.Mutex
	depth           int
	depthAt         time.Time
}

func New(db *store.DB, static fs.FS) *Server {
	srv := &Server{
		DB:       db,
		Projects: project.Repo{DB: db},
		Auth:     auth.Service{DB: db},
		Tokens:   tokens.Service{DB: db},
		Ingest:   ingest.New(db),
		Engine:   game.New(db),
		Metrics:  metrics.Reader{DB: db},
		Static:   static,
		limiter:  &loginLimiter{attempts: map[string][]time.Time{}},
	}
	srv.Notifier = notify.New(db, srv.Engine, "")
	srv.Queue = ingest.NewQueue(db)
	srv.Worker = ingest.NewWorker(srv.Queue, srv.Ingest)
	srv.MaxQueue = 20000
	return srv
}

type ctxKey int

const userKey ctxKey = 1

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.DB.PingContext(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database unreachable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": Version})
	})

	// One command installs a machine: the script is public, the agent it downloads is not.
	mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	mux.HandleFunc("GET /fleet.sh", s.handleFleetScript)
	mux.HandleFunc("GET /agent/{platform}", s.handleAgentBinary)

	// Machine API: project token.
	mux.HandleFunc("POST /api/v1/ingest", s.handleIngest)
	mux.HandleFunc("POST /api/v1/deploys", s.handleDeployWebhook)
	mux.HandleFunc("GET /api/v1/status", s.handleIngestStatus)

	// Session API.
	mux.HandleFunc("GET /api/setup", s.handleSetupStatus)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /demo", s.handleDemo)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.Handle("GET /api/me", s.authed(s.handleMe))
	mux.Handle("GET /api/meta", s.authed(s.handleMeta))
	mux.Handle("GET /api/updates", s.authed(s.handleUpdates))
	mux.Handle("POST /api/updates/check", s.authed(s.handleUpdateCheck))

	mux.Handle("GET /api/projects", s.authed(s.handleListProjects))
	mux.Handle("POST /api/projects", s.authed(s.handleCreateProject))
	mux.Handle("GET /api/projects/{slug}", s.authed(s.withProject(s.handleGetProject)))
	mux.Handle("PATCH /api/projects/{slug}", s.authed(s.withProject(s.handleUpdateProject)))
	mux.Handle("DELETE /api/projects/{slug}", s.authed(s.withProject(s.handleDeleteProject)))
	mux.Handle("POST /api/projects/{slug}/rotate-token", s.authed(s.withProject(s.handleRotateToken)))
	mux.Handle("GET /api/projects/{slug}/overview", s.authed(s.withProject(s.handleOverview)))
	mux.Handle("GET /api/projects/{slug}/slow", s.authed(s.withProject(s.handleSlow)))
	mux.Handle("GET /api/projects/{slug}/queries/{hash}", s.authed(s.withProject(s.handleQuery)))
	mux.Handle("GET /api/projects/{slug}/endpoints", s.authed(s.withProject(s.handleEndpoint)))
	mux.Handle("GET /api/projects/{slug}/missions", s.authed(s.withProject(s.handleMissions)))
	mux.Handle("GET /api/projects/{slug}/busy-hours", s.authed(s.withProject(s.handleBusyHours)))
	mux.Handle("GET /api/projects/{slug}/missions/{id}", s.authed(s.withProject(s.handleMission)))
	mux.Handle("PATCH /api/projects/{slug}/missions/{id}", s.authed(s.withProject(s.handleUpdateMission)))
	mux.Handle("POST /api/projects/{slug}/missions/{id}/claim", s.authed(s.withProject(s.handleClaimMission)))
	mux.Handle("DELETE /api/projects/{slug}/missions/{id}/claim", s.authed(s.withProject(s.handleReleaseMission)))
	mux.Handle("POST /api/projects/{slug}/missions/{id}/shipped", s.authed(s.withProject(s.handleShippedMission)))
	mux.Handle("POST /api/projects/{slug}/missions/{id}/dismiss", s.authed(s.withProject(s.handleDismissMission)))
	mux.Handle("GET /api/projects/{slug}/hotspots", s.authed(s.withProject(s.handleHotspots)))
	mux.Handle("POST /api/projects/{slug}/missions/{id}/tasks", s.authed(s.withProject(s.handleCreateTask)))
	mux.Handle("GET /api/projects/{slug}/deploys", s.authed(s.withProject(s.handleDeploys)))
	mux.Handle("POST /api/projects/{slug}/deploys", s.authed(s.withProject(s.handleCreateDeploy)))
	mux.Handle("GET /api/projects/{slug}/heatmap", s.authed(s.withProject(s.handleHeatmap)))
	mux.Handle("GET /api/projects/{slug}/jobs", s.authed(s.withProject(s.handleJobs)))
	mux.Handle("GET /api/projects/{slug}/hosts", s.authed(s.withProject(s.handleHosts)))
	mux.Handle("GET /api/projects/{slug}/hosts/{id}", s.authed(s.withProject(s.handleHost)))
	mux.Handle("GET /api/projects/{slug}/topology", s.authed(s.withProject(s.handleTopology)))
	mux.Handle("GET /api/projects/{slug}/fleet", s.authed(s.withProject(s.handleFleet)))
	mux.Handle("PUT /api/projects/{slug}/topology/labels", s.authed(s.withProject(s.handleTopologyLabel)))
	mux.Handle("PUT /api/projects/{slug}/topology/places", s.authed(s.withProject(s.handleTopologyPlace)))
	mux.Handle("DELETE /api/projects/{slug}/topology/places", s.authed(s.withProject(s.handleTopologyPlacesReset)))
	mux.Handle("DELETE /api/projects/{slug}/hosts/{id}", s.authed(s.withProject(s.handleDeleteHost)))
	mux.Handle("POST /api/projects/{slug}/hosts/{id}/mute", s.authed(s.withProject(s.handleMuteHost)))
	mux.Handle("DELETE /api/projects/{slug}/hosts/{id}/mute", s.authed(s.withProject(s.handleUnmuteHost)))
	mux.Handle("POST /api/projects/{slug}/queries/{hash}/mutes", s.authed(s.withProject(s.handleMuteDiagnosis)))
	mux.Handle("DELETE /api/projects/{slug}/queries/{hash}/mutes/{id}", s.authed(s.withProject(s.handleUnmuteDiagnosis)))
	mux.Handle("GET /api/projects/{slug}/tokens", s.authed(s.withProject(s.handleListTokens)))
	mux.Handle("POST /api/projects/{slug}/tokens", s.authed(s.handleCreateToken))
	mux.Handle("DELETE /api/projects/{slug}/tokens/{id}", s.authed(s.withProject(s.handleRevokeToken)))
	mux.Handle("GET /api/projects/{slug}/exclusions", s.authed(s.withProject(s.handleExclusions)))
	mux.Handle("POST /api/projects/{slug}/exclusions", s.authed(s.withProject(s.handleCreateExclusion)))
	mux.Handle("DELETE /api/projects/{slug}/exclusions/{id}", s.authed(s.withProject(s.handleDeleteExclusion)))
	mux.Handle("GET /api/projects/{slug}/debt-report", s.authed(s.withProject(s.handleDebtReport)))
	mux.Handle("GET /api/projects/{slug}/metrics", s.authed(s.withProject(s.handleMetrics)))
	mux.Handle("POST /api/projects/{slug}/notifications/test", s.authed(s.withProject(s.handleTestNotification)))

	// Model Context Protocol for coding assistants (personal tokens).
	mux.Handle("/mcp", mcp.Handler(s.DB, Version, s.PublicURL))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.HandleFunc("/", s.handleStatic)

	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")

		// Browser mutations must carry a custom header: a cross-site form cannot set it.
		if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/api/v1/") &&
			r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Slowpoke") == "" {
			writeError(w, http.StatusForbidden, "missing X-Slowpoke header")
			return
		}

		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "path", r.URL.Path, "error", rec)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			slog.Debug("request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "ms", time.Since(start).Milliseconds())
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) authed(fn func(http.ResponseWriter, *http.Request, auth.User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A personal token is the way in for anything that is not a browser: a command on
		// somebody's laptop, a script, an assistant. It belongs to a person and is revocable.
		if bearer := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); bearer != "" {
			t, err := s.Tokens.Authenticate(r.Context(), bearer, tokens.KindPersonal)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid personal token")
				return
			}
			u := auth.User{Name: t.Name}
			if t.UserID != nil {
				u.ID = *t.UserID
			}
			fn(w, r.WithContext(context.WithValue(r.Context(), userKey, u)), u)
			return
		}
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		u, err := s.Auth.UserBySession(r.Context(), cookie.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		fn(w, r.WithContext(context.WithValue(r.Context(), userKey, u)), u)
	})
}

func (s *Server) withProject(fn func(http.ResponseWriter, *http.Request, project.Project)) func(http.ResponseWriter, *http.Request, auth.User) {
	return func(w http.ResponseWriter, r *http.Request, _ auth.User) {
		p, err := s.Projects.BySlug(r.Context(), r.PathValue("slug"))
		if errors.Is(err, project.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		fn(w, r, p)
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.Static == nil {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" {
		if f, err := s.Static.Open(name); err == nil {
			info, statErr := f.Stat()
			f.Close()
			if statErr == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, s.Static, name)
				return
			}
		}
	}
	// A missing asset is a real 404 (a stale cached page must not get HTML as JavaScript).
	if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}
	// Client-side routes: the SPA handles them.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, s.Static, "index.html")
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}

// loginLimiter allows 10 failed attempts per IP every 15 minutes.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-15 * time.Minute)
	recent := l.attempts[ip][:0]
	for _, t := range l.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	l.attempts[ip] = recent
	return len(recent) >= 10
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[ip] = append(l.attempts[ip], time.Now())
}

// ParseTrustedProxies reads a comma-separated list of addresses or networks
// (SLOWPOKE_TRUSTED_PROXIES), e.g. "127.0.0.1, ::1, 10.0.0.0/8".
func ParseTrustedProxies(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			out = append(out, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q is not an address or a network", item)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

func (s *Server) trusted(addr netip.Addr) bool {
	for _, p := range s.TrustedProxies {
		if p.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}

// clientIP is the visitor's address. Behind a reverse proxy the connection comes from the
// proxy, so X-Forwarded-For is read from the right, skipping trusted proxies: entries on its
// left were written by the client and could be forged. Without trusted proxies it is ignored.
func (s *Server) clientIP(r *http.Request) string {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	addr := peer.Addr().Unmap()
	if !s.trusted(addr) {
		return addr.String()
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		addr = hop.Unmap()
		if !s.trusted(addr) {
			break
		}
	}
	return addr.String()
}
