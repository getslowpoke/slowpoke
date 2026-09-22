package fingerprint

import (
	"regexp"
	"strings"
)

var (
	redactString = regexp.MustCompile(`(?s)'(?:[^'\\]|\\.|'')*'`)
	redactNumber = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	limitClause  = regexp.MustCompile(`(?i)\b(limit|offset)\s+\d+(\s*,\s*\d+)?`)
)

// Redact removes the values of a statement but keeps what a diagnosis needs:
// whether a literal was a string or a number, where a LIKE wildcard was, and
// the LIMIT/OFFSET sizes. Real samples never leave the machine by default.
func Redact(sql string) string {
	if len(sql) > maxLen {
		sql = sql[:maxLen]
	}
	sql = redactString.ReplaceAllStringFunc(sql, func(lit string) string {
		inner := lit[1 : len(lit)-1]
		out := "s"
		if strings.HasPrefix(inner, "%") {
			out = "%" + out
		}
		if strings.HasSuffix(inner, "%") && len(inner) > 1 {
			out += "%"
		}
		return "'" + out + "'"
	})

	// Numbers become 0, except inside LIMIT/OFFSET clauses.
	protected := limitClause.FindAllStringIndex(sql, -1)
	inLimit := func(pos int) bool {
		for _, r := range protected {
			if pos >= r[0] && pos < r[1] {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	last := 0
	for _, m := range redactNumber.FindAllStringIndex(sql, -1) {
		// $1 is a PostgreSQL placeholder, not a value: the generic plan needs it.
		if inLimit(m[0]) || m[0] > 0 && sql[m[0]-1] == '$' {
			continue
		}
		b.WriteString(sql[last:m[0]])
		b.WriteString("0")
		last = m[1]
	}
	b.WriteString(sql[last:])
	sql = b.String()
	return sql
}
