package agent

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// checkEvery is how often sources are checked: often enough for "check again" to feel quick,
// rarely enough to cost nothing (a few catalog queries and one file read per source).
const checkEvery = 10 * time.Minute

// nextCheckIn waits less after a problem: a database still starting, a permission just
// granted. The fix shows up in a minute instead of ten.
func nextCheckIn(checks []protocol.SourceCheck) time.Duration {
	for _, c := range checks {
		for _, it := range c.Items {
			if it.Status == StatusError {
				return time.Minute
			}
		}
	}
	return checkEvery
}

// webLogFacts reads the end of an access log and counts the lines that carry a duration.
func webLogFacts(path string, apache bool) fileFacts {
	f, err := os.Open(path)
	if err != nil {
		return fileFacts{ReadError: err.Error(), Missing: os.IsNotExist(err)}
	}
	defer f.Close()
	facts := fileFacts{Readable: true}
	info, err := f.Stat()
	if err != nil {
		return facts
	}
	const tail = 64 << 10
	offset := max(int64(0), info.Size()-tail)
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return facts
	}
	lines := strings.Split(string(buf), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:] // the first one is cut
	}
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "{") {
			if _, _, _, _, _, ok := parseNginxJSON(line); ok {
				facts.Lines++
				facts.WithDuration++
			}
			continue
		}
		if combinedRe.MatchString(line) {
			facts.Lines++
			if _, _, _, _, _, ok := parseNginxText(line, apache); ok {
				facts.WithDuration++
			}
		}
	}
	return facts
}

func plainFileFacts(path string) fileFacts {
	f, err := os.Open(path)
	if err != nil {
		return fileFacts{ReadError: err.Error(), Missing: os.IsNotExist(err)}
	}
	f.Close()
	return fileFacts{Readable: true}
}

func (d *dbSource) mysqlFacts(ctx context.Context, slowLogRead bool) (mysqlFacts, map[string]string) {
	f := mysqlFacts{DigestsEnabled: d.src.enabled(d.src.Digests), ExplainEnabled: d.src.enabled(d.src.Explain), Schemas: d.src.Schemas, SlowLogRead: slowLogRead, SelectOn: map[string]bool{}}
	facts := map[string]string{"schemas": strings.Join(d.src.Schemas, ",")}
	if cfg, err := mysql.ParseDSN(d.src.DSN); err == nil {
		facts["dsn_user"], facts["address"] = cfg.User, cfg.Addr
	}
	if d.db == nil {
		f.ConnectError = "not connected"
		return f, facts
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var user, slowFile string
	var perf, slowOn int
	var longQuery float64
	err := d.db.QueryRowContext(ctx, `SELECT VERSION(), CURRENT_USER(), @@performance_schema, @@slow_query_log, COALESCE(@@slow_query_log_file,''), @@long_query_time`).
		Scan(&f.Version, &user, &perf, &slowOn, &slowFile, &longQuery)
	if err != nil {
		f.ConnectError = err.Error()
		return f, facts
	}
	f.Connected, f.PerfSchema, f.SlowLogOn = true, perf == 1, slowOn == 1
	facts["version"], facts["user"], facts["slow_log_file"] = f.Version, user, slowFile
	facts["long_query_time"] = strconv.FormatFloat(longQuery, 'f', -1, 64)

	if f.PerfSchema {
		var n int
		if err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.events_statements_summary_by_digest`).Scan(&n); err != nil {
			f.DigestsError = err.Error()
		} else {
			f.DigestsReadable = true
			rows, err := d.db.QueryContext(ctx, `SELECT QUERY_SAMPLE_TEXT FROM performance_schema.events_statements_summary_by_digest LIMIT 0`)
			if err == nil {
				f.HasSamples = true
				rows.Close()
			}
		}
	}

	// CURRENT_USER() is user@host; information_schema writes the grantee as 'user'@'host'.
	if i := strings.LastIndex(user, "@"); i > 0 {
		grantee := "'" + user[:i] + "'@'" + user[i+1:] + "'"
		var global int
		_ = d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE GRANTEE = ? AND PRIVILEGE_TYPE = 'SELECT'`, grantee).Scan(&global)
		f.GlobalSelect = global > 0
		if rows, err := d.db.QueryContext(ctx, `SELECT DISTINCT TABLE_SCHEMA FROM information_schema.SCHEMA_PRIVILEGES WHERE GRANTEE = ? AND PRIVILEGE_TYPE = 'SELECT'`, grantee); err == nil {
			for rows.Next() {
				var schema string
				if rows.Scan(&schema) == nil {
					f.SelectOn[strings.ReplaceAll(schema, `\_`, "_")] = true
				}
			}
			rows.Close()
		}
	}
	return f, facts
}

func (d *dbSource) postgresFacts(ctx context.Context) (postgresFacts, map[string]string) {
	f := postgresFacts{DigestsEnabled: d.src.enabled(d.src.Digests), ExplainEnabled: d.src.enabled(d.src.Explain)}
	facts := map[string]string{"schemas": strings.Join(d.src.Schemas, ",")}
	if cfg, err := pgx.ParseConfig(d.src.DSN); err == nil {
		facts["dsn_user"], facts["address"], facts["database"] = cfg.User, cfg.Host+":"+strconv.Itoa(int(cfg.Port)), cfg.Database
	}
	if d.db == nil {
		f.ConnectError = "not connected"
		return f, facts
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var user, preload string
	var hasExtension bool
	err := d.db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int, current_user, current_setting('shared_preload_libraries'),
		EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`).Scan(&f.VersionNum, &user, &preload, &hasExtension)
	if err != nil {
		f.ConnectError = err.Error()
		return f, facts
	}
	f.Connected = true
	facts["version"], facts["user"] = strconv.Itoa(f.VersionNum), user
	f.StatementsLoaded = hasExtension && strings.Contains(preload, "pg_stat_statements")
	if f.StatementsLoaded {
		var n int
		if err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_statements`).Scan(&n); err != nil {
			f.StatementsError = err.Error()
		} else {
			f.StatementsReadable = true
		}
	}
	_ = d.db.QueryRowContext(ctx, `SELECT pg_has_role(current_user, 'pg_read_all_stats', 'MEMBER')`).Scan(&f.ReadAllStats)
	var readAllData bool
	if f.VersionNum >= 140000 {
		_ = d.db.QueryRowContext(ctx, `SELECT pg_has_role(current_user, 'pg_read_all_data', 'MEMBER')`).Scan(&readAllData)
	}
	var anyTable bool
	_ = d.db.QueryRowContext(ctx, `SELECT COALESCE(bool_or(has_table_privilege(c.oid, 'SELECT')), false) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p') AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg_toast%'`).Scan(&anyTable)
	f.CanSelect = readAllData || anyTable
	return f, facts
}

// dsnAddress is the host:port the guide has to write back into the dsn line. The password is a
// placeholder, so the DSN may not parse: fall back to the default of the engine.
func dsnAddress(s Source) string {
	if s.Type == "postgres" {
		if u, err := url.Parse(s.DSN); err == nil && u.Host != "" && !strings.Contains(u.Host, "${") {
			return u.Host
		}
		return "127.0.0.1:5432"
	}
	if i, j := strings.Index(s.DSN, "tcp("), strings.Index(s.DSN, ")"); i >= 0 && j > i {
		return s.DSN[i+4 : j]
	}
	return "127.0.0.1:3306"
}

// checks describes every configured source, in the order of the configuration.
func (r *Runner) checks(ctx context.Context, now time.Time) []protocol.SourceCheck {
	var out []protocol.SourceCheck
	slowLogRead := false
	for _, f := range r.files {
		var facts fileFacts
		switch f.src.Type {
		case "nginx", "apache", "caddy":
			facts = webLogFacts(f.src.Path, f.src.Type == "apache" || f.src.Format == "apache")
		default:
			facts = plainFileFacts(f.src.Path)
			if f.src.Type == "mysql-slowlog" && facts.Readable {
				slowLogRead = true
			}
		}
		out = append(out, protocol.SourceCheck{Source: f.src.Type + ":" + f.src.Name, Items: fileCheckItems(f.src.Type, facts),
			Facts: map[string]string{"path": f.src.Path, "schemas": strings.Join(f.src.Schemas, ",")}})
	}
	for _, d := range r.dbs {
		check := protocol.SourceCheck{Source: d.src.Type + ":" + d.src.Name}
		if d.engine == "mysql" {
			var f mysqlFacts
			f, check.Facts = d.mysqlFacts(ctx, slowLogRead)
			check.Items = mysqlCheckItems(f)
		} else {
			var f postgresFacts
			f, check.Facts = d.postgresFacts(ctx)
			check.Items = postgresCheckItems(f)
		}
		out = append(out, check)
	}
	// Configured but not opened: the panel shows the source with the lines that finish it.
	for _, s := range r.waitingDBs {
		out = append(out, protocol.SourceCheck{Source: s.Type + ":" + s.Name,
			Items: []protocol.CheckItem{{Key: "connection", Status: StatusOff, Reason: "needs_password"}},
			Facts: map[string]string{"address": dsnAddress(s)}})
	}
	for _, rs := range r.redises {
		facts, items := rs.check(ctx)
		out = append(out, protocol.SourceCheck{Source: "redis:" + rs.src.Name, Items: items, Facts: facts})
	}
	for _, rcv := range r.otlpReceivers {
		status, reason := StatusWaiting, "no_traces_yet"
		facts := map[string]string{"listen": rcv.addr}
		if last := rcv.lastTrace(); !last.IsZero() {
			facts["last_trace_at"] = strconv.FormatInt(last.Unix(), 10)
			if time.Since(last) < time.Hour {
				status, reason = StatusOK, ""
			} else {
				reason = "no_recent_traces"
			}
		}
		if rcv.listenError != "" {
			status, reason = StatusError, "cannot_listen"
		}
		out = append(out, protocol.SourceCheck{Source: "otlp:" + rcv.name, Items: []protocol.CheckItem{item("receiving", status, false, reason, rcv.listenError)}, Facts: facts})
	}
	if r.host != nil {
		facts := map[string]string{"root": r.host.root}
		// What the agent costs this machine, measured on itself.
		for k, v := range r.cost.read(filepath.Join(r.host.root, "proc", "self"), now) {
			facts[k] = v
		}
		out = append(out, protocol.SourceCheck{Source: "host:host", Items: hostCheckItems(filepath.Join(r.host.root, "proc")), Facts: facts})
	}
	return out
}
