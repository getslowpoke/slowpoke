// Package sqlsafe decides whether a statement can be EXPLAINed safely.
package sqlsafe

import (
	"regexp"
	"strings"
)

var (
	selectRe    = regexp.MustCompile(`(?is)^\s*(select|with)\b`)
	dangerousRe = regexp.MustCompile(`(?is)\b(for\s+update|lock\s+in\s+share\s+mode|into\s+outfile|into\s+dumpfile|sleep\s*\(|benchmark\s*\(|pg_sleep|get_lock\s*\(|insert|update|delete|replace|drop|alter|create|truncate|grant)\b`)
	stringsRe   = regexp.MustCompile(`'(?:[^'\\]|\\.|'')*'`)
	pgParamRe   = regexp.MustCompile(`\$\d`)
)

// Explainable accepts only a single read-only SELECT with real values (no placeholders).
// Explainable tells whether a statement with real values can be EXPLAINed: a single
// read-only SELECT with no placeholders.
func Explainable(q string) bool {
	bare, ok := readOnlySelect(q)
	return ok && !strings.Contains(bare, "?") && !pgParamRe.MatchString(bare)
}

// ExplainableWithoutValues also accepts PostgreSQL $n placeholders, for EXPLAIN (GENERIC_PLAN):
// the planner never needs the values, and the same read-only rules apply.
func ExplainableWithoutValues(q string) bool {
	bare, ok := readOnlySelect(q)
	return ok && !strings.Contains(bare, "?")
}

func readOnlySelect(q string) (string, bool) {
	if !selectRe.MatchString(q) {
		return "", false
	}
	bare := stringsRe.ReplaceAllString(q, "''")
	if dangerousRe.MatchString(bare) {
		return "", false
	}
	if strings.Contains(strings.TrimRight(strings.TrimSpace(bare), "; "), ";") {
		return "", false
	}
	return bare, true
}
