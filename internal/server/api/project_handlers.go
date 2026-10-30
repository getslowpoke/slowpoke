package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/auth"
	"github.com/getslowpoke/slowpoke/internal/server/game"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/tasks"
)

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request, _ auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":             Version,
		"metrics":             project.Metrics,
		"notification_events": project.NotificationEvents,
		"defaults":            project.Defaults(),
		"histogram_edges":     protocol.HistogramEdges,
	})
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request, _ auth.User) {
	list, err := s.Projects.List(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	type item struct {
		project.Project
		Score *float64 `json:"score"`
		Level any      `json:"level"`
	}
	out := []item{}
	for _, p := range list {
		p.Token = ""
		p.Settings = p.Settings.Masked()
		it := item{Project: p}
		if score := game.CurrentScore(r.Context(), s.DB.Q(), p.ID); score != nil {
			it.Score = score
			it.Level = p.Settings.LevelFor(*score)
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request, _ auth.User) {
	var body struct {
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	p, err := s.Projects.Create(r.Context(), body.Name, body.Timezone)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": p})
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request, p project.Project) {
	p.Settings = p.Settings.Masked()
	writeJSON(w, http.StatusOK, map[string]any{"project": p, "badges": game.BadgeDefs(p.Settings), "task_providers": tasks.Providers})
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		Name     *string           `json:"name"`
		Timezone *string           `json:"timezone"`
		Settings *project.Settings `json:"settings"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		p.Name = *body.Name
	}
	if body.Timezone != nil {
		if _, err := time.LoadLocation(*body.Timezone); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid timezone")
			return
		}
		p.Timezone = *body.Timezone
	}
	if body.Settings != nil {
		stored := p.Settings
		p.Settings = *body.Settings
		p.Settings.RestoreSecrets(stored)
	}
	if err := s.Projects.Update(r.Context(), p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	p.Settings = p.Settings.Masked()
	writeJSON(w, http.StatusOK, map[string]any{"project": p})
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request, p project.Project) {
	if err := s.Projects.Delete(r.Context(), p.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRotateToken(w http.ResponseWriter, r *http.Request, p project.Project) {
	token, err := s.Projects.RotateToken(r.Context(), p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *Server) handleCreateDeploy(w http.ResponseWriter, r *http.Request, p project.Project) {
	var d protocol.Deploy
	if err := decode(r, &d); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if d.Version == "" && d.Commit == "" {
		writeError(w, http.StatusUnprocessableEntity, errors.New("version or commit required").Error())
		return
	}
	id, err := s.insertDeploy(r, p, d)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}
