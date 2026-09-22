package retention

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// Compact turns the minutes of custom metrics and machines older than the detail window into
// one row per hour, bucket at the start of the hour. Nothing reads them minute by minute past
// the first day (charts over a week or a month step by the hour or more), and every reader
// sums, averages or takes maxima per step, so the numbers stay the same from sixty times fewer
// rows. On a real installation these two tables were 170 of 200 MB after four days.
//
// An hour is compacted when it still holds a row that is not at its start; running it again,
// or folding a late minute into an hour already compacted, gives the same totals.
func Compact(ctx context.Context, db *store.DB, now time.Time, p Policy) error {
	cutoff := now.Add(-p.Detail).Truncate(time.Hour).Unix()
	if err := compactDays(ctx, db, now, p); err != nil {
		return err
	}
	for _, t := range []struct {
		table string
		hour  func(context.Context, store.Q, int64) error
	}{
		{"metric_minutes", compactMetricHour},
		{"host_minutes", compactHostHour},
	} {
		hours, err := pendingHours(ctx, db, t.table, cutoff)
		if err != nil {
			return err
		}
		// One transaction per hour: SQLite has a single writer and ingestion must not wait long.
		for _, h := range hours {
			if err := db.Tx(ctx, func(q store.Q) error { return t.hour(ctx, q, h) }); err != nil {
				return fmt.Errorf("compact %s at %d: %w", t.table, h, err)
			}
		}
	}
	return nil
}

func pendingHours(ctx context.Context, db *store.DB, table string, cutoff int64) ([]int64, error) {
	rows, err := db.Q().Query(ctx, `SELECT DISTINCT bucket - (bucket % 3600) FROM `+table+` WHERE bucket < ? AND bucket % 3600 <> 0 ORDER BY 1`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var h int64
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

type metricKey struct {
	project, host int64
	name          string
}

type metricHour struct {
	kind     string
	count    int64
	sum      float64
	min, max float64
	last     float64
	hist     [15]int64
}

func compactMetricHour(ctx context.Context, q store.Q, hour int64) error {
	hcols := make([]string, 15)
	for i := range hcols {
		hcols[i] = fmt.Sprintf("h%d", i)
	}
	// Folded in Go, in bucket order: the last value of the hour is the one of its latest minute.
	rows, err := q.Query(ctx, `SELECT project_id, host_id, name, kind, count, sum, min, max, last, `+strings.Join(hcols, ", ")+`
		FROM metric_minutes WHERE bucket >= ? AND bucket < ? ORDER BY bucket`, hour, hour+3600)
	if err != nil {
		return err
	}
	var order []metricKey
	byKey := map[metricKey]*metricHour{}
	for rows.Next() {
		var k metricKey
		var r metricHour
		var count int64
		var sum, min, max, last float64
		dest := []any{&k.project, &k.host, &k.name, &r.kind, &count, &sum, &min, &max, &last}
		for i := range r.hist {
			dest = append(dest, &r.hist[i])
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return err
		}
		m := byKey[k]
		if m == nil {
			m = &metricHour{kind: r.kind, min: min, max: max}
			byKey[k] = m
			order = append(order, k)
		}
		m.count += count
		m.sum += sum
		m.min = math.Min(m.min, min)
		m.max = math.Max(m.max, max)
		m.last = last
		for i := range m.hist {
			m.hist[i] += r.hist[i]
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM metric_minutes WHERE bucket >= ? AND bucket < ?`, hour, hour+3600); err != nil {
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", 10+15), ", ")
	for _, k := range order {
		m := byKey[k]
		args := []any{k.project, k.host, k.name, hour, m.kind, m.count, m.sum, m.min, m.max, m.last}
		for _, h := range m.hist {
			args = append(args, h)
		}
		if _, err := q.Exec(ctx, `INSERT INTO metric_minutes (project_id, host_id, name, bucket, kind, count, sum, min, max, last, `+strings.Join(hcols, ", ")+`)
			VALUES (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

// Columns of a machine's minute and how an hour keeps them: gauges their average, sizes their
// largest value, counters their sum. top_processes is only read for the latest minute, so an
// hour drops it (it was 400 of the ~600 bytes of a row).
var hostColumns = []struct {
	name, agg string
	integer   bool
}{
	{"cpu", "AVG", false}, {"cpu_iowait", "AVG", false}, {"cpu_steal", "AVG", false},
	{"load1", "AVG", false}, {"load5", "AVG", false}, {"load15", "AVG", false},
	{"mem_used", "AVG", true}, {"swap_used", "AVG", true}, {"disk_used", "AVG", true},
	{"disk_read_bps", "AVG", false}, {"disk_write_bps", "AVG", false}, {"net_rx_bps", "AVG", false}, {"net_tx_bps", "AVG", false},
	{"mem_total", "MAX", true}, {"swap_total", "MAX", true}, {"disk_total", "MAX", true}, {"cpus", "MAX", true},
	{"swap_out_pages", "SUM", true}, {"oom_kills", "SUM", true},
}

func compactHostHour(ctx context.Context, q store.Q, hour int64) error {
	selects := make([]string, 0, len(hostColumns)+1)
	names := make([]string, 0, len(hostColumns)+1)
	for _, c := range hostColumns {
		// A double on both engines: PostgreSQL's AVG and SUM of a BIGINT are numeric.
		selects = append(selects, fmt.Sprintf("CAST(%s(%s) AS DOUBLE PRECISION)", c.agg, c.name))
		names = append(names, c.name)
	}
	selects = append(selects, "CAST(MAX(COALESCE(cpu_peak, cpu)) AS DOUBLE PRECISION)")
	names = append(names, "cpu_peak")

	rows, err := q.Query(ctx, `SELECT host_id, `+strings.Join(selects, ", ")+` FROM host_minutes WHERE bucket >= ? AND bucket < ? GROUP BY host_id`, hour, hour+3600)
	if err != nil {
		return err
	}
	type hostHour struct {
		id     int64
		values []sql.NullFloat64
	}
	var hosts []hostHour
	for rows.Next() {
		h := hostHour{values: make([]sql.NullFloat64, len(names))}
		dest := []any{&h.id}
		for i := range h.values {
			dest = append(dest, &h.values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return err
		}
		hosts = append(hosts, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM host_minutes WHERE bucket >= ? AND bucket < ?`, hour, hour+3600); err != nil {
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(names)+2), ", ")
	for _, h := range hosts {
		args := []any{h.id, hour}
		for i, v := range h.values {
			switch {
			case !v.Valid:
				args = append(args, nil)
			case i < len(hostColumns) && hostColumns[i].integer:
				args = append(args, int64(math.Round(v.Float64)))
			default:
				args = append(args, v.Float64)
			}
		}
		if _, err := q.Exec(ctx, `INSERT INTO host_minutes (host_id, bucket, `+strings.Join(names, ", ")+`) VALUES (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

// A table of hours that becomes one row per day past Policy.Hourly: the key without the hour,
// the columns added up and the ones that keep their largest value.
type dayTable struct {
	table        string
	key          []string
	ints, floats []string // added up
	maxima       []string // floats, largest value
}

func endpointHistogram() []string {
	cols := make([]string, 15)
	for i := range cols {
		cols[i] = fmt.Sprintf("h%d", i)
	}
	return cols
}

// Past three weeks nothing reads these by the hour: a deploy is compared with the same hour up to
// 14 days before it, the pages and the missions read totals over at most 30 days (60 for the
// comparison with the previous window). Their edges then fall on a day instead of an hour.
// metrics.subtractExcluded spreads an endpoint hour over the part inside its window, but only for
// sources with minutes of requests, and those are kept 14 days: it never meets a day row.
var dayTables = []dayTable{
	{"endpoint_hours", []string{"project_id", "source", "endpoint"}, append([]string{"n", "slow", "e5"}, endpointHistogram()...), []string{"sum", "slow_sum"}, []string{"max"}},
	{"query_hours", []string{"fingerprint_id", "source", "host_id", "project_id"}, []string{"n", "rows"}, []string{"sum"}, []string{"max"}},
	{"php_stall_hours", []string{"project_id", "source", "frame", "code"}, []string{"n"}, nil, nil},
}

func compactDays(ctx context.Context, db *store.DB, now time.Time, p Policy) error {
	cutoff := now.Add(-p.Hourly).Truncate(24 * time.Hour).Unix()
	for _, t := range dayTables {
		rows, err := db.Q().Query(ctx, `SELECT DISTINCT hour - (hour % 86400) FROM `+t.table+` WHERE hour < ? AND hour % 86400 <> 0 ORDER BY 1`, cutoff)
		if err != nil {
			return err
		}
		var days []int64
		for rows.Next() {
			var d int64
			if err := rows.Scan(&d); err != nil {
				rows.Close()
				return err
			}
			days = append(days, d)
		}
		rows.Close()
		for _, d := range days {
			if err := db.Tx(ctx, func(q store.Q) error { return compactDay(ctx, q, t, d) }); err != nil {
				return fmt.Errorf("compact %s at %d: %w", t.table, d, err)
			}
		}
	}
	return nil
}

func compactDay(ctx context.Context, q store.Q, t dayTable, day int64) error {
	var selects, cols []string
	for _, c := range t.key {
		selects, cols = append(selects, c), append(cols, c)
	}
	for _, c := range t.ints {
		selects, cols = append(selects, "SUM("+c+")"), append(cols, c)
	}
	for _, c := range t.floats {
		selects, cols = append(selects, "CAST(SUM("+c+") AS DOUBLE PRECISION)"), append(cols, c)
	}
	for _, c := range t.maxima {
		selects, cols = append(selects, "CAST(MAX("+c+") AS DOUBLE PRECISION)"), append(cols, c)
	}
	rows, err := q.Query(ctx, `SELECT `+strings.Join(selects, ", ")+` FROM `+t.table+` WHERE hour >= ? AND hour < ? GROUP BY `+strings.Join(t.key, ", "), day, day+86400)
	if err != nil {
		return err
	}
	var groups [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range vals {
			switch {
			case i >= len(t.key)+len(t.ints):
				dest[i] = new(float64)
			case i >= len(t.key) || isIDColumn(t.key[i]):
				dest[i] = new(int64)
			default:
				dest[i] = new(string)
			}
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return err
		}
		for i, d := range dest {
			switch v := d.(type) {
			case *float64:
				vals[i] = *v
			case *int64:
				vals[i] = *v
			case *string:
				vals[i] = *v
			}
		}
		groups = append(groups, vals)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM `+t.table+` WHERE hour >= ? AND hour < ?`, day, day+86400); err != nil {
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(cols)+1), ", ")
	for _, vals := range groups {
		if _, err := q.Exec(ctx, `INSERT INTO `+t.table+` (hour, `+strings.Join(cols, ", ")+`) VALUES (`+placeholders+`)`, append([]any{day}, vals...)...); err != nil {
			return err
		}
	}
	return nil
}

func isIDColumn(c string) bool { return strings.HasSuffix(c, "_id") }
