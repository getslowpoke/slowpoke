package agent

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	userHostRe  = regexp.MustCompile(`^# User@Host: ([^\[\s]+)`)
	queryTimeRe = regexp.MustCompile(`Query_time: ([\d.]+)`)
	rowsExamRe  = regexp.MustCompile(`Rows_examined: (\d+)`)
	schemaRe    = regexp.MustCompile(`Schema: (\S+)`)
	timestampRe = regexp.MustCompile(`^SET timestamp=(\d+);$`)
	useRe       = regexp.MustCompile(`(?i)^use (\S+);$`)
)

// mysqlSlowParser reads the slow query log of MySQL 5.7/8 and MariaDB.
type mysqlSlowParser struct {
	src     Source
	agg     *Aggregator
	ignore  map[string]bool
	current *slowEntry
	schema  string
}

type slowEntry struct {
	user    string
	db      string
	seconds float64
	rows    int64
	ts      int64
	sql     strings.Builder
	hasTime bool
}

func NewMySQLSlowParser(src Source, agg *Aggregator) LineParser {
	ignore := map[string]bool{}
	for _, u := range src.IgnoreUsers {
		ignore[strings.ToLower(u)] = true
	}
	return &mysqlSlowParser{src: src, agg: agg, ignore: ignore}
}

func (p *mysqlSlowParser) Feed(line string) {
	if strings.HasPrefix(line, "# User@Host:") {
		p.current = &slowEntry{db: p.schema}
		if m := userHostRe.FindStringSubmatch(line); m != nil {
			p.current.user = m[1]
		}
		return
	}
	e := p.current
	if e == nil {
		return
	}
	if strings.HasPrefix(line, "#") {
		if m := queryTimeRe.FindStringSubmatch(line); m != nil {
			e.seconds, _ = strconv.ParseFloat(m[1], 64)
			e.hasTime = true
		}
		if m := rowsExamRe.FindStringSubmatch(line); m != nil {
			e.rows, _ = strconv.ParseInt(m[1], 10, 64)
		}
		if m := schemaRe.FindStringSubmatch(line); m != nil {
			e.db = m[1]
			p.schema = m[1]
		}
		return
	}
	if m := timestampRe.FindStringSubmatch(line); m != nil {
		e.ts, _ = strconv.ParseInt(m[1], 10, 64)
		return
	}
	if m := useRe.FindStringSubmatch(line); m != nil && e.sql.Len() == 0 {
		e.db = m[1]
		p.schema = m[1]
		return
	}

	if e.sql.Len() > 0 {
		e.sql.WriteByte(' ')
	}
	if e.sql.Len() < maxSampleLen*2 {
		e.sql.WriteString(strings.TrimSpace(line))
	}
	if strings.HasSuffix(strings.TrimSpace(line), ";") {
		p.flush()
	}
}

func (p *mysqlSlowParser) flush() {
	e := p.current
	p.current = nil
	if e == nil || !e.hasTime || e.ts == 0 || p.ignore[strings.ToLower(e.user)] || !p.src.schemaAllowed(e.db) {
		return
	}
	sql := strings.TrimRight(strings.TrimSpace(e.sql.String()), ";")
	// Normalize the whitespace of multi-line statements for a readable sample.
	sql = strings.Join(strings.Fields(sql), " ")
	if sql == "" || strings.HasPrefix(strings.ToLower(sql), "# administrator command") {
		return
	}
	p.agg.AddSlowQuery("mysql-slowlog", SlowQuery{
		Time:     time.Unix(e.ts, 0),
		User:     e.user,
		Database: e.db,
		Seconds:  e.seconds,
		Rows:     e.rows,
		SQL:      sql,
	})
}
