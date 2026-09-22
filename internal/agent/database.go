package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/sqlsafe"
)

// digestRow is one row of performance_schema digests or pg_stat_statements.
type digestRow struct {
	Schema     string
	Text       string
	Sample     string
	Count      int64
	Seconds    float64
	MaxSeconds float64
	Rows       int64
}

// digestDeltas turns two snapshots of cumulative counters into the statistics
// of the interval between them.
func digestDeltas(prev, cur map[string]digestRow, slowSeconds float64, countSlow bool) (map[string]protocol.QueryStat, int, float64, float64) {
	stats := map[string]protocol.QueryStat{}
	var slowN int
	var slowSum, slowMax float64

	for key, c := range cur {
		p, known := prev[key]
		dn, ds, dr := c.Count, c.Seconds, c.Rows
		if known && c.Count >= p.Count {
			dn, ds, dr = c.Count-p.Count, c.Seconds-p.Seconds, c.Rows-p.Rows
		}
		if dn <= 0 {
			continue
		}

		fp := fingerprint.Of(c.Text)
		hash := fingerprint.Hash(fp)
		s := stats[hash]
		s.Fingerprint = truncate(fp, maxSampleLen)
		s.Database = c.Schema
		if c.Sample != "" {
			s.Sample = truncate(c.Sample, maxSampleLen)
		}
		s.N += dn
		s.Sum += ds
		s.Rows += dr
		s.Max = max(s.Max, c.MaxSeconds)
		stats[hash] = s

		if countSlow && ds/float64(dn) >= slowSeconds {
			slowN += int(dn)
			slowSum += ds
			slowMax = max(slowMax, c.MaxSeconds, ds/float64(dn))
		}
	}
	return stats, slowN, slowSum, slowMax
}

// dbSource polls a MySQL/MariaDB or PostgreSQL server.
type dbSource struct {
	src         Source
	id          string // position among the runner's databases: names default to the type and may repeat
	engine      string
	db          *sql.DB
	prevDigests map[string]digestRow
	prevStatus  map[string]float64
	prevAt      time.Time
	countSlow   bool
}

// hasUnsetPlaceholder tells a DSN that still carries a ${VAR} from the generated config apart
// from a real password: a shell would have expanded it, agent.yaml is not a shell.
func hasUnsetPlaceholder(dsn string) bool {
	i := strings.Index(dsn, "${")
	return i >= 0 && strings.Contains(dsn[i:], "}")
}

func openDBSource(src Source, countSlow bool) (*dbSource, error) {
	driver, engine := "mysql", "mysql"
	if src.Type == "postgres" {
		driver, engine = "pgx", "postgres"
	}
	db, err := sql.Open(driver, src.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(10 * time.Minute)
	return &dbSource{src: src, engine: engine, db: db, countSlow: countSlow}, nil
}

func (d *dbSource) sampleOwner() SampleOwner {
	return SampleOwner{Engine: d.engine, Source: d.id}
}

func (d *dbSource) poll(ctx context.Context, agg *Aggregator, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var errs []string
	if d.src.enabled(d.src.Status) {
		if err := d.pollStatus(ctx, agg, now); err != nil {
			errs = append(errs, "status: "+err.Error())
		}
	}
	if d.src.enabled(d.src.Digests) {
		if err := d.pollDigests(ctx, agg, now); err != nil {
			errs = append(errs, "digests: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", d.src.Name, strings.Join(errs, "; "))
	}
	return nil
}

func (d *dbSource) pollStatus(ctx context.Context, agg *Aggregator, now time.Time) error {
	values := map[string]float64{}
	if d.engine == "mysql" {
		rows, err := d.db.QueryContext(ctx, "SHOW GLOBAL STATUS WHERE Variable_name IN ('Questions','Innodb_rows_read','Threads_running','Threads_connected')")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var value float64
			if err := rows.Scan(&name, &value); err == nil {
				values[name] = value
			}
		}
	} else {
		var xact, fetched, active, total float64
		err := d.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(xact_commit + xact_rollback),0), COALESCE(SUM(tup_fetched),0),
			(SELECT COUNT(*) FROM pg_stat_activity WHERE state = 'active'), (SELECT COUNT(*) FROM pg_stat_activity)
			FROM pg_stat_database`).Scan(&xact, &fetched, &active, &total)
		if err != nil {
			return err
		}
		values = map[string]float64{"Questions": xact, "Innodb_rows_read": fetched, "Threads_running": active, "Threads_connected": total}
	}

	if d.prevStatus != nil {
		seconds := now.Sub(d.prevAt).Seconds()
		rate := func(k string) float64 {
			if seconds <= 0 || values[k] < d.prevStatus[k] {
				return 0
			}
			return (values[k] - d.prevStatus[k]) / seconds
		}
		agg.SetDBStatus(protocol.DBStatus{
			Minute:           minuteOf(now),
			QPS:              rate("Questions"),
			RowsReadPS:       rate("Innodb_rows_read"),
			ThreadsRunning:   int(values["Threads_running"]),
			ThreadsConnected: int(values["Threads_connected"]),
		})
	}
	d.prevStatus, d.prevAt = values, now
	return nil
}

func (d *dbSource) pollDigests(ctx context.Context, agg *Aggregator, now time.Time) error {
	cur, err := d.readDigests(ctx)
	cur = d.filterSchemas(cur)
	if err != nil {
		return err
	}
	if d.prevDigests != nil {
		stats, slowN, slowSum, slowMax := digestDeltas(d.prevDigests, cur, d.src.SlowQuerySeconds, d.countSlow)
		source := "mysql-digest"
		if d.engine == "postgres" {
			source = "pg-statements"
		}
		agg.AddQueryStats(source, d.sampleOwner(), now, stats, slowN, slowSum, slowMax)
	}
	d.prevDigests = cur
	return nil
}

// filterSchemas keeps the statements of the databases listed in the source (all without a list).
func (d *dbSource) filterSchemas(rows map[string]digestRow) map[string]digestRow {
	if len(d.src.Schemas) == 0 {
		return rows
	}
	for key, r := range rows {
		if !d.src.schemaAllowed(r.Schema) {
			delete(rows, key)
		}
	}
	return rows
}

func (d *dbSource) readDigests(ctx context.Context) (map[string]digestRow, error) {
	out := map[string]digestRow{}
	if d.engine == "mysql" {
		const base = `SELECT COALESCE(SCHEMA_NAME,''), DIGEST, DIGEST_TEXT, COUNT_STAR, SUM_TIMER_WAIT/1e12, MAX_TIMER_WAIT/1e12, SUM_ROWS_EXAMINED%s
			FROM performance_schema.events_statements_summary_by_digest WHERE DIGEST_TEXT IS NOT NULL ORDER BY SUM_TIMER_WAIT DESC LIMIT 1000`
		// QUERY_SAMPLE_TEXT exists on MySQL 8 only.
		rows, err := d.db.QueryContext(ctx, fmt.Sprintf(base, ", COALESCE(QUERY_SAMPLE_TEXT,'')"))
		withSample := err == nil
		if err != nil {
			if rows, err = d.db.QueryContext(ctx, fmt.Sprintf(base, "")); err != nil {
				return nil, err
			}
		}
		defer rows.Close()
		for rows.Next() {
			var r digestRow
			var digest string
			dest := []any{&r.Schema, &digest, &r.Text, &r.Count, &r.Seconds, &r.MaxSeconds, &r.Rows}
			if withSample {
				dest = append(dest, &r.Sample)
			}
			if err := rows.Scan(dest...); err != nil {
				return nil, err
			}
			if strings.HasPrefix(r.Text, "SHOW ") || strings.Contains(r.Text, "performance_schema") {
				continue
			}
			out[r.Schema+"|"+digest] = r
		}
		return out, rows.Err()
	}

	rows, err := d.db.QueryContext(ctx, `SELECT d.datname, s.queryid::text, s.query, s.calls, s.total_exec_time/1000, s.max_exec_time/1000, s.rows
		FROM pg_stat_statements s JOIN pg_database d ON d.oid = s.dbid ORDER BY s.total_exec_time DESC LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r digestRow
		var id string
		if err := rows.Scan(&r.Schema, &id, &r.Text, &r.Count, &r.Seconds, &r.MaxSeconds, &r.Rows); err != nil {
			return nil, err
		}
		if strings.Contains(r.Text, "pg_stat_statements") {
			continue
		}
		// The text has $n placeholders instead of values: it is the sample PostgreSQL 16+
		// can explain with a generic plan.
		r.Sample = r.Text
		out[r.Schema+"|"+id] = r
	}
	return out, rows.Err()
}

// genericPlan runs EXPLAIN (GENERIC_PLAN) inside the read-only transaction. It needs the
// simple query protocol: with the extended one the server would expect a value for each
// $n, and pgx refuses to send placeholders without arguments.
func genericPlan(ctx context.Context, conn *sql.Conn, tx *sql.Tx, body string) ([]map[string]any, error) {
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return nil, err
	}
	if version < 160000 {
		return nil, errors.New("PostgreSQL 16 or newer is needed to explain statements without values")
	}
	var raw string
	err := conn.Raw(func(driverConn any) error {
		c, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected driver connection %T", driverConn)
		}
		results, err := c.Conn().PgConn().Exec(ctx, "EXPLAIN (GENERIC_PLAN, FORMAT JSON) "+body).ReadAll()
		if err != nil {
			return err
		}
		if len(results) != 1 || len(results[0].Rows) == 0 || len(results[0].Rows[0]) == 0 {
			return errors.New("empty plan")
		}
		raw = string(results[0].Rows[0][0])
		return nil
	})
	if err != nil {
		return nil, err
	}
	return []map[string]any{{"QUERY PLAN": raw}}, nil
}

var tableNameRe = regexp.MustCompile(`^[A-Za-z0-9_$]+$`)

// explain runs EXPLAIN in a read-only transaction and describes the tables involved.
func (d *dbSource) explain(ctx context.Context, req protocol.ExplainRequest) protocol.Explain {
	out := protocol.Explain{Hash: req.Hash, Engine: d.engine}
	if !d.src.enabled(d.src.Explain) {
		out.Error = "explain disabled on this source"
		return out
	}
	if !sqlsafe.Explainable(req.SQL) {
		// pg_stat_statements only keeps statements with $n placeholders.
		if d.engine != "postgres" || !sqlsafe.ExplainableWithoutValues(req.SQL) {
			out.Error = "not a single read-only SELECT with real values"
			return out
		}
		out.Generic = true
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := d.db.Conn(ctx)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer conn.Close()

	if req.Database != "" && d.engine == "mysql" && tableNameRe.MatchString(req.Database) {
		if _, err := conn.ExecContext(ctx, "USE `"+req.Database+"`"); err != nil {
			out.Error = err.Error()
			return out
		}
	}

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer tx.Rollback()

	body := strings.TrimRight(strings.TrimSpace(req.SQL), ";")
	var plan []map[string]any
	switch {
	case out.Generic:
		plan, err = genericPlan(ctx, conn, tx, body)
	case d.engine == "postgres":
		plan, err = queryMaps(ctx, tx, "EXPLAIN (FORMAT JSON) "+body)
	default:
		plan, err = queryMaps(ctx, tx, "EXPLAIN "+body)
	}
	if err != nil {
		out.Error = err.Error()
		return out
	}

	var tables []string
	if d.engine == "mysql" {
		out.Plan, _ = json.Marshal(plan)
		for _, row := range plan {
			if t, _ := row["table"].(string); tableNameRe.MatchString(t) {
				tables = append(tables, t)
			}
		}
	} else if len(plan) > 0 {
		raw, _ := plan[0]["QUERY PLAN"].(string)
		out.Plan = json.RawMessage(raw)
		tables = pgRelations(out.Plan)
	}

	out.Tables = map[string]protocol.TableDef{}
	for _, t := range unique(tables) {
		if def, err := d.describe(ctx, tx, t); err == nil {
			out.Tables[t] = def
		}
	}
	return out
}

func (d *dbSource) describe(ctx context.Context, tx *sql.Tx, table string) (protocol.TableDef, error) {
	var def protocol.TableDef
	if d.engine == "mysql" {
		cols, err := queryMaps(ctx, tx, "SHOW COLUMNS FROM `"+table+"`")
		if err != nil {
			return def, err
		}
		for _, c := range cols {
			def.Columns = append(def.Columns, protocol.Column{Name: fmt.Sprint(c["Field"]), Type: fmt.Sprint(c["Type"])})
		}
		idx, err := queryMaps(ctx, tx, "SHOW INDEX FROM `"+table+"`")
		if err != nil {
			return def, err
		}
		byName := map[string]*protocol.Index{}
		var order []string
		for _, r := range idx {
			name := fmt.Sprint(r["Key_name"])
			if byName[name] == nil {
				byName[name] = &protocol.Index{Name: name, Unique: fmt.Sprint(r["Non_unique"]) == "0"}
				order = append(order, name)
			}
			byName[name].Columns = append(byName[name].Columns, fmt.Sprint(r["Column_name"]))
		}
		for _, n := range order {
			def.Indexes = append(def.Indexes, *byName[n])
		}
		_ = tx.QueryRowContext(ctx, "SELECT COALESCE(TABLE_ROWS,0) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", table).Scan(&def.Rows)
		return def, nil
	}

	cols, err := queryMaps(ctx, tx, "SELECT column_name, data_type FROM information_schema.columns WHERE table_name = $1 ORDER BY ordinal_position", table)
	if err != nil {
		return def, err
	}
	for _, c := range cols {
		def.Columns = append(def.Columns, protocol.Column{Name: fmt.Sprint(c["column_name"]), Type: fmt.Sprint(c["data_type"])})
	}
	idx, err := queryMaps(ctx, tx, "SELECT indexname, indexdef FROM pg_indexes WHERE tablename = $1", table)
	if err != nil {
		return def, err
	}
	colsRe := regexp.MustCompile(`\(([^)]*)\)`)
	for _, r := range idx {
		ix := protocol.Index{Name: fmt.Sprint(r["indexname"]), Unique: strings.Contains(fmt.Sprint(r["indexdef"]), "UNIQUE")}
		if m := colsRe.FindStringSubmatch(fmt.Sprint(r["indexdef"])); m != nil {
			for _, c := range strings.Split(m[1], ",") {
				ix.Columns = append(ix.Columns, strings.Trim(strings.TrimSpace(c), `"`))
			}
		}
		def.Indexes = append(def.Indexes, ix)
	}
	_ = tx.QueryRowContext(ctx, "SELECT COALESCE(reltuples,0)::bigint FROM pg_class WHERE relname = $1", table).Scan(&def.Rows)
	return def, nil
}

func pgRelations(plan json.RawMessage) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if r, ok := x["Relation Name"].(string); ok {
				out = append(out, r)
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	var decoded any
	_ = json.Unmarshal(plan, &decoded)
	walk(decoded)
	return out
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryMaps(ctx context.Context, q querier, statement string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, c := range cols {
			if b, ok := values[i].([]byte); ok {
				row[c] = string(b)
			} else {
				row[c] = values[i]
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
