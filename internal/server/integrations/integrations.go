// Package integrations tells, per machine and per source, what Slowpoke can see: complete,
// partial (optional capabilities left off), with a problem, or waiting for data.
package integrations

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

const (
	Complete = "complete"
	Partial  = "partial"
	Problem  = "problem"
	Waiting  = "waiting"
	// Missing is not a fault: it is something the agent found on the machine and nobody reads.
	Missing = "missing"
)

type Check struct {
	Source    string               `json:"source"`
	Type      string               `json:"type"`
	Kind      string               `json:"kind,omitempty"` // for what was found: database, site, pool, cache, app
	Name      string               `json:"name"`
	Status    string               `json:"status"`
	Items     []protocol.CheckItem `json:"items"`
	Facts     map[string]string    `json:"facts"`
	CheckedAt int64                `json:"checked_at"`
	// Guides per item key: how to turn a capability on, or an optional one off.
	Guides map[string]Guide `json:"guides"`
	// Remove takes the whole integration away, including what was created for it.
	Remove []Step `json:"remove"`
}

type Summary struct {
	Total    int `json:"total"`
	Complete int `json:"complete"`
	Partial  int `json:"partial"`
	Problems int `json:"problems"`
	Waiting  int `json:"waiting"`
	Missing  int `json:"missing"`
}

// Status of one source: a failed capability is a problem even when optional (the team asked
// for it and it does not work); optional ones left off only make it partial.
func Status(items []protocol.CheckItem) string {
	status := Complete
	for _, it := range items {
		switch {
		case it.Status == "error", it.Status == "off" && !it.Optional:
			return Problem
		case it.Status == "waiting":
			status = Waiting
		case it.Status == "off" && status == Complete:
			status = Partial
		}
	}
	return status
}

func Summarize(checks []Check) Summary {
	s := Summary{Total: len(checks)}
	for _, c := range checks {
		switch c.Status {
		case Complete:
			s.Complete++
		case Partial:
			s.Partial++
		case Problem:
			s.Problems++
		case Waiting:
			s.Waiting++
		case Missing:
			s.Missing++
		}
	}
	return s
}

// ForProject returns the checks of every machine of the project, by host id.
func ForProject(ctx context.Context, db *store.DB, projectID int64) (map[int64][]Check, error) {
	rows, err := db.Q().Query(ctx, `SELECT c.host_id, c.source, c.items, c.facts, c.checked_at FROM host_checks c
		JOIN hosts h ON h.id = c.host_id WHERE h.project_id = ? ORDER BY c.host_id, c.source`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Check{}
	for rows.Next() {
		var hostID, at int64
		var source, items, facts string
		if err := rows.Scan(&hostID, &source, &items, &facts, &at); err != nil {
			return nil, err
		}
		out[hostID] = append(out[hostID], build(source, items, facts, at))
	}
	return out, rows.Err()
}

// ForHost returns the checks of one machine, which must belong to the project.
func ForHost(ctx context.Context, db *store.DB, projectID, hostID int64) ([]Check, error) {
	all, err := ForProject(ctx, db, projectID)
	if err != nil {
		return nil, err
	}
	if all[hostID] == nil {
		return []Check{}, nil
	}
	return all[hostID], nil
}

func build(source, rawItems, rawFacts string, at int64) Check {
	c := Check{Source: source, Name: source, CheckedAt: at, Items: []protocol.CheckItem{}, Facts: map[string]string{}}
	if kind, name, ok := strings.Cut(source, ":"); ok {
		c.Type, c.Name = kind, name
	}
	// "found:database:mysql": what is on the machine but not configured.
	if c.Type == "found" {
		if kind, name, ok := strings.Cut(c.Name, ":"); ok {
			c.Kind, c.Name = kind, name
		}
	}
	_ = json.Unmarshal([]byte(rawItems), &c.Items)
	_ = json.Unmarshal([]byte(rawFacts), &c.Facts)
	c.Status = Status(c.Items)
	if c.Type == "found" {
		c.Status = Missing
	}
	guides(&c)
	return c
}
