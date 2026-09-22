package doctor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

func mysqlExplain(t *testing.T, plan string, tables map[string]protocol.TableDef) *Explain {
	t.Helper()
	return &Explain{Engine: "mysql", Plan: json.RawMessage(plan), Tables: tables}
}

func codes(ds []Diagnosis) map[string]Diagnosis {
	out := map[string]Diagnosis{}
	for _, d := range ds {
		out[d.Code] = d
	}
	return out
}

var orders = protocol.TableDef{
	Rows: 250000,
	Columns: []protocol.Column{
		{Name: "id", Type: "bigint unsigned"}, {Name: "user_id", Type: "bigint unsigned"}, {Name: "status", Type: "varchar(20)"},
		{Name: "reference", Type: "varchar(40)"}, {Name: "note", Type: "text"}, {Name: "created_at", Type: "timestamp"},
	},
	Indexes: []protocol.Index{{Name: "PRIMARY", Columns: []string{"id"}, Unique: true}},
}

func TestFullScanSuggestsCompositeIndexEqualityFirst(t *testing.T) {
	in := Input{
		Fingerprint: "select * from orders where created_at > ? and user_id = ? and status = ? order by created_at desc",
		Sample:      "select * from orders where created_at > '2026-01-01' and user_id = 42 and status = 'paid' order by created_at desc",
		Explain:     mysqlExplain(t, `[{"table":"orders","type":"ALL","rows":"250000","Extra":"Using where; Using filesort"}]`, map[string]protocol.TableDef{"orders": orders}),
	}
	d := codes(Analyze(in))
	scan, ok := d["full_scan"]
	if !ok || scan.Severity != Critical {
		t.Fatalf("full scan not detected: %+v", d)
	}
	if !strings.Contains(scan.Fix, "ALTER TABLE `orders` ADD INDEX `idx_orders_user_id_status_created_at` (`user_id`, `status`, `created_at`)") {
		t.Fatalf("fix %q", scan.Fix)
	}
	if _, ok := d["filesort"]; !ok {
		t.Fatalf("filesort not detected")
	}
}

func TestExistingLeadingIndexIsNotSuggestedAgain(t *testing.T) {
	withIndex := orders
	withIndex.Indexes = append(withIndex.Indexes, protocol.Index{Name: "orders_user_id", Columns: []string{"user_id"}})
	in := Input{
		Fingerprint: "select * from orders where user_id = ?",
		Sample:      "select * from orders where user_id = 42",
		Explain:     mysqlExplain(t, `[{"table":"orders","type":"ALL","rows":250000,"Extra":"Using where"}]`, map[string]protocol.TableDef{"orders": withIndex}),
	}
	scan := codes(Analyze(in))["full_scan"]
	if strings.Contains(scan.Fix, "ADD INDEX") {
		t.Fatalf("index exists, must not be suggested: %q", scan.Fix)
	}
}

func TestStringColumnComparedWithNumberBreaksTheIndex(t *testing.T) {
	logs := protocol.TableDef{Rows: 9000000, Columns: []protocol.Column{{Name: "entity_id", Type: "varchar(191)"}}, Indexes: []protocol.Index{{Name: "logs_entity_id", Columns: []string{"entity_id"}}}}
	in := Input{
		Fingerprint: "select * from logs where entity_id = ?",
		Sample:      "select * from `logs` where `entity_id` = 4848337",
		Explain:     mysqlExplain(t, `[{"table":"logs","type":"ALL","rows":9000000}]`, map[string]protocol.TableDef{"logs": logs}),
	}
	d := codes(Analyze(in))
	conv, ok := d["implicit_conversion"]
	if !ok || conv.Severity != Critical || !strings.Contains(conv.Detail, "entity_id") {
		t.Fatalf("implicit conversion not detected: %+v", d)
	}
}

func TestFunctionOnColumnAndLeadingWildcard(t *testing.T) {
	in := Input{
		Fingerprint: "select * from docs where json_unquote(json_extract(params, ?)) = ? or title like ?",
		Sample:      "select * from docs where json_unquote(json_extract(params, '$.foglio')) = '12' or title like '%villa%'",
	}
	d := codes(Analyze(in))
	for _, c := range []string{"function_on_column", "leading_wildcard", "or_conditions"} {
		if _, ok := d[c]; !ok {
			t.Errorf("%s not detected: %+v", c, d)
		}
	}
}

func TestSelectStarOnWideTableCountOnBigTableAndDeepOffset(t *testing.T) {
	wide := protocol.TableDef{Rows: 2000000, Columns: []protocol.Column{{Name: "id", Type: "int"}, {Name: "payload", Type: "json"}, {Name: "notes", Type: "longtext"}}}
	in := Input{
		Fingerprint: "select * from user_services where user_id = ? limit ? offset ?",
		Sample:      "select * from user_services where user_id = 5 limit 20 offset 50000",
		Explain:     mysqlExplain(t, `[{"table":"user_services","type":"ref","key":"user_id","rows":300}]`, map[string]protocol.TableDef{"user_services": wide}),
	}
	d := codes(Analyze(in))
	if _, ok := d["select_star_wide"]; !ok {
		t.Errorf("select * on wide table: %+v", d)
	}
	if _, ok := d["deep_offset"]; !ok {
		t.Errorf("deep offset: %+v", d)
	}
	if _, ok := d["full_scan"]; ok {
		t.Errorf("ref access is not a full scan")
	}

	count := Input{
		Fingerprint: "select count(*) as aggregate from credits where user_id = ? and is_used = ?",
		Sample:      "select count(*) as aggregate from credits where user_id = 1 and is_used = 0",
		Explain:     mysqlExplain(t, `[{"table":"credits","type":"ref","key":"idx","rows":"4000000"}]`, map[string]protocol.TableDef{"credits": {Rows: 12760000}}),
		Stats:       Stats{CallsPerDay: 20000, AvgSeconds: 0.8},
	}
	if _, ok := codes(Analyze(count))["count_big_table"]; !ok {
		t.Errorf("count on big table not detected")
	}
}

func TestNPlusOneFromTheFrameworkPackage(t *testing.T) {
	in := Input{Fingerprint: "select * from categories where id = ? limit ?", Origin: "app/Http/Controllers/ProductController.php:21", NPlusOne: 40}
	d := codes(Analyze(in))
	n, ok := d["n_plus_one"]
	if !ok || !strings.Contains(n.Detail, "ProductController.php:21") {
		t.Fatalf("%+v", d)
	}
}

func TestPostgresSeqScan(t *testing.T) {
	plan := `[{"Plan":{"Node Type":"Sort","Plans":[{"Node Type":"Seq Scan","Relation Name":"orders","Alias":"o","Plan Rows":180000,"Filter":"(user_id = 42)"}]}}]`
	in := Input{
		Fingerprint: "select * from orders o where o.user_id = ? order by o.created_at",
		Sample:      "select * from orders o where o.user_id = 42 order by o.created_at",
		Explain:     &Explain{Engine: "postgres", Plan: json.RawMessage(plan), Tables: map[string]protocol.TableDef{"orders": orders}},
	}
	scan, ok := codes(Analyze(in))["full_scan"]
	if !ok || !strings.Contains(scan.Fix, `CREATE INDEX CONCURRENTLY idx_orders_user_id_created_at ON "orders" ("user_id", "created_at")`) {
		t.Fatalf("%+v", scan)
	}
}

func TestHealthyQueryHasNoCriticalFindings(t *testing.T) {
	in := Input{
		Fingerprint: "select id, status from orders where id = ?",
		Sample:      "select id, status from orders where id = 5",
		Explain:     mysqlExplain(t, `[{"table":"orders","type":"const","key":"PRIMARY","rows":1}]`, map[string]protocol.TableDef{"orders": orders}),
	}
	for _, d := range Analyze(in) {
		if d.Severity == Critical || d.Severity == Warning {
			t.Fatalf("unexpected %+v", d)
		}
	}
}

func TestPromptContainsEverythingAnAIWouldNeed(t *testing.T) {
	in := Input{
		Fingerprint: "select * from orders where user_id = ?",
		Sample:      "select * from orders where user_id = 42",
		Origin:      "app/Models/Order.php:10",
		Stats:       Stats{CallsPerDay: 1000, AvgSeconds: 1.2, SecondsPerDay: 1200, RowsExaminedAvg: 250000},
		Explain:     mysqlExplain(t, `[{"table":"orders","type":"ALL","rows":250000}]`, map[string]protocol.TableDef{"orders": orders}),
	}
	p := Prompt(in, Analyze(in))
	for _, want := range []string{"select * from orders where user_id = 42", "orders", "varchar(20)", "PRIMARY", "Order.php:10", "1200", "type"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt misses %q", want)
		}
	}
}

// The case found in production: Laravel's ->sum() called on a relation inside a loop. The prompt
// is read by the team's coding assistant in the repository, so it says where to start (the line
// and the routes where the loop runs), what the fix usually is, and how to prove it.
func TestPromptForAnNPlusOneTellsTheAssistantWhereToLook(t *testing.T) {
	in := Input{
		Fingerprint: "select sum(amount) as aggregate from external_payments where type = ? and user_service_id = ? and user_service_type = ? and external_payments.deleted_at is null",
		Origin:      "app/UserService.php:810",
		NPlusOne:    149120,
		Stats:       Stats{CallsPerDay: 237900, AvgSeconds: 0.0094, SecondsPerDay: 2242},
		Routes: []RouteUse{
			{Route: "GET /api/v3/user/{id}/payments", PerRequest: 16.1, Requests: 5200},
			{Route: "GET /api/v3/practices/{practice}", PerRequest: 6.1, Requests: 9100},
		},
		Link: "https://slowpoke.example.com/projects/shop/queries/95197a9f94ce0e4d",
	}
	d := Analyze(in)
	if len(d) == 0 || d[0].Code != "n_plus_one" || !strings.Contains(d[0].Fix, "withSum") {
		t.Fatalf("diagnosis %+v", d)
	}
	p := Prompt(in, d)
	for _, want := range []string{
		"app/UserService.php", "line 810", // where to start
		"GET /api/v3/user/{id}/payments", "16.1", // where the loop runs
		"withSum", // the usual fix for this shape
		"test",    // how to prove it
		"https://slowpoke.example.com/projects/shop/queries/95197a9f94ce0e4d",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt misses %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "database performance expert") {
		t.Error("an N+1 is fixed in the code: the prompt must not send the assistant to the database")
	}
}
