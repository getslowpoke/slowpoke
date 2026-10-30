package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/exclude"
	"github.com/getslowpoke/slowpoke/internal/server/project"
)

// excludedWindow is how far back the cost of an excluded endpoint is read: a week, like the
// debt of everything else.
const excludedWindow = 7 * 24 * time.Hour

var errNotStored = errors.New("the exclusion was not stored")

func (s *Server) handleExclusions(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	list, err := exclude.List(ctx, s.DB, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	now := time.Now()
	costs, err := s.Metrics.ExcludedCosts(ctx, p.ID, now.Add(-excludedWindow), now)
	if err != nil {
		serverError(w, err)
		return
	}
	days := s.Metrics.ObservedDays(ctx, p.ID, excludedWindow, now)
	items := make([]map[string]any, 0, len(list))
	var total float64
	for _, e := range list {
		active := e.Until == nil || *e.Until > now.Unix()
		var seconds float64
		var requests int64
		if active {
			one := exclude.One(e)
			for _, c := range costs {
				if one.Cost(c.Kind, c.Target) {
					seconds += c.Seconds
					requests += c.Count
				}
			}
		}
		total += seconds
		items = append(items, map[string]any{"id": e.ID, "kind": e.Kind, "target": e.Target, "label": e.Label, "reason": e.Reason, "note": e.Note, "created_by": e.CreatedBy,
			"created_at": e.CreatedAt, "until": e.Until, "active": active, "slow_requests": requests, "seconds_per_day": r2(seconds / days)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"exclusions": items, "reasons": exclude.Reasons, "kinds": exclude.Kinds, "excluded_seconds_per_day": r2(total / days)})
}

func (s *Server) handleCreateExclusion(w http.ResponseWriter, r *http.Request, p project.Project) {
	var body struct {
		Kind string `json:"kind"` // endpoint (the default) | query | php
		// Target is the endpoint ("POST /checkout"), the hash of a query or a PHP frame.
		// Endpoint is the older name of the same field, kept for whoever calls the API already.
		Target   string `json:"target"`
		Endpoint string `json:"endpoint"`
		Reason   string `json:"reason"`
		Note     string `json:"note"`
		Days     int    `json:"days"` // 0: until somebody removes it
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	now := time.Now()
	target := body.Target
	if target == "" {
		target = body.Endpoint
	}
	e := exclude.Exclusion{Kind: body.Kind, Target: target, Reason: strings.TrimSpace(body.Reason), Note: strings.TrimSpace(body.Note), CreatedBy: whoAsked(r)}
	if e.Kind == "" {
		e.Kind = exclude.KindEndpoint
	}
	if body.Days < 0 || body.Days > 3650 {
		writeError(w, http.StatusUnprocessableEntity, "days must be between 0 and 3650")
		return
	}
	if body.Days > 0 {
		until := now.Add(time.Duration(body.Days) * 24 * time.Hour).Unix()
		e.Until = &until
	}
	closed, err := exclude.Add(r.Context(), s.DB, p.ID, e, now)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	list, err := exclude.List(r.Context(), s.DB, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for _, x := range list {
		if x.Kind == e.Kind && x.Target == strings.TrimSpace(e.Target) {
			writeJSON(w, http.StatusCreated, map[string]any{"exclusion": x, "closed_missions": closed})
			return
		}
	}
	serverError(w, errNotStored)
}

func (s *Server) handleDeleteExclusion(w http.ResponseWriter, r *http.Request, p project.Project) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := exclude.Remove(r.Context(), s.DB, p.ID, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// exclusionOf is the exclusion in force for one thing, or nil.
func (s *Server) exclusionOf(r *http.Request, p project.Project, kind, target string) *exclude.Exclusion {
	list, err := exclude.List(r.Context(), s.DB, p.ID)
	if err != nil {
		return nil
	}
	now := time.Now().Unix()
	for _, e := range list {
		if (e.Until == nil || *e.Until > now) && e.Kind == kind && exclude.One(e).Has(kind, target) {
			return &e
		}
	}
	return nil
}

// handleRelatedExclusions is what goes with an endpoint the team is leaving out: the same path
// under its other methods, and the PHP stalls waiting on another service. Excluding only the GET
// of an export, or every endpoint of a partner's API but not the curl_exec() they wait in, left
// the rest open as regressions nobody could fix. Only what costs time and is not out already.
func (s *Server) handleRelatedExclusions(w http.ResponseWriter, r *http.Request, p project.Project) {
	ctx := r.Context()
	related := []map[string]any{}
	kind, target := r.URL.Query().Get("kind"), strings.TrimSpace(r.URL.Query().Get("target"))
	if kind != "" && kind != exclude.KindEndpoint || target == "" {
		writeJSON(w, http.StatusOK, map[string]any{"related": related})
		return
	}
	now := time.Now()
	costs, err := s.Metrics.Costs(ctx, p.ID, now.Add(-excludedWindow), now) // already without what is out
	if err != nil {
		serverError(w, err)
		return
	}
	days := s.Metrics.ObservedDays(ctx, p.ID, excludedWindow, now)
	// One endpoint seen by several web servers is one thing to leave out.
	seconds := map[string]float64{}
	labels := map[string]string{}
	var order []string
	for _, c := range costs {
		var why, label string
		switch {
		case c.Kind == exclude.KindEndpoint:
			_, endpoint, _ := strings.Cut(c.Target, " ")
			if !exclude.SamePath(target, endpoint) {
				continue
			}
			why, label = "same_path", endpoint
		case c.Kind == exclude.KindPHP && exclude.OutboundFrame(c.Target):
			why, label = "outbound", c.Target
		default:
			continue
		}
		key := c.Kind + "\x00" + exclude.Key(c.Kind, label) + "\x00" + why
		if _, ok := seconds[key]; !ok {
			order = append(order, key)
			labels[key] = label
		}
		seconds[key] += c.Seconds
	}
	for _, key := range order {
		parts := strings.SplitN(key, "\x00", 3)
		related = append(related, map[string]any{"kind": parts[0], "target": labels[key], "why": parts[2], "seconds_per_day": r2(seconds[key] / days)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"related": related})
}
