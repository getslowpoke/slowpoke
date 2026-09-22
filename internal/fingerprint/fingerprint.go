// Package fingerprint normalizes SQL so that queries differing only in their
// values share the same fingerprint.
//
// The same algorithm lives in the Laravel package (PHP): both are tested
// against spec/fingerprint_fixtures.json. Change them together.
package fingerprint

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"
)

type rule struct {
	re   *regexp.Regexp
	repl string
}

var values = []rule{
	{regexp.MustCompile(`(?s)/\*.*?\*/`), " "},
	{regexp.MustCompile(`(?s)'(?:[^'\\]|\\.|'')*'`), "?"},
	{regexp.MustCompile("[`\"]"), ""},
	{regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b`), "?"},
	{regexp.MustCompile(`\$\d+`), "?"},
	{regexp.MustCompile(`%\([A-Za-z_]\w*\)s|%s`), "?"},             // Python drivers
	{regexp.MustCompile(`(^|[^:\w]):[A-Za-z_]\w*`), "${1}?"},        // named bindings, not ::casts
	{regexp.MustCompile(`\b\d+(?:\.\d+)?\b`), "?"},
}

var spacing = []rule{
	{regexp.MustCompile(`\s+`), " "},
	{regexp.MustCompile(`\(\s+`), "("},
	{regexp.MustCompile(`\s+\)`), ")"},
	{regexp.MustCompile(`\s*,\s*`), ", "},
	{regexp.MustCompile(`\s*(<>|!=|>=|<=|=|<|>)\s*`), " $1 "},
	{regexp.MustCompile(`\b(in|values) \(\?(?:, \?)*\)(?:, \(\?(?:, \?)*\))*`), "$1 (...)"},
	{regexp.MustCompile(` {2,}`), " "},
}

const maxLen = 20000

// Of returns the fingerprint of a SQL statement.
func Of(sql string) string {
	if len(sql) > maxLen {
		sql = sql[:maxLen]
	}
	for _, r := range values {
		sql = r.re.ReplaceAllString(sql, r.repl)
	}
	sql = strings.ToLower(sql)
	for _, r := range spacing {
		sql = r.re.ReplaceAllString(sql, r.repl)
	}
	sql = joinCalls(sql)
	return strings.TrimRight(strings.TrimSpace(sql), "; ")
}

// callSpace finds "name (": performance_schema digests write "COUNT ( * )" where the
// application wrote "COUNT(*)". Keywords and table names before a column list keep the space.
var callSpace = regexp.MustCompile(`\b[a-z_][a-z0-9_]* \(`)

var tableBefore = regexp.MustCompile(`\b(into|table|update|join|from) $`)

func joinCalls(sql string) string {
	var b strings.Builder
	last := 0
	for _, m := range callSpace.FindAllStringIndex(sql, -1) {
		word := sql[m[0] : m[1]-2]
		if keywords[word] || tableBefore.MatchString(sql[:m[0]]) {
			continue
		}
		b.WriteString(sql[last:m[0]])
		b.WriteString(word)
		b.WriteString("(")
		last = m[1]
	}
	b.WriteString(sql[last:])
	return b.String()
}

var keywords = map[string]bool{}

func init() {
	for _, k := range strings.Fields(`in values from join and or not exists as on where select using over into table set
		when then else by having union all any some with returning limit offset is between like ilike case distinct
		update delete insert lateral filter partition within recursive conflict key index check references default
		primary unique foreign constraint do nothing`) {
		keywords[k] = true
	}
}

// Hash is the short stable identifier of a fingerprint.
func Hash(fp string) string {
	sum := sha1.Sum([]byte(fp))
	return hex.EncodeToString(sum[:])[:16]
}
