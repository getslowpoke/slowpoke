package agent

import (
	"strings"
	"testing"
)

// A staging API uses a database on a MySQL server that also hosts production
// databases (a WordPress site, other apps). One agent reads the whole server, so without a
// filter production queries would land in the staging project.
func TestSchemasFilterKeepsOnlyTheListedDatabases(t *testing.T) {
	src := Source{Type: "mysql-slowlog", Schemas: []string{"shop_testing"}}
	for name, want := range map[string]bool{"shop_testing": true, "SHOP_TESTING": true, "blog_wordpress": false, "": false} {
		if got := src.schemaAllowed(name); got != want {
			t.Errorf("%q: got %v want %v", name, got, want)
		}
	}
	if !(Source{}).schemaAllowed("") || !(Source{}).schemaAllowed("anything") {
		t.Fatal("without schemas every database is kept")
	}

	log := `# Time: 2026-09-17T10:00:03.123456Z
# User@Host: app[app] @  [10.0.0.5]  Id:    12
# Query_time: 1.500000  Lock_time: 0.000002 Rows_sent: 1  Rows_examined: 100000
use shop_testing;
SET timestamp=1789639203;
select * from contracts where note like '%x%';
# Time: 2026-09-17T10:00:04.000000Z
# User@Host: wp[wp] @  [10.0.0.6]  Id:    13
# Query_time: 3.000000  Lock_time: 0.000000 Rows_sent: 9  Rows_examined: 90000
use blog_wordpress;
SET timestamp=1789639204;
select * from wp_posts where post_content like '%y%';`
	a := NewAggregator()
	p := NewMySQLSlowParser(src, a)
	for _, line := range strings.Split(log, "\n") {
		p.Feed(line)
	}
	d := drainAll(a)
	if len(d.DB) != 1 || d.DB[0].SlowN != 1 || d.DB[0].SlowSum != 1.5 {
		t.Fatalf("only the staging database must be counted: %+v", d.DB)
	}
	for _, q := range d.DB[0].Queries {
		if q.Database != "shop_testing" {
			t.Fatalf("query from another database kept: %+v", q)
		}
	}

	rows := map[string]digestRow{
		"shop_testing|a":   {Schema: "shop_testing", Text: "SELECT * FROM `contracts`", Count: 10, Seconds: 2},
		"blog_wordpress|b": {Schema: "blog_wordpress", Text: "SELECT * FROM `wp_posts`", Count: 10, Seconds: 9},
		"|c":               {Schema: "", Text: "SELECT 1", Count: 10, Seconds: 1},
	}
	kept := (&dbSource{src: Source{Type: "mysql", Schemas: []string{"shop_testing"}}}).filterSchemas(rows)
	if len(kept) != 1 || kept["shop_testing|a"].Count != 10 {
		t.Fatalf("digests: %+v", kept)
	}
}
