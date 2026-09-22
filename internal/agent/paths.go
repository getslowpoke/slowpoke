package agent

import (
	"regexp"
	"strings"
)

var pathRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(/|$)`), "/{uuid}$1"},
	{regexp.MustCompile(`/\d+(/|$)`), "/{id}$1"},
	{regexp.MustCompile(`(?i)/[0-9a-f]{16,}(/|$)`), "/{hash}$1"},
	{regexp.MustCompile(`/[A-Za-z0-9_\-=%.]{40,}(/|$)`), "/{token}$1"},
}

// NormalizePath turns "/api/orders/4848337?x=1" into "/api/orders/{id}".
func NormalizePath(uri string) string {
	path := uri
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if path == "" {
		return "/"
	}
	// Twice: RE2 has no lookahead, adjacent segments share the slash.
	for range 2 {
		for _, r := range pathRules {
			path = r.re.ReplaceAllString(path, r.repl)
		}
	}
	return truncate(path, 120)
}
