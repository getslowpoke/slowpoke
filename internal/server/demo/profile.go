package demo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/game"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// A profile is the measured history of a real installation, table by table, without anything
// that belongs to people: no users, tokens, settings, integrations, query samples or links.
// Renamed with a dictionary it becomes a demo project with real numbers: the same traffic, the
// same slow queries, the same deploys, replayed on today's dates.
type Profile struct {
	Version  int     `json:"version"`
	Timezone string  `json:"timezone"`
	From     int64   `json:"from"`
	To       int64   `json:"to"`
	Tables   []Table `json:"tables"`
}

type Table struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

func (p *Profile) table(name string) *Table {
	for i := range p.Tables {
		if p.Tables[i].Name == name {
			return &p.Tables[i]
		}
	}
	return nil
}

// profileTable says how a table travels. Only the tables listed here leave the database: what
// the game derives (missions, points, days, badges, activities) is played again on import.
type profileTable struct {
	name  string
	host  bool     // scoped by host_id instead of project_id
	when  string   // the time a row happened: the import feeds the history hour by hour
	times []string // every column holding a time, shifted to today's dates
	drop  []string // columns that stay home
	key   []string // the primary key: rows renamed to the same key become one
}

var profileTables = []profileTable{
	{name: "hosts", times: []string{"last_seen_at", "network_at", "muted_at"}, drop: []string{"token_id"}},
	{name: "query_fingerprints", times: []string{"first_seen_at", "last_seen_at", "explained_at", "explain_requested_at"}, drop: []string{"sample"}},
	{name: "host_connections", host: true, key: []string{"host_id", "direction", "remote_ip", "port", "process"}},
	{name: "host_checks", host: true, times: []string{"checked_at"}, key: []string{"host_id", "source"}},
	{name: "source_sites", times: []string{"last_seen_at"}, key: []string{"source", "site"}},
	{name: "node_labels", times: []string{"updated_at"}, key: []string{"node_id"}},
	{name: "muted_diagnoses", times: []string{"created_at"}, drop: []string{"id"}, key: []string{"hash", "code", "table_name"}},
	{name: "exclusions", when: "created_at", times: []string{"created_at", "until"}, drop: []string{"id"}, key: []string{"kind", "target"}},
	{name: "deploys", when: "deployed_at", times: []string{"deployed_at"}, drop: []string{"id", "url", "impact"}},
	{name: "http_minutes", when: "bucket", times: []string{"bucket"}, key: []string{"source", "bucket"}},
	{name: "endpoint_hours", when: "hour", times: []string{"hour"}, key: []string{"source", "endpoint", "hour"}},
	{name: "query_hours", when: "hour", times: []string{"hour"}, key: []string{"fingerprint_id", "source", "host_id", "hour"}},
	{name: "route_queries", when: "last_seen_at", times: []string{"last_seen_at"}, key: []string{"route", "fingerprint_id"}},
	{name: "db_minutes", when: "bucket", times: []string{"bucket"}, key: []string{"host_id", "bucket"}},
	{name: "php_stall_hours", when: "hour", times: []string{"hour"}, key: []string{"source", "hour", "frame", "code"}},
	{name: "host_minutes", host: true, when: "bucket", times: []string{"bucket"}, key: []string{"host_id", "bucket"}},
	{name: "route_hours", when: "hour", times: []string{"hour"}, key: []string{"route", "hour"}},
	{name: "route_calls", when: "hour", times: []string{"hour"}, key: []string{"route", "host", "hour"}},
	{name: "job_hours", when: "hour", times: []string{"hour"}, key: []string{"kind", "name", "hour"}},
	{name: "command_runs", when: "started_at", times: []string{"started_at", "finished_at"}, drop: []string{"id"}},
	{name: "metric_minutes", when: "bucket", times: []string{"bucket"}, key: []string{"host_id", "name", "bucket"}},
}

func specOf(name string) (profileTable, bool) {
	for _, s := range profileTables {
		if s.name == name {
			return s, true
		}
	}
	return profileTable{}, false
}

// CutAt is the last time the hour struck in loc, at or before now: a profile cut there lines up
// with a demo rebuilt every night a little after that hour.
func CutAt(now time.Time, loc *time.Location, hour int) time.Time {
	t := now.In(loc)
	cut := time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, loc)
	if cut.After(t) {
		cut = cut.AddDate(0, 0, -1)
	}
	return cut
}

// ExportProfile reads the history of a project up to "to" (excluded).
func ExportProfile(ctx context.Context, db *store.DB, slug string, to time.Time) (Profile, error) {
	p, err := project.Repo{DB: db}.BySlug(ctx, slug)
	if err != nil {
		return Profile{}, err
	}
	prof := Profile{Version: 1, Timezone: p.Timezone, From: to.Unix(), To: to.Unix()}
	for _, spec := range profileTables {
		where := "project_id = ?"
		if spec.host {
			where = "host_id IN (SELECT id FROM hosts WHERE project_id = ?)"
		}
		args := []any{p.ID}
		if spec.when != "" {
			where += " AND " + spec.when + " < ?"
			args = append(args, to.Unix())
		}
		rows, err := db.Q().Query(ctx, "SELECT * FROM "+spec.name+" WHERE "+where, args...)
		if err != nil {
			return prof, fmt.Errorf("%s: %w", spec.name, err)
		}
		t, err := readTable(rows, spec)
		rows.Close()
		if err != nil {
			return prof, fmt.Errorf("%s: %w", spec.name, err)
		}
		if spec.when != "" {
			at := t.index(spec.when)
			for _, r := range t.Rows {
				if v, ok := r[at].(int64); ok && v < prof.From {
					prof.From = v
				}
			}
		}
		prof.Tables = append(prof.Tables, t)
	}
	return prof, nil
}

type rowScanner interface {
	Columns() ([]string, error)
	Next() bool
	Scan(...any) error
	Err() error
}

func readTable(rows rowScanner, spec profileTable) (Table, error) {
	cols, err := rows.Columns()
	if err != nil {
		return Table{}, err
	}
	t := Table{Name: spec.name}
	var keep []int
	for i, c := range cols {
		if c == "project_id" || contains(spec.drop, c) {
			continue
		}
		keep = append(keep, i)
		t.Columns = append(t.Columns, c)
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return t, err
		}
		row := make([]any, 0, len(keep))
		for _, i := range keep {
			row = append(row, normalize(vals[i]))
		}
		t.Rows = append(t.Rows, row)
	}
	return t, rows.Err()
}

// normalize turns what the drivers return into int64, float64, string, bool or nil.
func normalize(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	case time.Time:
		return x.Unix()
	}
	return v
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (t Table) index(col string) int {
	for i, c := range t.Columns {
		if c == col {
			return i
		}
	}
	return -1
}

func (p Profile) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	return enc.Encode(p)
}

// ReadProfile decodes numbers as int64 when they are whole, so ids and times stay exact.
func ReadProfile(r io.Reader) (Profile, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var p Profile
	if err := dec.Decode(&p); err != nil {
		return p, err
	}
	if p.Version != 1 {
		return p, fmt.Errorf("profile version %d is not supported", p.Version)
	}
	for ti := range p.Tables {
		for _, row := range p.Tables[ti].Rows {
			for i, v := range row {
				if n, ok := v.(json.Number); ok {
					if iv, err := n.Int64(); err == nil {
						row[i] = iv
					} else if fv, err := n.Float64(); err == nil {
						row[i] = fv
					}
				}
			}
		}
	}
	return p, nil
}

// ImportProfile builds a project from a profile, shifted by whole days so that its last hour is
// the last hour before o.Now. The rows arrive an hour at a time and the game judges every hour,
// so missions, bosses, deploy verdicts and badges are born when they would have been.
func ImportProfile(ctx context.Context, db *store.DB, prof Profile, o Options) (project.Project, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Name == "" {
		return project.Project{}, errors.New("a name is needed")
	}
	repo := project.Repo{DB: db}
	if existing, err := repo.BySlug(ctx, project.Slugify(o.Name)); err == nil {
		if err := repo.Delete(ctx, existing.ID); err != nil {
			return project.Project{}, err
		}
	} else if !errors.Is(err, project.ErrNotFound) {
		return project.Project{}, err
	}
	tz := prof.Timezone
	if tz == "" {
		tz = "UTC"
	}
	p, err := repo.Create(ctx, o.Name, tz)
	if err != nil {
		return p, err
	}
	p.Settings.People.Leaderboard = true
	if err := repo.Update(ctx, p); err != nil {
		return p, err
	}
	shift := (o.Now.Unix() - prof.To) / 86400 * 86400
	im := importer{db: db, p: p, shift: shift, hosts: map[int64]int64{0: 0}, fps: map[int64]int64{}}

	// Timeless tables first: machines and queries are what the rest points at.
	type pending struct {
		spec profileTable
		t    *Table
		at   int
		next int
	}
	var timed []*pending
	for _, spec := range profileTables {
		t := prof.table(spec.name)
		if t == nil {
			continue
		}
		if spec.when == "" {
			for _, r := range t.Rows {
				if err := im.insert(ctx, spec, *t, r); err != nil {
					return p, fmt.Errorf("%s: %w", spec.name, err)
				}
			}
			continue
		}
		at := t.index(spec.when)
		if at < 0 {
			return p, fmt.Errorf("%s: no column %s", spec.name, spec.when)
		}
		sort.SliceStable(t.Rows, func(i, j int) bool { return asInt(t.Rows[i][at]) < asInt(t.Rows[j][at]) })
		timed = append(timed, &pending{spec: spec, t: t, at: at})
	}

	engine := game.New(db)
	for h := prof.From - prof.From%3600; h < prof.To; h += 3600 {
		for _, pd := range timed {
			for ; pd.next < len(pd.t.Rows) && asInt(pd.t.Rows[pd.next][pd.at]) < h+3600; pd.next++ {
				if err := im.insert(ctx, pd.spec, *pd.t, pd.t.Rows[pd.next]); err != nil {
					return p, fmt.Errorf("%s: %w", pd.spec.name, err)
				}
			}
		}
		evalAt := time.Unix(h+3600+60+shift, 0)
		engine.Now = func() time.Time { return evalAt }
		if err := engine.EvaluateProject(ctx, p); err != nil {
			return p, err
		}
	}

	// The machines still reporting when the history ends are the ones online now.
	if _, err := db.Q().Exec(ctx, "UPDATE hosts SET last_seen_at = ? WHERE project_id = ? AND last_seen_at >= ?",
		o.Now.Unix(), p.ID, prof.To+shift-3600); err != nil {
		return p, err
	}
	engine.Now = func() time.Time { return o.Now }
	if err := engine.EvaluateProject(ctx, p); err != nil {
		return p, err
	}
	_, err = db.Q().Exec(ctx, "UPDATE activities SET notified = TRUE WHERE project_id = ?", p.ID)
	return p, err
}

type importer struct {
	db    *store.DB
	p     project.Project
	shift int64
	hosts map[int64]int64
	fps   map[int64]int64
}

func (im importer) insert(ctx context.Context, spec profileTable, t Table, row []any) error {
	cols := make([]string, 0, len(t.Columns)+1)
	args := make([]any, 0, len(t.Columns)+1)
	var oldID int64
	for i, c := range t.Columns {
		v := row[i]
		switch {
		case c == "id":
			oldID = asInt(v)
			continue
		case c == "host_id":
			id, ok := im.hosts[asInt(v)]
			if !ok {
				return nil // a row of a machine that is not in the profile
			}
			v = id
		case c == "fingerprint_id":
			id, ok := im.fps[asInt(v)]
			if !ok {
				return nil
			}
			v = id
		case contains(spec.times, c) && v != nil:
			v = asInt(v) + im.shift
		}
		cols = append(cols, c)
		args = append(args, v)
	}
	if !spec.host {
		cols = append(cols, "project_id")
		args = append(args, im.p.ID)
	}
	query := "INSERT INTO " + spec.name + " (" + strings.Join(cols, ", ") + ") VALUES (?" + strings.Repeat(", ?", len(cols)-1) + ")"
	switch spec.name {
	case "hosts":
		id, err := im.db.Q().InsertID(ctx, query, args...)
		im.hosts[oldID] = id
		return err
	case "query_fingerprints":
		id, err := im.db.Q().InsertID(ctx, query, args...)
		im.fps[oldID] = id
		return err
	}
	_, err := im.db.Q().Exec(ctx, query, args...)
	return err
}

func asInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	}
	return 0
}

// A Dictionary renames a profile. In order, on every text cell: Values replaces a whole cell
// (a commit message, a cron description), Replace every occurrence of a string (a domain), Words
// every whole word keeping its case, also inside camelCase and snake_case ("immobili",
// "ExportImmobileDataJob"). Deny lists strings and DenyWords whole words that must not survive,
// both checked without regard to case.
type Dictionary struct {
	Values    map[string]string `json:"values"`
	Replace   []Pair            `json:"replace"`
	Words     map[string]string `json:"words"`
	Deny      []string          `json:"deny"`
	DenyWords []string          `json:"deny_words"`
}

type Pair [2]string

var (
	ipv4 = regexp.MustCompile(`\b(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\b`)
	ipv6 = regexp.MustCompile(`\b[0-9a-fA-F]{1,4}(:[0-9a-fA-F]{0,4}){3,7}\b`)
	// What identifies a person, wherever it ended up: a path, a job name, a message.
	email      = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+`)
	fiscalCode = regexp.MustCompile(`\b[A-Za-z]{6}\d{2}[A-Za-z]\d{2}[A-Za-z]\d{3}[A-Za-z]\b`)
	iban       = regexp.MustCompile(`\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b`)
	letters    = regexp.MustCompile(`[A-Za-z]+`)
)

// subwords splits a run of letters where camelCase does: "PDFAntiriciclaggio" is PDF and
// Antiriciclaggio, "ExportImmobileDataJob" is Export, Immobile, Data, Job.
func subwords(run string) []string {
	var out []string
	start := 0
	isUpper := func(b byte) bool { return b >= 'A' && b <= 'Z' }
	for i := 1; i < len(run); i++ {
		switch {
		case !isUpper(run[i-1]) && isUpper(run[i]):
			out, start = append(out, run[start:i]), i
		case i+1 < len(run) && isUpper(run[i-1]) && isUpper(run[i]) && !isUpper(run[i+1]):
			out, start = append(out, run[start:i]), i
		}
	}
	return append(out, run[start:])
}

// withCase writes to in the case of from: lower, Title or UPPER.
func withCase(from, to string) string {
	switch {
	case from == strings.ToUpper(from) && len(from) > 1:
		return strings.ToUpper(to)
	case from[0] >= 'A' && from[0] <= 'Z':
		return strings.ToUpper(to[:1]) + to[1:]
	}
	return to
}

func translateWords(s string, words map[string]string) string {
	if len(words) == 0 {
		return s
	}
	return letters.ReplaceAllStringFunc(s, func(run string) string {
		parts := subwords(run)
		for i, w := range parts {
			if to, ok := words[strings.ToLower(w)]; ok && to != "" {
				parts[i] = withCase(w, to)
			}
		}
		return strings.Join(parts, "")
	})
}

// Rename applies the dictionary, scrubs what identifies a person, gives every IPv4 address
// another one of the same kind (private stays private) and replaces commit hashes with others
// of the same length.
func (p *Profile) Rename(d Dictionary) {
	pairs := append([]Pair(nil), d.Replace...)
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i][0]) > len(pairs[j][0]) })
	ips := map[string]string{}
	var private, public int
	fakeIP := func(ip string) string {
		if v, ok := ips[ip]; ok {
			return v
		}
		m := ipv4.FindStringSubmatch(ip)
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		var v string
		switch {
		case a == 127 || a == 0:
			v = ip
		case a == 10 || a == 192 && b == 168 || a == 172 && b >= 16 && b <= 31:
			private++
			v = fmt.Sprintf("10.0.%d.%d", private/250, private%250+2)
		default:
			public++
			v = fmt.Sprintf("203.0.%d.%d", 113+public/250, public%250+2)
		}
		ips[ip] = v
		return v
	}
	for ti := range p.Tables {
		t := &p.Tables[ti]
		sha := t.index("commit_sha")
		for _, row := range t.Rows {
			for i, v := range row {
				s, ok := v.(string)
				if !ok {
					continue
				}
				if i == sha && s != "" {
					sum := sha256.Sum256([]byte("slowpoke-demo:" + s))
					row[i] = hex.EncodeToString(sum[:])[:len(s)]
					continue
				}
				if to, ok := d.Values[s]; ok {
					row[i] = to
					continue
				}
				s = email.ReplaceAllString(s, "someone@example.com")
				s = fiscalCode.ReplaceAllString(s, "{id}")
				s = iban.ReplaceAllString(s, "{iban}")
				s = ipv6.ReplaceAllStringFunc(s, func(m string) string {
					if strings.Count(m, ":") < 2 || !strings.ContainsAny(m, "0123456789") {
						return m
					}
					return "2001:db8::1"
				})
				for _, pr := range pairs {
					s = strings.ReplaceAll(s, pr[0], pr[1])
				}
				s = translateWords(s, d.Words)
				row[i] = ipv4.ReplaceAllStringFunc(s, fakeIP)
			}
		}
		t.merge()
	}
}

// merge folds the rows that renaming made equal on the key: counts and sums add up, maxima and
// times keep the highest, minima the lowest, anything else the first row's value.
func (t *Table) merge() {
	spec, ok := specOf(t.Name)
	if !ok || len(spec.key) == 0 {
		return
	}
	var keyAt []int
	for _, k := range spec.key {
		if i := t.index(k); i >= 0 {
			keyAt = append(keyAt, i)
		}
	}
	seen := map[string]int{}
	rows := t.Rows[:0]
	for _, r := range t.Rows {
		parts := make([]string, len(keyAt))
		for j, i := range keyAt {
			parts[j] = fmt.Sprint(r[i])
		}
		k := strings.Join(parts, "\x00")
		at, dup := seen[k]
		if !dup {
			seen[k] = len(rows)
			rows = append(rows, r)
			continue
		}
		into := rows[at]
		for i, c := range t.Columns {
			if contains(spec.key, c) {
				continue
			}
			into[i] = fold(c, contains(spec.times, c), into[i], r[i])
		}
	}
	t.Rows = rows
}

func fold(col string, isTime bool, a, b any) any {
	fa, aNum := a.(float64)
	fb, bNum := b.(float64)
	ia, aInt := a.(int64)
	ib, bInt := b.(int64)
	switch {
	case aInt && bInt:
		switch {
		case isTime || col == "max" || strings.HasSuffix(col, "_max") || strings.HasPrefix(col, "max"):
			return max(ia, ib)
		case col == "min":
			return min(ia, ib)
		}
		return ia + ib
	case (aNum || aInt) && (bNum || bInt):
		x, y := fa, fb
		if aInt {
			x = float64(ia)
		}
		if bInt {
			y = float64(ib)
		}
		switch {
		case col == "max" || strings.HasSuffix(col, "_max") || strings.HasPrefix(col, "max"):
			return max(x, y)
		case col == "min":
			return min(x, y)
		case col == "last":
			return y
		}
		return x + y
	case a == nil:
		return b
	}
	return a
}

// Leaks lists the text cells that still hold a denied string or a denied whole word:
// "table.column: value".
func (p Profile) Leaks(deny []string, denyWords ...string) []string {
	words := map[string]bool{}
	for _, w := range denyWords {
		words[strings.ToLower(w)] = true
	}
	var out []string
	for _, t := range p.Tables {
		for _, row := range t.Rows {
			for i, v := range row {
				s, ok := v.(string)
				if !ok {
					continue
				}
				if leaky(s, deny, words) {
					out = append(out, t.Name+"."+t.Columns[i]+": "+s)
				}
			}
		}
	}
	return out
}

func leaky(s string, deny []string, words map[string]bool) bool {
	low := strings.ToLower(s)
	for _, d := range deny {
		if d != "" && strings.Contains(low, strings.ToLower(d)) {
			return true
		}
	}
	if len(words) > 0 {
		for _, run := range letters.FindAllString(s, -1) {
			for _, w := range subwords(run) {
				if words[strings.ToLower(w)] {
					return true
				}
			}
		}
	}
	return false
}

// Strings lists every distinct text of the profile with the column it lives in, to write the
// dictionary from: "column\tvalue".
func (p Profile) Strings() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range p.Tables {
		for _, row := range t.Rows {
			for i, v := range row {
				if s, ok := v.(string); ok && s != "" {
					k := t.Name + "." + t.Columns[i] + "\t" + s
					if !seen[k] {
						seen[k] = true
						out = append(out, k)
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// Prune drops what weighs nothing: endpoints, routes and queries with fewer than minN calls and
// less than minSeconds of time over the whole history, with every row that hangs on them. A
// public server collects thousands of one-off probes (/.env, /wp-admin): they are a fraction of
// a percent of the time, and every one of them is a name to check before it can be shown.
func (p *Profile) Prune(minN int64, minSeconds float64) {
	keep := func(table, key string) map[any]bool {
		t := p.table(table)
		if t == nil {
			return nil
		}
		ik, in, is := t.index(key), t.index("n"), t.index("sum")
		n, sum := map[any]int64{}, map[any]float64{}
		for _, r := range t.Rows {
			n[r[ik]] += asInt(r[in])
			sum[r[ik]] += asFloat(r[is])
		}
		out := map[any]bool{}
		for k := range n {
			if n[k] >= minN || sum[k] >= minSeconds {
				out[k] = true
			}
		}
		return out
	}
	endpoints := keep("endpoint_hours", "endpoint")
	routes := keep("route_hours", "route")
	queries := keep("query_hours", "fingerprint_id")
	filter := func(table, key string, kept map[any]bool) {
		t := p.table(table)
		if t == nil || kept == nil {
			return
		}
		ik := t.index(key)
		rows := t.Rows[:0]
		for _, r := range t.Rows {
			if kept[r[ik]] {
				rows = append(rows, r)
			}
		}
		t.Rows = rows
	}
	filter("endpoint_hours", "endpoint", endpoints)
	filter("route_hours", "route", routes)
	filter("route_queries", "route", routes)
	filter("route_calls", "route", routes)
	filter("query_hours", "fingerprint_id", queries)
	filter("query_fingerprints", "id", queries)
	filter("route_queries", "fingerprint_id", queries)
}

func asFloat(v any) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return 0
}

// Until ends the history earlier: rows that happened at or after to are dropped.
func (p *Profile) Until(to int64) {
	if to >= p.To {
		return
	}
	for i := range p.Tables {
		t := &p.Tables[i]
		spec, ok := specOf(t.Name)
		if !ok || spec.when == "" {
			continue
		}
		at := t.index(spec.when)
		rows := t.Rows[:0]
		for _, r := range t.Rows {
			if asInt(r[at]) < to {
				rows = append(rows, r)
			}
		}
		t.Rows = rows
	}
	p.To = to
}
