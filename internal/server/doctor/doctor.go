// Package doctor explains why a query is slow and how to fix it, from the
// statement, its EXPLAIN plan, the table definitions and what the framework
// package saw (origin in code, N+1).
package doctor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

type Severity string

const (
	Critical Severity = "critical"
	Warning  Severity = "warning"
	Info     Severity = "info"
)

var severityRank = map[Severity]int{Critical: 0, Warning: 1, Info: 2}

type Diagnosis struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Fix      string   `json:"fix,omitempty"`
	Table    string   `json:"table,omitempty"`
}

type Explain struct {
	Engine  string                       `json:"engine"`
	Generic bool                         `json:"generic,omitempty"` // planned without values: estimates, not a real call
	Plan    json.RawMessage              `json:"plan"`
	Tables  map[string]protocol.TableDef `json:"tables"`
}

type Stats struct {
	CallsPerDay     float64 `json:"calls_per_day"`
	AvgSeconds      float64 `json:"avg_seconds"`
	SecondsPerDay   float64 `json:"seconds_per_day"`
	RowsExaminedAvg float64 `json:"rows_examined_avg"`
}

type Input struct {
	Fingerprint string
	Sample      string
	Explain     *Explain
	Stats       Stats
	Origin      string
	NPlusOne    int64
	// Routes are the endpoints that run the query, with how many times per request: for an N+1
	// they are where the loop is.
	Routes []RouteUse
	// Link is the page of this query in the panel, for whoever reads the prompt later.
	Link string
}

type RouteUse struct {
	Route      string
	PerRequest float64
	Requests   int64
}

const (
	scanWarnRows     = 1000
	scanCriticalRows = 50000
)

// Analyze returns the findings, most severe first.
func Analyze(in Input) []Diagnosis {
	q := parse(in)
	var out []Diagnosis
	add := func(d Diagnosis) { out = append(out, d) }

	for _, check := range []func(*query, Input, func(Diagnosis)){
		checkPlan, checkImplicitConversion, checkFunctionOnColumn, checkLeadingWildcard, checkOr,
		checkSelectStar, checkDeepOffset, checkCount, checkNotInSubquery, checkNPlusOne,
	} {
		check(q, in, add)
	}

	sort.SliceStable(out, func(i, j int) bool { return severityRank[out[i].Severity] < severityRank[out[j].Severity] })
	return out
}

// ---------------------------------------------------------------- parsing

type condition struct {
	table, column string
	equality      bool
}

type query struct {
	sql        string // lowercase sample (or fingerprint), no backticks or double quotes
	where      string
	tables     []string          // in order of appearance
	aliases    map[string]string // alias -> table
	conditions []condition
	orderBy    []condition
	engine     string
	defs       map[string]protocol.TableDef
}

var (
	fromJoinRe   = regexp.MustCompile(`\b(?:from|join)\s+([a-z0-9_$]+(?:\.[a-z0-9_$]+)?)(?:\s+(?:as\s+)?([a-z0-9_]+))?`)
	whereRe      = regexp.MustCompile(`\bwhere\b(.*?)(?:\bgroup\s+by\b|\border\s+by\b|\blimit\b|\bhaving\b|\bfor\s+update\b|\bunion\b|$)`)
	conditionRe  = regexp.MustCompile(`(?:\b([a-z_][a-z0-9_]*)\.)?\b([a-z_][a-z0-9_]*)\s*(<=>|=|>=|<=|<>|!=|>|<|\bin\s*\(|\bbetween\b|\blike\s+'[^%_])`)
	orderByRe    = regexp.MustCompile(`\border\s+by\s+(.+?)(?:\blimit\b|$)`)
	reservedWord = map[string]bool{"where": true, "join": true, "left": true, "right": true, "inner": true, "outer": true, "cross": true, "on": true, "order": true, "group": true, "limit": true, "set": true, "using": true, "and": true, "or": true, "not": true, "null": true, "is": true, "select": true, "straight_join": true, "natural": true, "force": true, "use": true, "ignore": true, "lateral": true}
)

func parse(in Input) *query {
	text := in.Sample
	if text == "" {
		text = in.Fingerprint
	}
	text = strings.ToLower(strings.NewReplacer("`", "", `"`, "").Replace(text))
	text = strings.Join(strings.Fields(text), " ")

	q := &query{sql: text, aliases: map[string]string{}, defs: map[string]protocol.TableDef{}}
	if in.Explain != nil {
		q.engine = in.Explain.Engine
		for name, def := range in.Explain.Tables {
			q.defs[strings.ToLower(name)] = def
		}
	}

	for _, m := range fromJoinRe.FindAllStringSubmatch(text, -1) {
		table := m[1]
		if i := strings.LastIndex(table, "."); i >= 0 {
			table = table[i+1:]
		}
		if reservedWord[table] || strings.HasPrefix(table, "(") {
			continue
		}
		q.tables = appendUnique(q.tables, table)
		q.aliases[table] = table
		if alias := m[2]; alias != "" && !reservedWord[alias] {
			q.aliases[alias] = table
		}
	}

	if m := whereRe.FindStringSubmatch(text); m != nil {
		q.where = m[1]
		// Functions are handled by their own check: their arguments are not usable columns.
		for _, c := range conditionRe.FindAllStringSubmatch(stripFunctions(m[1]), -1) {
			if reservedWord[c[2]] {
				continue
			}
			op := strings.TrimSpace(c[3])
			eq := op == "=" || op == "<=>" || strings.HasPrefix(op, "in")
			if table := q.resolve(c[1], c[2]); table != "" {
				q.conditions = append(q.conditions, condition{table: table, column: c[2], equality: eq})
			}
		}
	}

	if m := orderByRe.FindStringSubmatch(text); m != nil {
		for _, part := range strings.Split(m[1], ",") {
			fields := strings.Fields(strings.TrimSpace(part))
			if len(fields) == 0 {
				continue
			}
			ref := fields[0]
			qualifier, column := "", ref
			if i := strings.Index(ref, "."); i >= 0 {
				qualifier, column = ref[:i], ref[i+1:]
			}
			if !regexp.MustCompile(`^[a-z_][a-z0-9_]*$`).MatchString(column) {
				break
			}
			if table := q.resolve(qualifier, column); table != "" {
				q.orderBy = append(q.orderBy, condition{table: table, column: column})
			}
		}
	}
	return q
}

var functionCallRe = regexp.MustCompile(`\b[a-z_][a-z0-9_]*\s*\([^()]*\)`)

func stripFunctions(s string) string {
	for i := 0; i < 5; i++ {
		next := functionCallRe.ReplaceAllStringFunc(s, func(call string) string {
			if strings.HasPrefix(strings.TrimSpace(call), "in") || strings.HasPrefix(call, "in(") {
				return call
			}
			return "fn()"
		})
		if next == s {
			break
		}
		s = next
	}
	return s
}

// resolve finds the table of a column reference.
func (q *query) resolve(qualifier, column string) string {
	if qualifier != "" {
		return q.aliases[qualifier]
	}
	if len(q.tables) == 1 {
		return q.tables[0]
	}
	for _, t := range q.tables {
		if hasColumn(q.defs[t], column) {
			return t
		}
	}
	return ""
}

func hasColumn(def protocol.TableDef, column string) bool {
	_, ok := columnType(def, column)
	return ok
}

func columnType(def protocol.TableDef, column string) (string, bool) {
	for _, c := range def.Columns {
		if strings.EqualFold(c.Name, column) {
			return strings.ToLower(c.Type), true
		}
	}
	return "", false
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// ---------------------------------------------------------------- index suggestion

func (q *query) suggestIndex(table string) []string {
	def, known := q.defs[table]
	usable := func(c condition) bool {
		return c.table == table && (!known || len(def.Columns) == 0 || hasColumn(def, c.column))
	}
	var cols []string
	for _, c := range q.conditions {
		if c.equality && usable(c) {
			cols = appendUnique(cols, c.column)
		}
	}
	rangeAdded := false
	for _, c := range q.conditions {
		if !c.equality && usable(c) && !contains(cols, c.column) {
			cols = append(cols, c.column)
			rangeAdded = true
			break
		}
	}
	if !rangeAdded {
		for _, c := range q.orderBy {
			if !usable(c) {
				break
			}
			cols = appendUnique(cols, c.column)
		}
	}
	if len(cols) > 4 {
		cols = cols[:4]
	}
	return cols
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (q *query) existingIndexStartingWith(table, column string) string {
	for _, ix := range q.defs[table].Indexes {
		if len(ix.Columns) > 0 && strings.EqualFold(ix.Columns[0], column) {
			return ix.Name
		}
	}
	return ""
}

func indexName(table string, cols []string) string {
	name := "idx_" + table + "_" + strings.Join(cols, "_")
	if len(name) > 60 {
		name = name[:60]
	}
	return name
}

func (q *query) indexFix(table string, cols []string) string {
	name := indexName(table, cols)
	if q.engine == "postgres" {
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = `"` + c + `"`
		}
		return fmt.Sprintf(`CREATE INDEX CONCURRENTLY %s ON "%s" (%s);`, name, table, strings.Join(quoted, ", "))
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = "`" + c + "`"
	}
	return fmt.Sprintf("ALTER TABLE `%s` ADD INDEX `%s` (%s), ALGORITHM=INPLACE, LOCK=NONE;", table, name, strings.Join(quoted, ", "))
}

// ---------------------------------------------------------------- checks

type planRow struct {
	table, access, key, extra string
	rows                      float64
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (q *query) planRows(e *Explain) []planRow {
	var out []planRow
	if e.Engine == "postgres" {
		var decoded any
		_ = json.Unmarshal(e.Plan, &decoded)
		var walk func(v any, parentSort bool)
		walk = func(v any, parentSort bool) {
			switch x := v.(type) {
			case []any:
				for _, c := range x {
					walk(c, parentSort)
				}
			case map[string]any:
				if plan, ok := x["Plan"]; ok {
					walk(plan, parentSort)
					return
				}
				node := str(x["Node Type"])
				row := planRow{table: str(x["Relation Name"]), rows: toFloat(x["Plan Rows"]), key: str(x["Index Name"])}
				switch node {
				case "Seq Scan":
					row.access = "ALL"
				case "Index Scan", "Index Only Scan", "Bitmap Heap Scan":
					row.access = "ref"
				}
				if parentSort {
					row.extra = "Using filesort"
				}
				if row.table != "" {
					out = append(out, row)
				}
				children, _ := x["Plans"].([]any)
				for _, c := range children {
					walk(c, node == "Sort")
				}
			}
		}
		walk(decoded, false)
		return out
	}

	var rows []map[string]any
	_ = json.Unmarshal(e.Plan, &rows)
	for _, r := range rows {
		out = append(out, planRow{table: strings.ToLower(str(r["table"])), access: str(r["type"]), key: str(r["key"]), extra: str(r["Extra"]), rows: toFloat(r["rows"])})
	}
	return out
}

func checkPlan(q *query, in Input, add func(Diagnosis)) {
	if in.Explain == nil {
		return
	}
	for _, r := range q.planRows(in.Explain) {
		table := r.table
		rows := max(r.rows, float64(q.defs[table].Rows))
		if r.access == "ALL" && rows >= scanWarnRows {
			sev := Warning
			if rows >= scanCriticalRows {
				sev = Critical
			}
			d := Diagnosis{Code: "full_scan", Severity: sev, Table: table,
				Title:  fmt.Sprintf("Full table scan on %s", table),
				Detail: fmt.Sprintf("The database reads every row of %s (about %s rows) because no index matches the filter.", table, humanRows(rows))}
			cols := q.suggestIndex(table)
			switch {
			case len(cols) == 0:
				d.Fix = "Add a WHERE condition on an indexed column, or an index on the columns used to filter."
			case q.existingIndexStartingWith(table, cols[0]) != "":
				ix := q.existingIndexStartingWith(table, cols[0])
				d.Detail += fmt.Sprintf(" An index starting with %s exists (%s) but is not used.", cols[0], ix)
				d.Fix = "Check that the value has the column's type (a number against a text column disables the index) and that the filter is selective enough; ANALYZE TABLE refreshes statistics."
			default:
				d.Fix = q.indexFix(table, cols)
			}
			add(d)
		}
		if strings.Contains(r.extra, "filesort") && rows >= scanWarnRows {
			add(Diagnosis{Code: "filesort", Severity: Warning, Table: table,
				Title:  "Sorting without an index",
				Detail: fmt.Sprintf("Rows of %s are sorted after being read (%s rows).", table, humanRows(rows)),
				Fix:    "Put the ORDER BY columns at the end of the index used by the filter."})
		}
		if strings.Contains(r.extra, "temporary") {
			add(Diagnosis{Code: "temporary", Severity: Warning, Table: table,
				Title:  "Temporary table",
				Detail: "GROUP BY, DISTINCT or a subquery builds a temporary table, on disk when it is big.",
				Fix:    "Index the GROUP BY columns, or aggregate a smaller filtered set."})
		}
	}
}

var textTypeRe = regexp.MustCompile(`^(var)?char|text|enum|uuid|citext`)

func checkImplicitConversion(q *query, in Input, add func(Diagnosis)) {
	if len(q.defs) == 0 || in.Sample == "" {
		return
	}
	seen := map[string]bool{}
	for _, c := range q.conditions {
		ctype, ok := columnType(q.defs[c.table], c.column)
		if !ok || !textTypeRe.MatchString(ctype) || seen[c.table+"."+c.column] {
			continue
		}
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(c.column) + `\s*(=|in\s*\(|>|<|>=|<=)\s*-?\d`)
		if re.MatchString(q.sql) {
			seen[c.table+"."+c.column] = true
			add(Diagnosis{Code: "implicit_conversion", Severity: Critical, Table: c.table,
				Title:  "Number compared with a text column",
				Detail: fmt.Sprintf("%s.%s is %s but the query compares it with a number: the index cannot be used and every row is converted.", c.table, c.column, ctype),
				Fix:    fmt.Sprintf("Pass the value as a string (%s = '123'), e.g. cast it in code before the query.", c.column)})
		}
	}
}

var functionOnColumnRe = regexp.MustCompile(`\b(date|lower|upper|json_extract|json_unquote|year|month|day|substring|substr|concat|ifnull|coalesce|trim|cast|convert|date_format|unix_timestamp|to_char|extract)\s*\(\s*([a-z_][a-z0-9_.]*)`)

func checkFunctionOnColumn(q *query, _ Input, add func(Diagnosis)) {
	if q.where == "" {
		return
	}
	if m := functionOnColumnRe.FindStringSubmatch(q.where); m != nil {
		add(Diagnosis{Code: "function_on_column", Severity: Warning,
			Title:  fmt.Sprintf("%s() applied to a column in WHERE", m[1]),
			Detail: fmt.Sprintf("Wrapping %s in %s() prevents the database from using an index on it.", m[2], m[1]),
			Fix:    "Compare the raw column (e.g. a range instead of DATE(col)), or add an indexed generated column with the computed value."})
	}
}

func checkLeadingWildcard(q *query, _ Input, add func(Diagnosis)) {
	if regexp.MustCompile(`\blike\s+'%`).MatchString(q.sql) {
		add(Diagnosis{Code: "leading_wildcard", Severity: Warning,
			Title:  "LIKE starting with %",
			Detail: "A pattern that starts with a wildcard cannot use a B-tree index: every row is checked.",
			Fix:    "Use a prefix search ('abc%'), a FULLTEXT / trigram index, or a search engine."})
	}
}

func checkOr(q *query, _ Input, add func(Diagnosis)) {
	if q.where == "" || !regexp.MustCompile(`\bor\b`).MatchString(q.where) {
		return
	}
	columns := map[string]bool{}
	for _, part := range regexp.MustCompile(`\bor\b`).Split(q.where, -1) {
		for _, m := range regexp.MustCompile(`([a-z_][a-z0-9_]*)\s*(\(|=|like|>|<|in\b)`).FindAllStringSubmatch(part, -1) {
			if !reservedWord[m[1]] {
				columns[m[1]] = true
				break
			}
		}
	}
	if len(columns) >= 2 {
		add(Diagnosis{Code: "or_conditions", Severity: Info,
			Title:  "OR across different columns",
			Detail: "OR between different columns often prevents a single index from being used.",
			Fix:    "Split into a UNION ALL of queries that each use their own index."})
	}
}

var wideTypeRe = regexp.MustCompile(`json|text|blob|bytea`)

func checkSelectStar(q *query, _ Input, add func(Diagnosis)) {
	if !regexp.MustCompile(`^select\s+([a-z0-9_]+\.)?\*\s+from`).MatchString(q.sql) {
		return
	}
	for _, t := range q.tables {
		def, ok := q.defs[t]
		if !ok {
			continue
		}
		heavy := len(def.Columns) >= 25
		for _, c := range def.Columns {
			if wideTypeRe.MatchString(strings.ToLower(c.Type)) {
				heavy = true
			}
		}
		if heavy {
			sev := Info
			if def.Rows >= 100000 {
				sev = Warning
			}
			add(Diagnosis{Code: "select_star_wide", Severity: sev, Table: t,
				Title:  fmt.Sprintf("SELECT * on a wide table (%s)", t),
				Detail: "The query loads every column, including large text/JSON ones, even if few are used.",
				Fix:    "Select only the needed columns (e.g. ->select(['id', 'status']) in Eloquent)."})
			return
		}
	}
}

var offsetRe = regexp.MustCompile(`\boffset\s+(\d+)|\blimit\s+(\d+)\s*,\s*\d+`)

func checkDeepOffset(q *query, _ Input, add func(Diagnosis)) {
	m := offsetRe.FindStringSubmatch(q.sql)
	if m == nil {
		return
	}
	n, _ := strconv.Atoi(m[1] + m[2])
	if n >= 10000 {
		add(Diagnosis{Code: "deep_offset", Severity: Warning,
			Title:  fmt.Sprintf("Pagination with OFFSET %d", n),
			Detail: "The database reads and throws away all the skipped rows on every page.",
			Fix:    "Use keyset pagination: WHERE id > :last_id ORDER BY id LIMIT :size."})
	}
}

func checkCount(q *query, in Input, add func(Diagnosis)) {
	if !strings.HasPrefix(q.sql, "select count(") {
		return
	}
	for _, t := range q.tables {
		if rows := q.defs[t].Rows; rows >= 1000000 {
			sev := Info
			if in.Stats.CallsPerDay*in.Stats.AvgSeconds >= 600 {
				sev = Warning
			}
			add(Diagnosis{Code: "count_big_table", Severity: sev, Table: t,
				Title:  fmt.Sprintf("COUNT on a big table (%s rows)", humanRows(float64(rows))),
				Detail: "Counting reads every matching row each time.",
				Fix:    "Cache the count, keep a counter column updated on write, or show an approximate value."})
			return
		}
	}
}

func checkNotInSubquery(q *query, _ Input, add func(Diagnosis)) {
	if regexp.MustCompile(`\bnot\s+in\s*\(\s*select\b`).MatchString(q.sql) {
		add(Diagnosis{Code: "not_in_subquery", Severity: Warning,
			Title:  "NOT IN (SELECT ...)",
			Detail: "NOT IN with a subquery is often executed row by row and behaves unexpectedly with NULLs.",
			Fix:    "Rewrite as NOT EXISTS or a LEFT JOIN ... WHERE other.id IS NULL."})
	}
}

func checkNPlusOne(_ *query, in Input, add func(Diagnosis)) {
	if in.NPlusOne <= 0 {
		return
	}
	sev := Warning
	if in.NPlusOne >= 10 {
		sev = Critical
	}
	where := "in a loop"
	if in.Origin != "" {
		where = "from " + in.Origin
	}
	add(Diagnosis{Code: "n_plus_one", Severity: sev,
		Title:  "N+1 queries",
		Detail: fmt.Sprintf("The same query ran many times in a single request %s (%d requests affected).", where, in.NPlusOne),
		Fix:    nPlusOneFix(in.Fingerprint)})
}

// aggregateRe is how Laravel writes ->sum(), ->count(), ->avg() on a relation: "... as aggregate".
var aggregateRe = regexp.MustCompile(`^select\s+(sum|count|avg|min|max)\s*\(.*\)\s+as\s+aggregate\b`)

func nPlusOneFix(fp string) string {
	if m := aggregateRe.FindStringSubmatch(strings.ToLower(fp)); m != nil {
		fn := map[string]string{"sum": "withSum", "count": "withCount", "avg": "withAvg", "min": "withMin", "max": "withMax"}[m[1]]
		return fmt.Sprintf("This is Laravel's ->%s() called on a relation once per row. Load the totals together with the rows: "+
			"->%s('relation', 'column') on the query that loads them (or one grouped query keyed by id), then read the attribute instead of querying.", m[1], fn)
	}
	return "Load the relation in one query: eager loading (->with('relation')) or a single WHERE id IN (...)."
}

func humanRows(n float64) string {
	switch {
	case n >= 1e6:
		return strconv.FormatFloat(n/1e6, 'f', 1, 64) + "M"
	case n >= 1e3:
		return strconv.FormatFloat(n/1e3, 'f', 0, 64) + "k"
	}
	return strconv.FormatFloat(n, 'f', 0, 64)
}

// Prompt builds a self-contained request for the team's coding assistant, working in the
// application's repository: where to start, what the measure says, what the fix usually is and
// how to prove it. An N+1 is fixed in the code, a slow statement often in the schema.
func Prompt(in Input, findings []Diagnosis) string {
	var b strings.Builder
	nPlusOne := in.NPlusOne > 0
	if nPlusOne {
		b.WriteString("Slowpoke measured an N+1 in production: the same query runs many times in one request. Fix it in this repository with the smallest safe code change, keeping the behaviour identical.\n")
	} else {
		b.WriteString("Slowpoke measured a slow query in production. Find why it is slow and fix it in this repository with the smallest safe change (a query rewrite, a code change or an index), keeping the behaviour identical.\n")
	}

	if in.Origin != "" || len(in.Routes) > 0 {
		b.WriteString("\n## Where to look\n")
		if in.Origin != "" {
			file, line := in.Origin, ""
			if i := strings.LastIndex(in.Origin, ":"); i > 0 {
				if _, err := strconv.Atoi(in.Origin[i+1:]); err == nil {
					file, line = in.Origin[:i], in.Origin[i+1:]
				}
			}
			if line != "" {
				fmt.Fprintf(&b, "- The query is issued from `%s` (line %s of `%s`).\n", in.Origin, line, file)
			} else {
				fmt.Fprintf(&b, "- The query is issued from `%s`.\n", file)
			}
		}
		if len(in.Routes) > 0 {
			b.WriteString("- It runs in these routes (times per request):\n")
			for _, r := range in.Routes {
				fmt.Fprintf(&b, "  - `%s`: %s per request, %d requests\n", r.Route, strconv.FormatFloat(r.PerRequest, 'f', -1, 64), r.Requests)
			}
		}
		if nPlusOne {
			b.WriteString("- Start from that line and go up to its callers in the routes with the most calls per request: the loop that calls it once per item is the problem.\n")
		}
	}

	b.WriteString("\n## The query (values redacted)\n```sql\n")
	if in.Sample != "" {
		b.WriteString(in.Sample)
	} else {
		b.WriteString(in.Fingerprint)
	}
	b.WriteString("\n```\n\n## What it costs\n")
	fmt.Fprintf(&b, "- calls per day: %.0f\n- average time: %.3f s\n- waiting per day: %.0f s\n", in.Stats.CallsPerDay, in.Stats.AvgSeconds, in.Stats.SecondsPerDay)
	if in.Stats.RowsExaminedAvg > 0 {
		fmt.Fprintf(&b, "- rows examined per call: %.0f\n", in.Stats.RowsExaminedAvg)
	}
	if nPlusOne {
		fmt.Fprintf(&b, "- detected as N+1 in %d requests\n", in.NPlusOne)
	}

	if len(findings) > 0 {
		b.WriteString("\n## Diagnosis\n")
		for _, f := range findings {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", f.Severity, f.Title, f.Detail)
			if f.Fix != "" {
				fmt.Fprintf(&b, "  Usual fix: %s\n", f.Fix)
			}
		}
	}

	if in.Explain != nil {
		fmt.Fprintf(&b, "\n## EXPLAIN (%s)\n```json\n%s\n```\n", in.Explain.Engine, string(in.Explain.Plan))
		names := make([]string, 0, len(in.Explain.Tables))
		for n := range in.Explain.Tables {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			def := in.Explain.Tables[n]
			fmt.Fprintf(&b, "\n## Table %s (~%d rows)\nColumns:\n", n, def.Rows)
			for _, c := range def.Columns {
				fmt.Fprintf(&b, "- %s %s\n", c.Name, c.Type)
			}
			b.WriteString("Indexes:\n")
			for _, ix := range def.Indexes {
				fmt.Fprintf(&b, "- %s (%s)%s\n", ix.Name, strings.Join(ix.Columns, ", "), map[bool]string{true: " unique", false: ""}[ix.Unique])
			}
		}
	}

	b.WriteString("\n## How to do it\n")
	if nPlusOne {
		b.WriteString("1. Write a test that calls one of the routes above and counts the queries, and watch it fail: the count grows with the number of items.\n")
		b.WriteString("2. Change the code so the data is loaded once for all the items; keep the output of the route identical.\n")
		b.WriteString("3. Run the test again: the number of queries no longer depends on the number of items.\n")
	} else {
		b.WriteString("1. Read the code that builds the query and check how often it runs and with which filters.\n")
		b.WriteString("2. Prefer a rewrite that uses an existing index; add an index only if none fits, as a migration that does not lock a big table.\n")
		b.WriteString("3. Prove it: the same results before and after, and the plan (EXPLAIN) no longer scans the table.\n")
	}
	b.WriteString("4. Explain the change in the pull request with the numbers above.\n")
	if in.Link != "" {
		fmt.Fprintf(&b, "\nAfter the deploy, Slowpoke measures the same query again: %s\n", in.Link)
	}
	return b.String()
}
