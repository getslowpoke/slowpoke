// Package exclude keeps what the team took out of the score, the debt and the missions: an
// endpoint that is a long poll, an export, a webhook or a call that waits on somebody else's
// API, a query of a library nobody can change, a PHP frame inside a vendor's code. Nothing is
// deleted: the measures stay, and the excluded time is shown apart with its reason, so a score
// nobody can trust is never the price of a quiet one.
package exclude

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// Kinds are the things that can be left out, the same words the costs and the missions use.
const (
	KindEndpoint = "endpoint"
	KindQuery    = "query"
	KindPHP      = "php"
)

var Kinds = []string{KindEndpoint, KindQuery, KindPHP}

// Reasons are the ones the UI offers: a reason is what a report shows next to the excluded time.
var Reasons = []string{"external", "third_party", "streaming", "export", "webhook", "other"}

type Exclusion struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`   // endpoint | query | php
	Target string `json:"target"` // "POST /checkout", a query hash, a PHP frame
	// Label is what the panel shows for the target: filled by the API, not stored.
	Label     string `json:"label,omitempty"`
	Reason    string `json:"reason"`
	Note      string `json:"note"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	Until     *int64 `json:"until"` // nil: until somebody removes it
}

func validReason(r string) bool { return contains(Reasons, r) }

func validKind(k string) bool { return contains(Kinds, k) }

func contains(list []string, v string) bool {
	for _, known := range list {
		if v == known {
			return true
		}
	}
	return false
}

// Key makes the same thing written twice one. For an endpoint the route of two frameworks is the
// same endpoint: "GET /orders/{order}", "GET /orders/:id" and nginx's "GET /orders/{id}". A query
// is its fingerprint hash and a PHP frame its own text, both already normalized.
func Key(kind, target string) string {
	target = strings.TrimSpace(target)
	if kind != KindEndpoint {
		return target
	}
	method, path, ok := strings.Cut(target, " ")
	if !ok {
		return target
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range parts {
		if strings.HasPrefix(p, "{") || strings.HasPrefix(p, ":") || strings.HasPrefix(p, "<") {
			parts[i] = "{}"
		}
	}
	return strings.ToUpper(method) + " /" + strings.Join(parts, "/")
}

// Set is the exclusions in force at one moment.
type Set map[string]bool

func setKey(kind, target string) string { return kind + "\x00" + Key(kind, target) }

// Has tells whether one thing is excluded.
func (s Set) Has(kind, target string) bool { return len(s) > 0 && s[setKey(kind, target)] }

// Endpoint tells whether an endpoint ("GET /orders/{id}") is excluded.
func (s Set) Endpoint(endpoint string) bool { return s.Has(KindEndpoint, endpoint) }

// Cost tells whether the target of a cost is excluded. An endpoint cost carries its source
// ("nginx:shop GET /orders/{id}"), which is not part of what the team excluded; the others are
// the target itself.
func (s Set) Cost(kind, target string) bool {
	if kind != KindEndpoint {
		return s.Has(kind, target)
	}
	_, endpoint, ok := strings.Cut(target, " ")
	return ok && s.Endpoint(endpoint)
}

// Target tells whether the target of an endpoint cost is excluded.
func (s Set) Target(target string) bool { return s.Cost(KindEndpoint, target) }

// Of is the set of the given endpoints, to ask about one exclusion at a time.
func Of(endpoints ...string) Set {
	s := Set{}
	for _, e := range endpoints {
		s[setKey(KindEndpoint, e)] = true
	}
	return s
}

// One is the set of a single exclusion.
func One(e Exclusion) Set { return Set{setKey(e.Kind, e.Target): true} }

// Active reads the exclusions in force at a moment: the end of the window being read.
func Active(ctx context.Context, db *store.DB, projectID int64, at time.Time) (Set, error) {
	rows, err := db.Q().Query(ctx, `SELECT kind, target FROM exclusions WHERE project_id = ? AND (until IS NULL OR until > ?)`, projectID, at.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := Set{}
	for rows.Next() {
		var kind, target string
		if err := rows.Scan(&kind, &target); err != nil {
			return nil, err
		}
		set[setKey(kind, target)] = true
	}
	return set, rows.Err()
}

func List(ctx context.Context, db *store.DB, projectID int64) ([]Exclusion, error) {
	rows, err := db.Q().Query(ctx, `SELECT id, kind, target, reason, note, created_by, created_at, until FROM exclusions WHERE project_id = ? ORDER BY created_at DESC, id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Exclusion{}
	for rows.Next() {
		var e Exclusion
		if err := rows.Scan(&e.ID, &e.Kind, &e.Target, &e.Reason, &e.Note, &e.CreatedBy, &e.CreatedAt, &e.Until); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Add leaves something out of the score, or changes the reason of something already out, and
// closes its open missions and bosses as excluded: without it the next evaluation would find the
// cost gone and pay points for a repayment that never happened. It returns how many were closed.
func Add(ctx context.Context, db *store.DB, projectID int64, e Exclusion, now time.Time) (int, error) {
	e.Target = strings.TrimSpace(e.Target)
	if e.Kind == "" {
		e.Kind = KindEndpoint
	}
	if !validKind(e.Kind) {
		return 0, errors.New("unknown kind")
	}
	if e.Target == "" || len(e.Target) > 191 {
		return 0, errors.New("target required")
	}
	if !validReason(e.Reason) {
		return 0, errors.New("unknown reason")
	}
	if len(e.Note) > 500 {
		e.Note = e.Note[:500]
	}
	closed := 0
	err := db.Tx(ctx, func(q store.Q) error {
		_, err := q.Exec(ctx, `INSERT INTO exclusions (project_id, kind, target, reason, note, created_by, created_at, until) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (project_id, kind, target) DO UPDATE SET reason = excluded.reason, note = excluded.note, until = excluded.until`,
			projectID, e.Kind, e.Target, e.Reason, e.Note, e.CreatedBy, now.Unix(), e.Until)
		if err != nil {
			return err
		}
		rows, err := q.Query(ctx, `SELECT id, target FROM missions WHERE project_id = ? AND kind = ? AND status = 'open'`, projectID, e.Kind)
		if err != nil {
			return err
		}
		set := One(e)
		var ids []int64
		for rows.Next() {
			var id int64
			var target string
			if err := rows.Scan(&id, &target); err != nil {
				rows.Close()
				return err
			}
			if set.Cost(e.Kind, target) {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			if _, err := q.Exec(ctx, `UPDATE missions SET status = 'excluded', closed_at = ? WHERE id = ?`, now.Unix(), id); err != nil {
				return err
			}
		}
		closed = len(ids)
		return nil
	})
	return closed, err
}

// Remove puts something back in the score. Its missions do not reopen: the next evaluation opens
// new ones if it is still worth it.
func Remove(ctx context.Context, db *store.DB, projectID, id int64) error {
	_, err := db.Q().Exec(ctx, `DELETE FROM exclusions WHERE project_id = ? AND id = ?`, projectID, id)
	return err
}
