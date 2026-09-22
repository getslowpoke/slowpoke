package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/tokens"
)

const maxIngestBytes = 20 << 20

func (s *Server) projectFromToken(w http.ResponseWriter, r *http.Request) (project.Project, bool) {
	p, _, ok := s.agentAuth(w, r)
	return p, ok
}

// agentAuth accepts the project token or a per-machine agent token; tokenID is 0 for the project token.
func (s *Server) agentAuth(w http.ResponseWriter, r *http.Request) (project.Project, int64, bool) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if strings.HasPrefix(token, "spa_") {
		t, err := s.Tokens.Authenticate(r.Context(), token, tokens.KindAgent)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid agent token")
			return project.Project{}, 0, false
		}
		p, err := s.Projects.ByID(r.Context(), t.ProjectID)
		if err != nil {
			serverError(w, err)
			return p, 0, false
		}
		return p, t.ID, true
	}
	p, err := s.Projects.ByToken(r.Context(), token)
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "invalid project token")
		return p, 0, false
	}
	if err != nil {
		serverError(w, err)
		return p, 0, false
	}
	return p, 0, true
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	p, tokenID, ok := s.agentAuth(w, r)
	if !ok {
		return
	}
	if depth := s.queueDepth(r); depth >= s.MaxQueue {
		w.Header().Set("Retry-After", "30")
		writeError(w, http.StatusTooManyRequests, "ingest queue is full, retry later")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIngestBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	gzipped := r.Header.Get("Content-Encoding") == "gzip"
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty body")
		return
	}
	if err := s.Queue.EnqueueFrom(r.Context(), p.ID, tokenID, body, gzipped); err != nil {
		serverError(w, err)
		return
	}

	// Every agent learns which version this server is, and where its own binary lives: that is
	// how a fleet stays aligned without anyone going machine by machine.
	resp := protocol.IngestResponse{OK: true, ServerVersion: Version, AgentDownload: s.agentDownload(r.Header.Get("X-Slowpoke-Platform"))}
	if strings.Contains(r.Header.Get("X-Slowpoke-Capabilities"), "explain") && s.explainThrottle.allow(p.ID, 30*time.Second) {
		if resp.Explain, err = s.Ingest.ExplainRequests(r.Context(), p, time.Now()); err != nil {
			slog.Warn("explain requests failed", "project", p.Slug, "error", err)
		}
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// queueDepth is cached for a second: the check runs on every request.
func (s *Server) queueDepth(r *http.Request) int {
	s.depthMu.Lock()
	defer s.depthMu.Unlock()
	if time.Since(s.depthAt) > time.Second {
		if d, err := s.Queue.Depth(r.Context()); err == nil {
			s.depth, s.depthAt = d, time.Now()
		}
	}
	return s.depth
}

type throttle struct {
	mu   sync.Mutex
	last map[int64]time.Time
}

func (t *throttle) allow(id int64, every time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = map[int64]time.Time{}
	}
	if time.Since(t.last[id]) < every {
		return false
	}
	t.last[id] = time.Now()
	return true
}

func (s *Server) insertDeploy(r *http.Request, p project.Project, d protocol.Deploy) (int64, error) {
	at := d.DeployedAt
	if at == 0 || at > time.Now().Add(time.Hour).Unix() {
		at = time.Now().Unix()
	}
	clip := func(v string, n int) string {
		if len(v) > n {
			return v[:n]
		}
		return v
	}
	return s.DB.Q().InsertID(r.Context(), `INSERT INTO deploys (project_id, version, commit_sha, author, message, url, deployed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, clip(d.Version, 100), clip(d.Commit, 64), clip(d.Author, 100), clip(d.Message, 500), clip(d.URL, 500), at)
}

func (s *Server) handleDeployWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectFromToken(w, r)
	if !ok {
		return
	}
	var d protocol.Deploy
	if err := decode(r, &d); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id, err := s.insertDeploy(r, p, d)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// handleIngestStatus lets agents and load tests see the queue.
func (s *Server) handleIngestStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.projectFromToken(w, r); !ok {
		return
	}
	depth, err := s.Queue.Depth(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queue_depth": depth, "max_queue": s.MaxQueue, "version": Version,
		"processed": s.Worker.Processed.Load(), "failed": s.Worker.Failed.Load()})
}
