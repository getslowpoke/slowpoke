// Package missionctx gathers everything known about a mission: what is slow,
// where it lives in the code, why, how to fix it and how its cost evolved.
// It feeds the mission page, task descriptions and the MCP server used by
// coding assistants.
package missionctx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/debt"
	"github.com/getslowpoke/slowpoke/internal/server/doctor"
	"github.com/getslowpoke/slowpoke/internal/server/game"
	"github.com/getslowpoke/slowpoke/internal/server/metrics"
	"github.com/getslowpoke/slowpoke/internal/server/mute"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

var ErrNotFound = errors.New("mission not found")

type Mission struct {
	ID          int64    `json:"id"`
	Type        string   `json:"type"`
	Kind        string   `json:"kind"`
	Target      string   `json:"target"`
	Label       string   `json:"label"`
	Status      string   `json:"status"`
	Points      int      `json:"points"`
	Baseline    float64  `json:"baseline"`
	Current     *float64 `json:"current"`
	GoodWindows int      `json:"good_windows"`
	NeedWindows int      `json:"need_windows"`
	WindowHours float64  `json:"window_hours"`
	Person      string   `json:"person"`
	OpenedAt    int64    `json:"opened_at"`
	DueAt       *int64   `json:"due_at"`
	ClosedAt    *int64   `json:"closed_at"`
}

type Window struct {
	From    int64   `json:"from"`
	Seconds float64 `json:"seconds"`
	Count   int64   `json:"count"`
}

type RouteUse struct {
	Route      string  `json:"route"`
	Calls      int64   `json:"calls"`
	Requests   int64   `json:"requests"`
	PerRequest float64 `json:"per_request"`
}

type Query struct {
	Hash        string             `json:"hash"`
	Fingerprint string             `json:"fingerprint"`
	Sample      string             `json:"sample"`
	Database    string             `json:"database"`
	Origin      string             `json:"origin"`
	NPlusOne    int64              `json:"n_plus_one"`
	Stats       doctor.Stats       `json:"stats"`
	Explain     *doctor.Explain    `json:"explain"`
	Diagnoses   []doctor.Diagnosis `json:"diagnoses"`
	// Muted is the advice the team chose not to follow, with why: the assistant must not
	// propose it again.
	Muted  []mute.Muted `json:"muted_diagnoses"`
	Routes []RouteUse   `json:"routes"`
}

type EndpointQuery struct {
	Hash        string  `json:"hash"`
	Fingerprint string  `json:"fingerprint"`
	Route       string  `json:"route"`
	Origin      string  `json:"origin"`
	PerRequest  float64 `json:"per_request"`
	Seconds     float64 `json:"seconds"`
	NPlusOne    int64   `json:"n_plus_one"`
}

type Endpoint struct {
	Source   string          `json:"source"`
	Endpoint string          `json:"endpoint"`
	Requests int64           `json:"requests"`
	Avg      float64         `json:"avg"`
	Max      float64         `json:"max"`
	Slow     int64           `json:"slow"`
	E5       int64           `json:"e5"`
	Queries  []EndpointQuery `json:"queries"`
	// What its requests waited on other services, when a framework package measured it.
	Outbound *metrics.Outbound `json:"outbound,omitempty"`
}

type Stall struct {
	Frame string `json:"frame"`
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

type Metric struct {
	Name  string  `json:"name"`
	Kind  string  `json:"kind"`
	Count int64   `json:"count"`
	Avg   float64 `json:"avg"`
	P95   float64 `json:"p95"`
	Max   float64 `json:"max"`
}

type Deploy struct {
	ID         int64  `json:"id"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	Author     string `json:"author"`
	Message    string `json:"message"`
	DeployedAt int64  `json:"deployed_at"`
}

type Task struct {
	ID          int64  `json:"id"`
	Integration string `json:"integration"`
	Provider    string `json:"provider"`
	ExternalID  string `json:"external_id"`
	URL         string `json:"url"`
	Assignee    string `json:"assignee"`
	CreatedAt   int64  `json:"created_at"`
}

type Context struct {
	Project      string    `json:"project"`
	ProjectSlug  string    `json:"project_slug"`
	URL          string    `json:"url"`
	Mission      Mission   `json:"mission"`
	CostPerDay   float64   `json:"cost_seconds_per_day"`
	TargetPerDay float64   `json:"target_seconds_per_day"`
	History      []Window  `json:"history"`
	Query        *Query    `json:"query,omitempty"`
	Endpoint     *Endpoint `json:"endpoint,omitempty"`
	Stalls       []Stall   `json:"stalls,omitempty"`
	Metric       *Metric   `json:"metric,omitempty"`
	Deploy       *Deploy   `json:"deploy,omitempty"`
	Tasks        []Task    `json:"tasks"`
	// Where it stands: who is on it, whether the fix is online, whether it held.
	Life game.Life `json:"life"`
	Effort       string    `json:"effort"`
	EffortAuto   bool      `json:"effort_auto"`
	ROI          float64   `json:"roi"`
}

type Options struct {
	PublicURL string
	Now       time.Time
	// Windows of history to return (default 14).
	Windows int
}

func Build(ctx context.Context, db *store.DB, p project.Project, missionID int64, o Options) (Context, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Windows <= 0 {
		o.Windows = 14
	}
	c := Context{Project: p.Name, ProjectSlug: p.Slug, Tasks: []Task{}}
	m := &c.Mission
	var deployID sql.NullInt64
	err := db.Q().Row(ctx, `SELECT id, type, kind, target, label, status, points, baseline, current, good_windows, person, opened_at, due_at, closed_at, deploy_id, effort
		FROM missions WHERE id = ? AND project_id = ?`, missionID, p.ID).Scan(&m.ID, &m.Type, &m.Kind, &m.Target, &m.Label, &m.Status, &m.Points,
		&m.Baseline, &m.Current, &m.GoodWindows, &m.Person, &m.OpenedAt, &m.DueAt, &m.ClosedAt, &deployID, &c.Effort)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	m.NeedWindows = p.Settings.Missions.GoodWindows
	m.WindowHours = p.Settings.Missions.WindowHours
	if o.PublicURL != "" {
		c.URL = fmt.Sprintf("%s/projects/%s/missions/%d", strings.TrimRight(o.PublicURL, "/"), p.Slug, m.ID)
	}
	perDay := 24 / m.WindowHours
	cost := m.Baseline
	if m.Current != nil {
		cost = *m.Current
	}
	c.CostPerDay = math.Round(cost * perDay)
	if m.Type == "boss" {
		c.TargetPerDay = math.Round(m.Baseline * 1.5 * perDay)
	} else {
		c.TargetPerDay = math.Round(m.Baseline * p.Settings.Missions.TargetRatio * perDay)
	}

	reader := metrics.Reader{DB: db}
	window := time.Duration(m.WindowHours) * time.Hour
	end := o.Now.Truncate(time.Hour)
	for i := o.Windows - 1; i >= 0; i-- {
		to := end.Add(-time.Duration(i) * window)
		from := to.Add(-window)
		costs, err := reader.Costs(ctx, p.ID, from, to)
		if err != nil {
			return c, err
		}
		w := Window{From: from.Unix()}
		for _, x := range costs {
			if x.Kind == m.Kind && x.Target == m.Target {
				w.Seconds, w.Count = math.Round(x.Seconds*100)/100, x.Count
			}
		}
		c.History = append(c.History, w)
	}

	switch m.Kind {
	case "query":
		q, err := LoadQuery(ctx, db, p, m.Target, o.Now)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return c, err
		}
		c.Query = q
	case "endpoint":
		ep, err := loadEndpoint(ctx, db, p, m.Target, o.Now)
		if err != nil {
			return c, err
		}
		c.Endpoint = ep
	case "metric":
		snap, err := reader.Snapshot(ctx, p.ID, o.Now.Add(-7*24*time.Hour), o.Now)
		if err != nil {
			return c, err
		}
		if cm, ok := snap.Custom[m.Target]; ok && cm.Count > 0 {
			c.Metric = &Metric{Name: m.Target, Kind: cm.Kind, Count: cm.Count, Avg: cm.Sum / float64(cm.Count), P95: cm.Percentile(0.95), Max: cm.Max}
		}
	case "php":
		rows, err := db.Q().Query(ctx, `SELECT frame, code, SUM(n) FROM php_stall_hours WHERE project_id = ? AND frame = ? AND hour >= ? GROUP BY frame, code ORDER BY SUM(n) DESC LIMIT 10`,
			p.ID, m.Target, o.Now.Add(-7*24*time.Hour).Unix())
		if err != nil {
			return c, err
		}
		for rows.Next() {
			var s Stall
			if err := rows.Scan(&s.Frame, &s.Code, &s.Count); err == nil {
				c.Stalls = append(c.Stalls, s)
			}
		}
		rows.Close()
	}

	if deployID.Valid {
		d := Deploy{ID: deployID.Int64}
		if err := db.Q().Row(ctx, "SELECT version, commit_sha, author, message, deployed_at FROM deploys WHERE id = ?", deployID.Int64).
			Scan(&d.Version, &d.Commit, &d.Author, &d.Message, &d.DeployedAt); err == nil {
			c.Deploy = &d
		}
	}

	if c.Effort == "" {
		var ds []doctor.Diagnosis
		if c.Query != nil {
			ds = c.Query.Diagnoses
		}
		c.Effort = debt.EstimateEffort(m.Kind, m.Type, ds)
		c.EffortAuto = true
	}
	c.ROI = debt.ROI(c.CostPerDay, c.Effort)

	rows, err := db.Q().Query(ctx, "SELECT id, integration, provider, external_id, url, assignee, created_at FROM mission_tasks WHERE mission_id = ? ORDER BY id", m.ID)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Integration, &t.Provider, &t.ExternalID, &t.URL, &t.Assignee, &t.CreatedAt); err == nil {
			c.Tasks = append(c.Tasks, t)
		}
	}
	rows.Close()
	lives, err := game.Lives(ctx, db, []int64{m.ID})
	if err != nil {
		return c, err
	}
	c.Life = lives[m.ID]
	return c, nil
}

// LoadQuery returns what is known about a query fingerprint, with the doctor's diagnosis.
func LoadQuery(ctx context.Context, db *store.DB, p project.Project, hash string, now time.Time) (*Query, error) {
	return NewQueries(db, p, now).Load(ctx, hash)
}

// Queries loads several queries against one read of the project's costs. The costs cover every target of
// the project: reading them again for each query made a list of 50 missions take 17 s on a real project.
type Queries struct {
	db    *store.DB
	p     project.Project
	now   time.Time
	costs []metrics.Cost
	days  float64
	read  bool
}

func NewQueries(db *store.DB, p project.Project, now time.Time) *Queries {
	return &Queries{db: db, p: p, now: now}
}

// Load returns what is known about one query fingerprint, with the doctor's diagnosis.
func (qs *Queries) Load(ctx context.Context, hash string) (*Query, error) {
	db, p, now := qs.db, qs.p, qs.now
	q := &Query{Hash: hash, Routes: []RouteUse{}}
	var id int64
	var sample, explain sql.NullString
	err := db.Q().Row(ctx, `SELECT id, fingerprint, sample, db_name, explain, origin, n_plus_one FROM query_fingerprints WHERE project_id = ? AND hash = ?`, p.ID, hash).
		Scan(&id, &q.Fingerprint, &sample, &q.Database, &explain, &q.Origin, &q.NPlusOne)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	q.Sample = sample.String

	if !qs.read {
		costs, err := metrics.Reader{DB: db}.Costs(ctx, p.ID, now.Add(-7*24*time.Hour), now)
		if err != nil {
			return nil, err
		}
		qs.costs, qs.days, qs.read = costs, metrics.Reader{DB: db}.ObservedDays(ctx, p.ID, 7*24*time.Hour, now), true
	}
	var rows7 int64
	_ = db.Q().Row(ctx, "SELECT COALESCE(SUM(rows),0) FROM query_hours WHERE fingerprint_id = ? AND hour >= ?", id, now.Add(-7*24*time.Hour).Unix()).Scan(&rows7)
	days := qs.days
	for _, c := range qs.costs {
		if c.Kind == "query" && c.Target == hash {
			q.Stats = doctor.Stats{CallsPerDay: float64(c.Count) / days, SecondsPerDay: c.Seconds / days}
			if c.Count > 0 {
				q.Stats.AvgSeconds = c.Seconds / float64(c.Count)
				q.Stats.RowsExaminedAvg = float64(rows7) / float64(c.Count)
			}
		}
	}
	if explain.Valid {
		var e doctor.Explain
		if json.Unmarshal([]byte(explain.String), &e) == nil {
			q.Explain = &e
		}
	}
	mutes, err := mute.DiagnosesOf(ctx, db, p.ID, hash)
	if err != nil {
		return nil, err
	}
	q.Diagnoses, q.Muted = mute.Split(doctor.Analyze(doctor.Input{Fingerprint: q.Fingerprint, Sample: q.Sample, Explain: q.Explain, Stats: q.Stats, Origin: q.Origin, NPlusOne: q.NPlusOne}), mutes)

	rs, err := db.Q().Query(ctx, `SELECT route, n, requests FROM route_queries WHERE fingerprint_id = ? ORDER BY sum DESC LIMIT 20`, id)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		var r RouteUse
		if err := rs.Scan(&r.Route, &r.Calls, &r.Requests); err == nil {
			r.PerRequest = math.Round(float64(r.Calls)/math.Max(1, float64(r.Requests))*10) / 10
			q.Routes = append(q.Routes, r)
		}
	}
	rs.Close()
	return q, nil
}

func loadEndpoint(ctx context.Context, db *store.DB, p project.Project, target string, now time.Time) (*Endpoint, error) {
	source, endpoint, _ := strings.Cut(target, " ")
	ep := &Endpoint{Source: source, Endpoint: endpoint, Queries: []EndpointQuery{}}
	var sum float64
	err := db.Q().Row(ctx, `SELECT COALESCE(SUM(n),0), COALESCE(SUM(sum),0), COALESCE(MAX(max),0), COALESCE(SUM(slow),0), COALESCE(SUM(e5),0) FROM endpoint_hours
		WHERE project_id = ? AND source = ? AND endpoint = ? AND hour >= ?`, p.ID, source, endpoint, now.Add(-7*24*time.Hour).Unix()).Scan(&ep.Requests, &sum, &ep.Max, &ep.Slow, &ep.E5)
	if err != nil {
		return nil, err
	}
	if ep.Requests > 0 {
		ep.Avg = math.Round(sum/float64(ep.Requests)*1000) / 1000
	}

	if out, err := (metrics.Reader{DB: db}).Outbound(ctx, p.ID, endpoint, now.Add(-7*24*time.Hour), now); err != nil {
		return nil, err
	} else if out.Seconds > 0 {
		ep.Outbound = &out
	}

	method, path, _ := strings.Cut(endpoint, " ")
	rows, err := db.Q().Query(ctx, `SELECT rq.route, f.hash, f.fingerprint, f.origin, rq.n, rq.sum, rq.requests, f.n_plus_one FROM route_queries rq
		JOIN query_fingerprints f ON f.id = rq.fingerprint_id WHERE rq.project_id = ? AND rq.route LIKE ? ORDER BY rq.sum DESC LIMIT 50`, p.ID, method+" %")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var q EndpointQuery
		var n, requests int64
		if err := rows.Scan(&q.Route, &q.Hash, &q.Fingerprint, &q.Origin, &n, &q.Seconds, &requests, &q.NPlusOne); err != nil {
			continue
		}
		if !RouteMatches(q.Route, method, path) {
			continue
		}
		q.PerRequest = math.Round(float64(n)/math.Max(1, float64(requests))*10) / 10
		ep.Queries = append(ep.Queries, q)
	}
	sort.SliceStable(ep.Queries, func(i, j int) bool { return ep.Queries[i].Seconds > ep.Queries[j].Seconds })
	return ep, rows.Err()
}

// RouteMatches compares a framework route ("GET /orders/{order}") with a normalized path ("/orders/{id}").
func RouteMatches(route, method, path string) bool {
	parts := strings.SplitN(route, " ", 2)
	if len(parts) != 2 || parts[0] != method {
		return false
	}
	norm := func(s string) []string { return strings.Split(strings.Trim(s, "/"), "/") }
	a, b := norm(parts[1]), norm(path)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		wa := strings.HasPrefix(a[i], "{") || strings.HasPrefix(a[i], ":") || strings.HasPrefix(a[i], "<")
		wb := strings.HasPrefix(b[i], "{")
		if wa != wb || (!wa && a[i] != b[i]) {
			return false
		}
	}
	return true
}

func human(seconds float64) string {
	s := int64(math.Round(seconds))
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh %dm", s/3600, (s%3600)/60)
	case s >= 60:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%ds", s)
}

// Markdown is the brief for a person or a coding assistant: self-contained, no real values.
func (c Context) Markdown() string {
	var b strings.Builder
	m := c.Mission
	kind := map[string]string{"query": "Slow query", "endpoint": "Slow endpoint", "php": "PHP waiting", "metric": "Slow operation"}[m.Kind]
	if m.Type == "boss" {
		kind = "Regression · " + kind
	}
	fmt.Fprintf(&b, "# %s: %s\n\n", kind, m.Label)
	fmt.Fprintf(&b, "- Project: %s · mission #%d · status %s · %d points\n", c.Project, m.ID, m.Status, m.Points)
	if c.Life.Stage != "" {
		fmt.Fprintf(&b, "- Where it stands: %s\n", game.StageInWords(c.Life, m.Person, m.NeedWindows, m.GoodWindows))
	}
	fmt.Fprintf(&b, "- Cost now: **%s of waiting per day**; target: under %s per day\n", human(c.CostPerDay), human(c.TargetPerDay))
	if c.Effort != "" {
		source := "estimated"
		if !c.EffortAuto {
			source = "set by the team"
		}
		fmt.Fprintf(&b, "- Effort: %s (%s, ~%.0fh) · return: %s removed per day for each hour of work\n", strings.ToUpper(c.Effort), source, debt.EffortHours[c.Effort], human(c.ROI))
	}
	if c.URL != "" {
		fmt.Fprintf(&b, "- Slowpoke: %s\n", c.URL)
	}
	if c.Deploy != nil {
		fmt.Fprintf(&b, "- Appeared after deploy %s by %s: %s\n", firstNonEmpty(c.Deploy.Version, short(c.Deploy.Commit)), c.Deploy.Author, c.Deploy.Message)
	}

	if q := c.Query; q != nil {
		b.WriteString("\n## The query\n```sql\n")
		if q.Sample != "" {
			b.WriteString(q.Sample)
		} else {
			b.WriteString(q.Fingerprint)
		}
		b.WriteString("\n```\n")
		fmt.Fprintf(&b, "- %.0f calls/day, %.3f s on average, %s lost per day", q.Stats.CallsPerDay, q.Stats.AvgSeconds, human(q.Stats.SecondsPerDay))
		if q.Stats.RowsExaminedAvg > 0 {
			fmt.Fprintf(&b, ", %.0f rows examined per call", q.Stats.RowsExaminedAvg)
		}
		b.WriteString("\n")
		if q.Origin != "" {
			fmt.Fprintf(&b, "- Called from `%s`\n", q.Origin)
		}
		if q.NPlusOne > 0 {
			fmt.Fprintf(&b, "- Runs as **N+1** in %d requests\n", q.NPlusOne)
		}
		if len(q.Routes) > 0 {
			b.WriteString("- Routes:\n")
			for _, r := range q.Routes {
				fmt.Fprintf(&b, "  - `%s`: %s per request\n", r.Route, trim(r.PerRequest))
			}
		}
		writeDiagnoses(&b, q.Diagnoses)
		writeMuted(&b, q.Muted)
		if q.Explain != nil {
			for name, t := range q.Explain.Tables {
				fmt.Fprintf(&b, "\n### Table `%s` (~%d rows)\n", name, t.Rows)
				for _, ix := range t.Indexes {
					fmt.Fprintf(&b, "- index %s (%s)\n", ix.Name, strings.Join(ix.Columns, ", "))
				}
			}
		}
	}

	if ep := c.Endpoint; ep != nil {
		fmt.Fprintf(&b, "\n## The endpoint\n`%s` on %s: %d requests in 7 days, %.3f s on average, max %.2f s, %d slow, %d errors 5xx\n", ep.Endpoint, ep.Source, ep.Requests, ep.Avg, ep.Max, ep.Slow, ep.E5)
		if len(ep.Queries) > 0 {
			b.WriteString("\n### Queries it runs\n")
			for _, q := range ep.Queries {
				fmt.Fprintf(&b, "- `%s` — %s per request", q.Fingerprint, trim(q.PerRequest))
				if q.NPlusOne > 0 {
					b.WriteString(" · **N+1**")
				}
				if q.Origin != "" {
					fmt.Fprintf(&b, " · `%s`", q.Origin)
				}
				b.WriteString("\n")
			}
		}
		if out := ep.Outbound; out != nil {
			fmt.Fprintf(&b, "\n### Calls to other services\n%.0f%% of its time is spent waiting on outbound HTTP calls (%d of %d requests make at least one).\n",
				out.Share*100, out.WithCalls, out.Requests)
			for _, h := range out.Hosts {
				fmt.Fprintf(&b, "- `%s` — %s per request, %.3f s on average, max %.2f s", h.Host, trim(h.PerRequest), h.Avg, h.Max)
				if h.Errors > 0 {
					fmt.Fprintf(&b, ", %d failed", h.Errors)
				}
				if h.Origin != "" {
					fmt.Fprintf(&b, " · `%s`", h.Origin)
				}
				b.WriteString("\n")
			}
			if out.Share >= 0.5 {
				b.WriteString("\nThe database is not the problem here. The usual repairs: a short timeout (and a retry with backoff) on the client, " +
					"a cache of the answer when it does not change at every request, parallel calls instead of one after the other, " +
					"or moving the call to a queued job and answering the user without waiting for it.\n")
			}
		}
	}

	if len(c.Stalls) > 0 {
		b.WriteString("\n## Where PHP waits\n")
		for _, s := range c.Stalls {
			fmt.Fprintf(&b, "- `%s` from `%s` (%d times in 7 days)\n", s.Frame, s.Code, s.Count)
		}
	}

	if mt := c.Metric; mt != nil {
		fmt.Fprintf(&b, "\n## The operation\nCustom %s `%s`: %d measures in 7 days, average %.3f s, p95 %.3f s, max %.2f s.\nFind where it is measured (e.g. `measure('%s', ...)`) and speed up the wrapped code.\n", mt.Kind, mt.Name, mt.Count, mt.Avg, mt.P95, mt.Max, mt.Name)
	}

	b.WriteString("\n## Cost per window\n")
	for _, w := range c.History {
		fmt.Fprintf(&b, "- %s: %s\n", time.Unix(w.From, 0).UTC().Format("Jan 2 15:04"), human(w.Seconds))
	}

	b.WriteString("\n## Definition of done\n")
	b.WriteString("1. A test reproduces the problem (query count or duration) and fails before the fix.\n")
	b.WriteString("2. The fix keeps the behaviour; the test passes.\n")
	b.WriteString("3. Schema changes are migrations that do not lock big tables.\n")
	fmt.Fprintf(&b, "4. After the deploy Slowpoke measures the cost under %s per day and closes the mission.\n", human(c.TargetPerDay))
	return b.String()
}

func writeDiagnoses(b *strings.Builder, ds []doctor.Diagnosis) {
	if len(ds) == 0 {
		return
	}
	b.WriteString("\n## Diagnosis\n")
	for _, d := range ds {
		fmt.Fprintf(b, "- **[%s] %s**: %s\n", d.Severity, d.Title, d.Detail)
		if d.Fix != "" {
			if strings.HasPrefix(d.Fix, "ALTER") || strings.HasPrefix(d.Fix, "CREATE") {
				fmt.Fprintf(b, "  ```sql\n  %s\n  ```\n", d.Fix)
			} else {
				fmt.Fprintf(b, "  Fix: %s\n", d.Fix)
			}
		}
	}
}

func writeMuted(b *strings.Builder, ms []mute.Muted) {
	if len(ms) == 0 {
		return
	}
	b.WriteString("\n## Muted by the team\nThe team chose not to follow this advice: do not propose it again.\n")
	for _, m := range ms {
		fmt.Fprintf(b, "- %s", m.Title)
		if m.Mute.Note != "" {
			fmt.Fprintf(b, " (%s)", m.Mute.Note)
		}
		b.WriteString("\n")
	}
}

func trim(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
