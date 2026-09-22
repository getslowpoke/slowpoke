package agent

import (
	"regexp"
	"strings"
)

// Session, transaction and catalog statements run by drivers and pools on every
// request cost nothing and bury the queries worth fixing. The agent's own
// monitoring queries (performance_schema, pg_stat_*) are dropped too, with its EXPLAINs,
// its table descriptions and the catalog lookups drivers such as pgx run on connect.
var housekeepingRe = regexp.MustCompile(`^(set|use|begin|commit|rollback|start transaction|savepoint|release savepoint|show|discard|reset|deallocate|explain)\b` +
	`|^select (pg_catalog\.version\(\)|version\(\)|current_schema\(\)|database\(\)|@@)` +
	`|\binformation_schema\.|\bpg_catalog\.|\bfrom pg_(type|namespace|class|attribute|index|indexes|database|proc|range|enum)\b` +
	`|@@?wsrep_|\btime_zone(_transition|_transition_type|_name|_leap_second)?\b.*\b(engine|order by time_zone_id)`)

func ignoredStatement(sql string) bool {
	s := strings.ToLower(strings.TrimSpace(sql))
	return housekeepingRe.MatchString(s) || strings.Contains(s, "performance_schema.") || strings.Contains(s, "pg_stat_")
}

// thirdPartyCode tells framework and library files from the application's own code.
func thirdPartyCode(path string) bool {
	for _, dir := range []string{"/vendor/", "/node_modules/", "/site-packages/", "/dist-packages/", "/go/pkg/mod/", "/usr/lib/", "/usr/local/lib/"} {
		if strings.Contains(path, dir) {
			return true
		}
	}
	return strings.HasPrefix(path, "vendor/") || strings.HasPrefix(path, "node_modules/")
}
