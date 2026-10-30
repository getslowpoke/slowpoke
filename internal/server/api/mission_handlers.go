package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/auth"
	"github.com/getslowpoke/slowpoke/internal/server/debt"
	"github.com/getslowpoke/slowpoke/internal/server/game"
	"github.com/getslowpoke/slowpoke/internal/server/missionctx"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/tasks"
)

func (s *Server) missionContext(r *http.Request, p project.Project) (missionctx.Context, error) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return missionctx.Build(r.Context(), s.DB, p, id, missionctx.Options{PublicURL: s.publicURL(r)})
}

// publicURL is the configured public URL, or the one the browser used.
func (s *Server) publicURL(r *http.Request) string {
	if s.PublicURL != "" {
		return s.PublicURL
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) handleMission(w http.ResponseWriter, r *http.Request, p project.Project) {
	c, err := s.missionContext(r, p)
	if errors.Is(err, missionctx.ErrNotFound) {
		writeError(w, http.StatusNotFound, "mission not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	integrations := []map[string]string{}
	for _, t := range p.Settings.Integrations.Tasks {
		integrations = append(integrations, map[string]string{"id": t.ID, "name": t.Name, "provider": t.Provider})
	}
	writeJSON(w, http.StatusOK, map[string]any{"context": c, "markdown": c.Markdown(), "integrations": integrations})
}

// handleOpenMission opens a mission by hand from a slow thing, or leads to the one already open.
func (s *Server) handleOpenMission(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id, created, err := s.Engine.OpenMission(r.Context(), p, body.Kind, body.Target)
	switch {
	case errors.Is(err, game.ErrUnknownKind):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	case errors.Is(err, game.ErrUnknownTarget):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		serverError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"id": id, "created": created})
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		Integration string `json:"integration"`
		Assignee    string `json:"assignee"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var integration *tasks.Integration
	for i := range p.Settings.Integrations.Tasks {
		if p.Settings.Integrations.Tasks[i].ID == body.Integration {
			integration = &p.Settings.Integrations.Tasks[i]
		}
	}
	if integration == nil {
		writeError(w, http.StatusUnprocessableEntity, "unknown integration")
		return
	}
	c, err := s.missionContext(r, p)
	if errors.Is(err, missionctx.ErrNotFound) {
		writeError(w, http.StatusNotFound, "mission not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	title := fmt.Sprintf("[Slowpoke] %s", c.Mission.Label)
	if len(title) > 200 {
		title = title[:200] + "…"
	}
	labels := []string{"performance", "tech-debt"}
	if c.Mission.Type == "boss" {
		labels = append(labels, "regression")
	}
	res, err := tasks.Create(r.Context(), *integration, tasks.Issue{Title: title, Body: c.Markdown(), Assignee: strings.TrimSpace(body.Assignee), Labels: labels})
	if err != nil {
		writeError(w, http.StatusBadGateway, integration.Provider+": "+err.Error())
		return
	}
	id, err := s.DB.Q().InsertID(r.Context(), `INSERT INTO mission_tasks (mission_id, integration, provider, external_id, url, assignee, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.Mission.ID, integration.ID, integration.Provider, res.ExternalID, res.URL, strings.TrimSpace(body.Assignee), time.Now().Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "external_id": res.ExternalID, "url": res.URL})
}

func (s *Server) handleUpdateMission(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		Effort *string `json:"effort"`
	}
	if err := decode(r, &body); err != nil || body.Effort == nil {
		writeError(w, http.StatusBadRequest, "effort is required")
		return
	}
	effort := strings.TrimSpace(*body.Effort)
	if effort != "" {
		if _, ok := debt.EffortHours[effort]; !ok {
			writeError(w, http.StatusUnprocessableEntity, "effort must be xs, s, m, l or empty for the estimate")
			return
		}
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	res, err := s.DB.Q().Exec(r.Context(), "UPDATE missions SET effort = ? WHERE id = ? AND project_id = ?", effort, id, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "mission not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleHotspots(w http.ResponseWriter, r *http.Request, p project.Project) {
	spots, err := debt.Hotspots(r.Context(), s.DB, p, time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hotspots": spots})
}

// me is how the person asking is written on a mission: the email, which is also what a deploy
// writes as its author, so the deploy of whoever took a mission is recognised as the fix.
func me(r *http.Request) string {
	if u, ok := r.Context().Value(userKey).(auth.User); ok {
		if u.Email != "" {
			return u.Email
		}
		return u.Name
	}
	return ""
}

func missionError(w http.ResponseWriter, err error) {
	var taken *game.Taken
	switch {
	case errors.As(err, &taken):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "taken_by": taken.By, "since": taken.Since})
	case errors.Is(err, game.ErrNotFound):
		writeError(w, http.StatusNotFound, "mission not found")
	case errors.Is(err, game.ErrClosed):
		writeError(w, http.StatusConflict, "the mission is closed")
	default:
		serverError(w, err)
	}
}

func (s *Server) handleClaimMission(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		Force bool `json:"force"`
	}
	_ = decode(r, &body) // an empty body is a plain claim
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := game.Claim(r.Context(), s.DB, p.ID, id, me(r), body.Force, time.Now()); err != nil {
		missionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDismissMission waves a regression away: it closes with no points and stays quiet for the
// days asked for, while its target keeps counting in the score.
func (s *Server) handleDismissMission(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		Note string `json:"note"`
		Days int    `json:"days"`
	}
	_ = decode(r, &body)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := game.Dismiss(r.Context(), s.DB, p.ID, id, strings.TrimSpace(body.Note), body.Days, time.Now()); err != nil {
		missionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleReleaseMission(w http.ResponseWriter, r *http.Request, p project.Project) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := game.Release(r.Context(), s.DB, p.ID, id); err != nil {
		missionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleShippedMission(w http.ResponseWriter, r *http.Request, p project.Project) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := game.MarkShipped(r.Context(), s.DB, p.ID, id, me(r), time.Now()); err != nil {
		missionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleBusyHours says the hours missions, bosses and deploys are judged on.
func (s *Server) handleBusyHours(w http.ResponseWriter, r *http.Request, p project.Project) {
	b, err := game.BusyHours(r.Context(), s.DB, p, time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"known": b.Known, "manual": b.Manual, "groups": b.Groups(), "text": b.Describe()})
}
