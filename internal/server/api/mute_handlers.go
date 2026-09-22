package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/auth"
	"github.com/getslowpoke/slowpoke/internal/server/mute"
	"github.com/getslowpoke/slowpoke/internal/server/project"
)

// whoAsked is the person behind a request, as shown next to what they muted.
func whoAsked(r *http.Request) string {
	u, ok := r.Context().Value(userKey).(auth.User)
	if !ok {
		return ""
	}
	if u.Email != "" {
		return u.Email
	}
	return u.Name
}

func (s *Server) handleMuteHost(w http.ResponseWriter, r *http.Request, p project.Project) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var body struct {
		Note string `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	err := mute.SetHost(r.Context(), s.DB, p.ID, id, whoAsked(r), body.Note, time.Now())
	if errors.Is(err, mute.ErrNotFound) {
		writeError(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	muted, err := mute.HostMuted(r.Context(), s.DB, p.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"muted": muted})
}

func (s *Server) handleUnmuteHost(w http.ResponseWriter, r *http.Request, p project.Project) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := mute.ClearHost(r.Context(), s.DB, p.ID, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMuteDiagnosis(w http.ResponseWriter, r *http.Request, p project.Project) {
	hash := r.PathValue("hash")
	var body struct {
		Code  string `json:"code"`
		Table string `json:"table"`
		Note  string `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var n int
	if err := s.DB.Q().Row(r.Context(), "SELECT COUNT(*) FROM query_fingerprints WHERE project_id = ? AND hash = ?", p.ID, hash).Scan(&n); err != nil {
		serverError(w, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "query not found")
		return
	}
	m, err := mute.AddDiagnosis(r.Context(), s.DB, p.ID, mute.Diagnosis{Hash: hash, Code: body.Code, Table: body.Table, Note: body.Note, CreatedBy: whoAsked(r)}, time.Now())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"mute": m})
}

func (s *Server) handleUnmuteDiagnosis(w http.ResponseWriter, r *http.Request, p project.Project) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := mute.RemoveDiagnosis(r.Context(), s.DB, p.ID, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
