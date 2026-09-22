package api

// Updates. Two different problems, two different answers.
//
// The machines are easy: they already talk to this server, and this server hands out the agent
// it was built with, so nothing has to leave the network. The panel just lists which agents are
// older than the server.
//
// The server itself is the hard one: knowing that a newer Slowpoke exists means asking someone
// outside. So it is a button, never a background check: one GET to the release feed, when
// somebody presses it, carrying nothing about this installation. And it only ever *tells* you
// the command to run — a self-hosted panel does not download a binary from the internet and put
// it in place of itself.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/auth"
)

// DefaultUpdateFeed is the public list of releases. Empty in SLOWPOKE_UPDATE_FEED turns the
// button off for people who want a panel that never talks to anything.
const DefaultUpdateFeed = "https://api.github.com/repos/getslowpoke/slowpoke/releases/latest"

func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request, _ auth.User) {
	rows, err := s.DB.Q().Query(r.Context(),
		`SELECT h.name, h.version, p.slug, h.muted_at IS NOT NULL FROM hosts h JOIN projects p ON p.id = h.project_id WHERE h.kind = 'agent' ORDER BY h.name`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	machines := []map[string]any{}
	behind := 0
	for rows.Next() {
		var name, version, slug string
		var muted bool
		if err := rows.Scan(&name, &version, &slug, &muted); err != nil {
			serverError(w, err)
			return
		}
		outdated := version != "" && version != Version
		// A muted machine is left as it is on purpose: it is listed, not counted as behind.
		if outdated && !muted {
			behind++
		}
		machines = append(machines, map[string]any{"name": name, "version": version, "project": slug, "outdated": outdated, "muted": muted})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   Version,
		"machines":  machines,
		"behind":    behind,
		"can_check": s.UpdateFeed != "",
		"feed":      s.UpdateFeed,
		// How this server was installed, so the panel shows the command that applies here.
		"in_container": inContainer(),
		"serves_agent": s.agentDownload("linux-amd64") != "" || s.agentDownload("linux-arm64") != "",
	})
}

type release struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	URL         string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request, _ auth.User) {
	if s.UpdateFeed == "" {
		writeError(w, http.StatusUnprocessableEntity, "no release feed is configured (SLOWPOKE_UPDATE_FEED)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.UpdateFeed, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid feed address")
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "slowpoke/"+Version)

	res, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cannot reach the release feed: "+err.Error())
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "the release feed answered HTTP "+strconv.Itoa(res.StatusCode))
		return
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&rel); err != nil {
		writeError(w, http.StatusBadGateway, "the release feed is not readable")
		return
	}

	latest := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	writeJSON(w, http.StatusOK, map[string]any{
		"running":      Version,
		"latest":       latest,
		"name":         rel.Name,
		"url":          rel.URL,
		"published_at": rel.PublishedAt,
		"newer":        isNewer(Version, rel.TagName),
		"checked_at":   time.Now().Unix(),
	})
}

// baseVersion is the "1.2.3" of "1.2.3-3af334d".
func baseVersion(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i > 0 {
		v = v[:i]
	}
	return v
}

// isNewer compares the numbers and nothing else: a build from source ("0.1.0-3af334d") is the
// same 0.1.0, because what it carries beyond the tag is not something a feed can know.
func isNewer(running, latest string) bool {
	a, b := parts(baseVersion(running)), parts(baseVersion(latest))
	if a == nil || b == nil {
		return false
	}
	for i := range b {
		switch {
		case b[i] > a[i]:
			return true
		case b[i] < a[i]:
			return false
		}
	}
	return false
}

func parts(v string) []int {
	fields := strings.Split(v, ".")
	if len(fields) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// inContainer tells the panel which update command applies to this server.
func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(b)
		return strings.Contains(s, "docker") || strings.Contains(s, "containerd") || strings.Contains(s, "kubepods")
	}
	return false
}
