package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/auth"
	"github.com/getslowpoke/slowpoke/internal/server/exclude"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// sourceProject is a real-looking installation to export: two days of the demo story, a user,
// a secret in the settings and a real value in a query sample, none of which may leave.
func sourceProject(t *testing.T) (*store.DB, project.Project, time.Time) {
	t.Helper()
	db := store.OpenTest(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 16, 4, 0, 0, 0, time.UTC)
	p, err := Seed(ctx, db, Options{Days: 2, Now: now, Name: "Acme Real"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (auth.Service{DB: db}).CreateUser(ctx, "Owner", "owner@acme.example", "a long password here"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Q().Exec(ctx, "UPDATE query_fingerprints SET sample = 'SELECT * FROM users WHERE email = ''mario@acme.example''' WHERE project_id = ?", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Q().Exec(ctx, "INSERT INTO deploys (project_id, version, commit_sha, author, message, url, deployed_at) VALUES (?, 'api@1a2b3c', ?, 'ada', 'Faster search', 'https://github.com/acme/secret-repo/commit/1', ?)",
		p.ID, strings.Repeat("ab", 20), now.Add(-20*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := exclude.Add(ctx, db, p.ID, exclude.Exclusion{Kind: exclude.KindEndpoint, Target: "GET /", Reason: "streaming", CreatedBy: "ada"}, now.Add(-30*time.Hour)); err != nil {
		t.Fatal(err)
	}
	return db, p, now
}

func TestExportProfileKeepsMeasuresAndNothingPersonal(t *testing.T) {
	db, p, now := sourceProject(t)
	prof, err := ExportProfile(context.Background(), db, p.Slug, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(prof)
	for _, secret := range []string{"owner@acme.example", "mario@acme.example", "secret-repo", p.Token, "token_hash", `"sample"`} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("the profile carries %q", secret)
		}
	}
	for _, table := range []string{"hosts", "http_minutes", "endpoint_hours", "query_fingerprints", "query_hours", "host_minutes", "host_connections", "deploys", "job_hours", "metric_minutes", "route_calls", "exclusions"} {
		if tb := prof.table(table); tb == nil || len(tb.Rows) == 0 {
			t.Errorf("table %s is empty in the profile", table)
		}
	}
	for _, table := range []string{"users", "sessions", "access_tokens", "app_settings", "invites", "missions", "projects"} {
		if prof.table(table) != nil {
			t.Errorf("table %s must not be exported", table)
		}
	}
	if prof.To != now.Unix() || prof.Timezone != "Europe/Rome" {
		t.Errorf("to %d timezone %q", prof.To, prof.Timezone)
	}
}

func TestImportProfileReplaysTheHistoryOnToday(t *testing.T) {
	src, p, cut := sourceProject(t)
	ctx := context.Background()
	prof, err := ExportProfile(ctx, src, p.Slug, cut)
	if err != nil {
		t.Fatal(err)
	}
	// Through JSON, as it travels from one server to the other.
	var buf bytes.Buffer
	if err := prof.Write(&buf); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadProfile(&buf)
	if err != nil {
		t.Fatal(err)
	}

	db := store.OpenTest(t)
	now := cut.Add(10*24*time.Hour + 20*time.Minute)
	q, err := ImportProfile(ctx, db, loaded, Options{Name: "Nestwise", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	count := func(d *store.DB, query string, args ...any) int {
		var n int
		if err := d.Q().Row(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, table := range []string{"http_minutes", "endpoint_hours", "query_hours", "job_hours", "route_hours", "php_stall_hours", "deploys"} {
		want := count(src, "SELECT COUNT(*) FROM "+table+" WHERE project_id = ?", p.ID)
		if got := count(db, "SELECT COUNT(*) FROM "+table+" WHERE project_id = ?", q.ID); got != want {
			t.Errorf("%s: %d rows, want %d", table, got, want)
		}
	}
	hostRows := "SELECT COUNT(*) FROM host_minutes WHERE host_id IN (SELECT id FROM hosts WHERE project_id = ?)"
	if got, want := count(db, hostRows, q.ID), count(src, hostRows, p.ID); got != want {
		t.Errorf("host_minutes: %d rows, want %d", got, want)
	}

	// Shifted by whole days: the last hour of the export is the last hour before now.
	var last int64
	_ = db.Q().Row(ctx, "SELECT MAX(bucket) FROM http_minutes WHERE project_id = ?", q.ID).Scan(&last)
	var srcLast int64
	_ = src.Q().Row(ctx, "SELECT MAX(bucket) FROM http_minutes WHERE project_id = ?", p.ID).Scan(&srcLast)
	if last-srcLast != 10*86400 {
		t.Errorf("shifted by %d s, want ten days", last-srcLast)
	}

	// The game played the history again: days scored, missions opened, deploys judged.
	if count(db, "SELECT COUNT(*) FROM score_days WHERE project_id = ?", q.ID) == 0 {
		t.Error("no score days")
	}
	if count(db, "SELECT COUNT(*) FROM missions WHERE project_id = ?", q.ID) == 0 {
		t.Error("no missions")
	}
	// The machines that were reporting at the end of the export are online now.
	if n := count(db, "SELECT COUNT(*) FROM hosts WHERE project_id = ? AND last_seen_at >= ?", q.ID, now.Add(-time.Minute).Unix()); n == 0 {
		t.Error("every machine looks offline")
	}
	// Query rows point at the new fingerprints, not at ids of the other database.
	if n := count(db, "SELECT COUNT(*) FROM query_hours qh LEFT JOIN query_fingerprints f ON f.id = qh.fingerprint_id AND f.project_id = qh.project_id WHERE qh.project_id = ? AND f.id IS NULL", q.ID); n != 0 {
		t.Errorf("%d query rows without their fingerprint", n)
	}

	// Importing again replaces the project.
	if _, err := ImportProfile(ctx, db, loaded, Options{Name: "Nestwise", Now: now}); err != nil {
		t.Fatal(err)
	}
	if list, _ := (project.Repo{DB: db}).List(ctx); len(list) != 1 {
		t.Fatalf("projects %d", len(list))
	}
}

func TestRenameReplacesNamesAndAddressesAndFindsLeaks(t *testing.T) {
	prof := Profile{Tables: []Table{
		{Name: "endpoint_hours", Columns: []string{"source", "endpoint", "n"}, Rows: [][]any{
			{"nginx:app.acme.it", "GET /ricerca-contratto/{id}", int64(3)},
		}},
		{Name: "host_connections", Columns: []string{"remote_ip", "process"}, Rows: [][]any{
			{"203.0.113.10", "php-fpm: pool acme"},
			{"10.1.2.3", "nginx"},
			{"203.0.113.10", "redis"},
		}},
		{Name: "deploys", Columns: []string{"commit_sha", "author"}, Rows: [][]any{
			{"a3f9c2e1d4b5a6978c0d1e2f3a4b5c6d7e8f9a0b", "Mario Rossi"},
		}},
	}}
	dict := Dictionary{
		Replace: []Pair{{"app.acme.it", "app.nestwise.io"}, {"ricerca-contratto", "lease-search"}, {"acme", "nestwise"}, {"Mario Rossi", "ada"}},
		Deny:    []string{"acme", "ricerca", "Mario"},
	}
	prof.Rename(dict)
	if leaks := prof.Leaks(dict.Deny, dict.DenyWords...); len(leaks) != 0 {
		t.Fatalf("leaks %v", leaks)
	}
	eh := prof.table("endpoint_hours").Rows[0]
	if eh[0] != "nginx:app.nestwise.io" || eh[1] != "GET /lease-search/{id}" || eh[2] != int64(3) {
		t.Errorf("endpoint row %v", eh)
	}
	conns := prof.table("host_connections").Rows
	if conns[0][0] == "203.0.113.10" || conns[0][0] != conns[2][0] {
		t.Errorf("the same public address must become the same other address: %v", conns)
	}
	if !strings.HasPrefix(conns[1][0].(string), "10.") {
		t.Errorf("a private address stays private: %v", conns[1][0])
	}
	if sha := prof.table("deploys").Rows[0][0].(string); len(sha) != 40 || strings.HasPrefix(sha, "a3f9c2e1") {
		t.Errorf("commit %q", sha)
	}

	// What the dictionary forgot is reported, whatever the case.
	prof.Tables[0].Rows[0][1] = "GET /Ricerca"
	if leaks := prof.Leaks(dict.Deny, dict.DenyWords...); len(leaks) != 1 {
		t.Errorf("leaks %v", leaks)
	}
}

func TestCutAtIsTheLastTimeTheHourStruck(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	for now, want := range map[string]string{
		"2026-09-29T17:10:00+02:00": "2026-09-29T04:00:00+02:00",
		"2026-09-29T03:59:00+02:00": "2026-09-28T04:00:00+02:00",
		"2026-09-29T04:00:00+02:00": "2026-09-29T04:00:00+02:00",
	} {
		n, _ := time.Parse(time.RFC3339, now)
		if got := CutAt(n, rome, 4).Format(time.RFC3339); got != want {
			t.Errorf("CutAt(%s) = %s, want %s", now, got, want)
		}
	}
}

func TestStringsListsEveryTextOnce(t *testing.T) {
	prof := Profile{Tables: []Table{{Name: "hosts", Columns: []string{"name", "n"}, Rows: [][]any{{"web-1", int64(1)}, {"web-1", int64(2)}, {"", nil}}}}}
	if got := prof.Strings(); len(got) != 1 || got[0] != "hosts.name\tweb-1" {
		t.Errorf("strings %q", got)
	}
}

func TestPruneDropsTheLongTailWithEverythingHangingOnIt(t *testing.T) {
	prof := Profile{Tables: []Table{
		{Name: "endpoint_hours", Columns: []string{"endpoint", "hour", "n", "sum"}, Rows: [][]any{
			{"GET /busy", int64(0), int64(80), 1.0}, {"GET /busy", int64(3600), int64(80), 1.0},
			{"GET /slow-once", int64(0), int64(1), 90.0},
			{"GET /.env", int64(0), int64(3), 0.1},
		}},
		{Name: "query_fingerprints", Columns: []string{"id", "fingerprint"}, Rows: [][]any{{int64(1), "select a"}, {int64(2), "select b"}}},
		{Name: "query_hours", Columns: []string{"fingerprint_id", "hour", "n", "sum"}, Rows: [][]any{{int64(1), int64(0), int64(500), 2.0}, {int64(2), int64(0), int64(2), 0.1}}},
		{Name: "route_queries", Columns: []string{"route", "fingerprint_id"}, Rows: [][]any{{"GET /busy", int64(1)}, {"GET /busy", int64(2)}, {"GET /rare", int64(1)}}},
		{Name: "route_hours", Columns: []string{"route", "hour", "n", "sum"}, Rows: [][]any{{"GET /busy", int64(0), int64(300), 5.0}, {"GET /rare", int64(0), int64(1), 0.2}}},
		{Name: "route_calls", Columns: []string{"route", "host"}, Rows: [][]any{{"GET /rare", "api.example.com"}}},
	}}
	prof.Prune(100, 60)
	rows := func(name string) int { return len(prof.table(name).Rows) }
	for name, want := range map[string]int{"endpoint_hours": 3, "query_fingerprints": 1, "query_hours": 1, "route_queries": 1, "route_hours": 1, "route_calls": 0} {
		if got := rows(name); got != want {
			t.Errorf("%s: %d rows, want %d", name, got, want)
		}
	}
}

func TestRenameTranslatesWholeWordsAndScrubsPeople(t *testing.T) {
	prof := Profile{Tables: []Table{{Name: "t", Columns: []string{"v"}, Rows: [][]any{
		{"GET /api/soggetto/GMBMNL51H67D488H/immobili"},
		{"App\\Jobs\\ExportImmobileDataJob"},
		{"select * from `immobili` where `codice_fiscale` = ?"},
		{"upgrade the ADE sync"},
		{"Invia i task ai fornitori"},
		{"mail to mario.rossi@example.it, IBAN IT60X0542811101000000123456"},
		{`["203.0.113.10","2a00:6d40:72:3bc::1"]`},
	}}}}
	d := Dictionary{
		Values: map[string]string{"Invia i task ai fornitori": "Send the tasks to the suppliers"},
		Words:  map[string]string{"soggetto": "subject", "immobili": "properties", "immobile": "property", "codice": "code", "fiscale": "tax", "ade": "registry"},
		Deny:   []string{"GMBMNL", "mario", "IT60", "2a00"}, DenyWords: []string{"ade", "immobili"},
	}
	prof.Rename(d)
	got := []string{}
	for _, r := range prof.Tables[0].Rows {
		got = append(got, r[0].(string))
	}
	want := []string{
		"GET /api/subject/{id}/properties",
		"App\\Jobs\\ExportPropertyDataJob",
		"select * from `properties` where `code_tax` = ?",
		"upgrade the REGISTRY sync",
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("row %d: %q, want %q", i, got[i], w)
		}
	}
	if got[4] != "Send the tasks to the suppliers" {
		t.Errorf("whole value %q", got[4])
	}
	if leaks := prof.Leaks(d.Deny, d.DenyWords...); len(leaks) != 0 {
		t.Errorf("leaks %v", leaks)
	}
	// A word inside another word is not that word.
	if leaks := (Profile{Tables: []Table{{Name: "t", Columns: []string{"v"}, Rows: [][]any{{"made upgrades"}}}}}).Leaks(nil, "ade"); len(leaks) != 0 {
		t.Errorf("leaks %v", leaks)
	}
}

func TestRenameMergesRowsThatBecameTheSame(t *testing.T) {
	prof := Profile{Tables: []Table{{Name: "endpoint_hours", Columns: []string{"source", "endpoint", "hour", "n", "sum", "max"}, Rows: [][]any{
		{"nginx:a", "GET /subject/GMBMNL51H67D488H", int64(3600), int64(2), 1.5, 1.0},
		{"nginx:a", "GET /subject/RSSMRA80A01H501U", int64(3600), int64(3), 0.5, 0.3},
		{"nginx:a", "GET /subject/RSSMRA80A01H501U", int64(7200), int64(1), 0.1, 0.1},
	}}}}
	prof.Rename(Dictionary{})
	rows := prof.table("endpoint_hours").Rows
	if len(rows) != 2 {
		t.Fatalf("rows %v", rows)
	}
	if r := rows[0]; r[1] != "GET /subject/{id}" || r[3] != int64(5) || r[4] != 2.0 || r[5] != 1.0 {
		t.Errorf("merged row %v", r)
	}
}

func TestUntilEndsTheHistoryEarlier(t *testing.T) {
	prof := Profile{From: 0, To: 7200, Tables: []Table{
		{Name: "http_minutes", Columns: []string{"source", "bucket"}, Rows: [][]any{{"a", int64(60)}, {"a", int64(3600)}, {"a", int64(3660)}}},
		{Name: "hosts", Columns: []string{"id", "name"}, Rows: [][]any{{int64(1), "web-1"}}},
	}}
	prof.Until(3600)
	if prof.To != 3600 || len(prof.table("http_minutes").Rows) != 1 || len(prof.table("hosts").Rows) != 1 {
		t.Errorf("to %d, rows %v", prof.To, prof.table("http_minutes").Rows)
	}
}
