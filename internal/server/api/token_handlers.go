package api

import (
	"strings"
	"errors"
	"net/http"
	"strconv"

	"github.com/getslowpoke/slowpoke/internal/server/auth"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/tokens"
)

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request, p project.Project) {
	list, err := s.Tokens.List(r.Context(), p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": list})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request, u auth.User) {
	p, err := s.Projects.BySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var body struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	userID := int64(0)
	if body.Kind == tokens.KindPersonal {
		userID = u.ID
	}
	t, plain, err := s.Tokens.Create(r.Context(), p.ID, body.Kind, body.Name, userID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// The plain token is shown once: only its hash is stored.
	// A shell script asks for text and gets the token, nothing else to parse.
	if strings.Contains(r.Header.Get("Accept"), "text/plain") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(plain + "\n"))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": t, "secret": plain})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request, p project.Project) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Tokens.Revoke(r.Context(), p.ID, id); errors.Is(err, tokens.ErrNotFound) {
		writeError(w, http.StatusNotFound, "token not found")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDeleteHost removes a machine and its measures; its agent token is revoked
// so a forgotten agent cannot bring it back.
func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request, p project.Project) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var tokenID *int64
	var name string
	if err := s.DB.Q().Row(r.Context(), "SELECT name, token_id FROM hosts WHERE id = ? AND project_id = ?", id, p.ID).Scan(&name, &tokenID); err != nil {
		writeError(w, http.StatusNotFound, "host not found")
		return
	}
	if tokenID != nil {
		if err := s.Tokens.Revoke(r.Context(), p.ID, *tokenID); err != nil && !errors.Is(err, tokens.ErrNotFound) {
			serverError(w, err)
			return
		}
	}
	if _, err := s.DB.Q().Exec(r.Context(), "DELETE FROM hosts WHERE id = ? AND project_id = ?", id, p.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name, "token_revoked": tokenID != nil})
}
