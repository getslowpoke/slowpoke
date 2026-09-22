package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

func items(list []protocol.CheckItem) map[string]protocol.CheckItem {
	out := map[string]protocol.CheckItem{}
	for _, it := range list {
		out[it.Key] = it
	}
	return out
}

// A real staging database: MySQL 5.7 shared with production, an agent user that may
// read statistics but no application data, slow log off. Statistics work; plans are an
// optional extra the UI explains how to enable.
func TestMySQLChecksStatisticsOnly(t *testing.T) {
	got := items(mysqlCheckItems(mysqlFacts{
		Connected: true, Version: "5.7.42-0ubuntu0.18.04.1", PerfSchema: true, DigestsReadable: true,
		DigestsEnabled: true, ExplainEnabled: true, Schemas: []string{"shop_testing"},
	}))
	if got["connection"].Status != StatusOK || got["statistics"].Status != StatusOK {
		t.Fatalf("statistics must be active: %+v", got)
	}
	if s := got["slow_log"]; s.Status != StatusOff || !s.Optional {
		t.Fatalf("slow log is an optional extra: %+v", s)
	}
	plans := got["plans"]
	if plans.Status != StatusOff || !plans.Optional || plans.Reason != "no_select" {
		t.Fatalf("plans need SELECT on shop_testing: %+v", plans)
	}
}

func TestMySQLChecksPlansNeedTheSlowLogOn57(t *testing.T) {
	facts := mysqlFacts{
		Connected: true, Version: "5.7.42", PerfSchema: true, DigestsReadable: true, DigestsEnabled: true,
		ExplainEnabled: true, Schemas: []string{"shop"}, SelectOn: map[string]bool{"shop": true},
	}
	if p := items(mysqlCheckItems(facts))["plans"]; p.Status != StatusOff || p.Reason != "needs_slow_log" {
		t.Fatalf("5.7 keeps no statement values: %+v", p)
	}
	facts.SlowLogOn, facts.SlowLogRead = true, true
	got := items(mysqlCheckItems(facts))
	if got["slow_log"].Status != StatusOK || got["plans"].Status != StatusOK {
		t.Fatalf("with the slow log read, plans work: %+v", got)
	}

	// MySQL 8 keeps a sample with values: no slow log needed.
	facts = mysqlFacts{Connected: true, Version: "8.0.39", PerfSchema: true, DigestsReadable: true, DigestsEnabled: true,
		ExplainEnabled: true, HasSamples: true, GlobalSelect: true}
	if p := items(mysqlCheckItems(facts))["plans"]; p.Status != StatusOK {
		t.Fatalf("MySQL 8 with global SELECT: %+v", p)
	}
}

func TestMySQLChecksProblems(t *testing.T) {
	if got := mysqlCheckItems(mysqlFacts{ConnectError: "Access denied for user 'slowpoke'@'127.0.0.1'"}); len(got) != 1 || got[0].Status != StatusError {
		t.Fatalf("a failed connection is the only thing to fix: %+v", got)
	}
	got := items(mysqlCheckItems(mysqlFacts{Connected: true, Version: "10.11.8-MariaDB", DigestsEnabled: true, ExplainEnabled: true}))
	if s := got["statistics"]; s.Status != StatusOff || s.Reason != "performance_schema_off" {
		t.Fatalf("MariaDB ships performance_schema off: %+v", s)
	}
	got = items(mysqlCheckItems(mysqlFacts{Connected: true, Version: "8.0.39", PerfSchema: true, DigestsEnabled: true, DigestsError: "SELECT command denied"}))
	if s := got["statistics"]; s.Status != StatusError || s.Reason != "no_performance_schema_grant" {
		t.Fatalf("missing grant: %+v", s)
	}
	got = items(mysqlCheckItems(mysqlFacts{Connected: true, Version: "8.0.39", PerfSchema: true, DigestsReadable: true, DigestsEnabled: true, SlowLogOn: true}))
	if s := got["slow_log"]; s.Status != StatusOff || s.Reason != "not_read" {
		t.Fatalf("slow log on but no mysql-slowlog source: %+v", s)
	}
	got = items(mysqlCheckItems(mysqlFacts{Connected: true, Version: "8.0.39", PerfSchema: true, DigestsReadable: true, DigestsEnabled: true}))
	if s := got["plans"]; s.Status != StatusOff || s.Reason != "disabled" {
		t.Fatalf("explain: false in agent.yaml: %+v", s)
	}
}

func TestPostgresChecks(t *testing.T) {
	got := items(postgresCheckItems(postgresFacts{Connected: true, VersionNum: 170002, StatementsLoaded: true, StatementsReadable: true,
		ReadAllStats: true, CanSelect: true, ExplainEnabled: true, DigestsEnabled: true}))
	for _, key := range []string{"connection", "statistics", "plans"} {
		if got[key].Status != StatusOK {
			t.Fatalf("%s: %+v", key, got)
		}
	}
	got = items(postgresCheckItems(postgresFacts{Connected: true, VersionNum: 150004, DigestsEnabled: true, ExplainEnabled: true, CanSelect: true}))
	if s := got["statistics"]; s.Status != StatusOff || s.Reason != "pg_stat_statements_missing" {
		t.Fatalf("statistics: %+v", s)
	}
	if s := got["plans"]; s.Status != StatusOff || s.Reason != "needs_postgres_16" {
		t.Fatalf("pg_stat_statements keeps no values before 16: %+v", s)
	}
}

func TestFileChecks(t *testing.T) {
	if s := fileCheckItems("nginx", fileFacts{ReadError: "permission denied"}); len(s) != 1 || s[0].Status != StatusError || s[0].Reason != "unreadable" {
		t.Fatalf("%+v", s)
	}
	got := items(fileCheckItems("nginx", fileFacts{Readable: true, Lines: 40, WithDuration: 0}))
	if s := got["duration"]; s.Status != StatusError || s.Reason != "no_duration" {
		t.Fatalf("nginx without $request_time is useless: %+v", got)
	}
	got = items(fileCheckItems("nginx", fileFacts{Readable: true}))
	if s := got["duration"]; s.Status != StatusWaiting {
		t.Fatalf("an empty log waits for traffic: %+v", got)
	}
	got = items(fileCheckItems("php-fpm", fileFacts{Readable: true}))
	if got["readable"].Status != StatusOK || len(got) != 1 {
		t.Fatalf("an empty slow log is good news: %+v", got)
	}
}

func TestWebLogFactsCountLinesWithDuration(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := dir + "/" + name
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	combined := `1.2.3.4 - - [17/Sep/2026:17:29:49 +0200] "GET / HTTP/1.1" 200 34033 "-" "curl/8.5.0"` + "\n"
	timed := `1.2.3.4 - - [17/Sep/2026:17:29:49 +0200] "GET / HTTP/1.1" 200 34033 "-" "curl/8.5.0" rt=0.054` + "\n"

	if f := webLogFacts(write("plain.log", strings.Repeat(combined, 5)), false); !f.Readable || f.Lines != 5 || f.WithDuration != 0 {
		t.Fatalf("the default nginx format: %+v", f)
	}
	if f := webLogFacts(write("timed.log", strings.Repeat(timed, 3)), false); f.Lines != 3 || f.WithDuration != 3 {
		t.Fatalf("with rt=: %+v", f)
	}
	if f := webLogFacts(dir+"/missing.log", false); f.Readable || f.ReadError == "" {
		t.Fatalf("missing file: %+v", f)
	}
}

// Against the throwaway PostgreSQL of ./scripts/test: a superuser without pg_stat_statements.
func TestPostgresFactsOnARealServer(t *testing.T) {
	url := os.Getenv("SLOWPOKE_TEST_POSTGRES")
	if url == "" {
		t.Skip("SLOWPOKE_TEST_POSTGRES not set")
	}
	d, err := openDBSource(Source{Type: "postgres", Name: "pg", DSN: url}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.db.Close()
	f, facts := d.postgresFacts(context.Background())
	if !f.Connected || f.VersionNum < 160000 || facts["user"] != "postgres" || facts["dsn_user"] != "postgres" {
		t.Fatalf("facts %+v %+v", f, facts)
	}
	got := items(postgresCheckItems(f))
	if got["statistics"].Reason != "pg_stat_statements_missing" || got["plans"].Status != StatusOK {
		t.Fatalf("items %+v", got)
	}
}

// In the lab the agent started before the databases: the first check said "connection
// refused" and would have stayed on screen for 10 minutes. Problems are checked again soon.
func TestChecksWithProblemsAreRepeatedSoon(t *testing.T) {
	fine := []protocol.SourceCheck{{Source: "nginx:app", Items: []protocol.CheckItem{{Key: "readable", Status: StatusOK}, {Key: "plans", Status: StatusOff, Optional: true}}}}
	if got := nextCheckIn(fine); got != checkEvery {
		t.Fatalf("all fine: %v", got)
	}
	broken := append(fine, protocol.SourceCheck{Source: "mysql:main", Items: []protocol.CheckItem{{Key: "connection", Status: StatusError}}})
	if got := nextCheckIn(broken); got != time.Minute {
		t.Fatalf("a problem: %v", got)
	}
}

// Reading which process holds a socket needs more than reading files: without it the map still
// works, but the arrows start from the machine instead of the service that opened them.
func TestHostCheckReportsProcessAccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/1/fd", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/stat", []byte("cpu 1 2 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[42]", dir+"/1/fd/3"); err != nil {
		t.Fatal(err)
	}
	if got := items(hostCheckItems(dir)); got["processes"].Status != StatusOK {
		t.Fatalf("readable /proc: %+v", got)
	}

	// A /proc where the file descriptors of other processes cannot be read.
	closed := t.TempDir()
	// Created readable and closed afterwards: MkdirAll with no permissions at all only works
	// for root, which is how this passed in Docker and failed on a normal machine.
	if err := os.MkdirAll(closed+"/1/fd", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(closed+"/1/fd", 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(closed+"/stat", []byte("cpu 1 2 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := items(hostCheckItems(closed))
	if got["readable"].Status != StatusOK {
		t.Fatalf("/proc itself is readable: %+v", got)
	}
	if got["processes"].Status != StatusOff || got["processes"].Reason != "needs_capabilities" || !got["processes"].Optional {
		t.Fatalf("processes: %+v", got["processes"])
	}
}

// A database source left with its placeholder password is not opened, but it must still be in
// the panel: otherwise the machine simply shows no database and nobody knows a line is missing.
func TestDatabaseWaitingForItsPasswordIsStillReported(t *testing.T) {
	r, err := NewRunner(&Config{Server: "http://127.0.0.1:1", Token: "t", Host: "db-1", StateDir: t.TempDir(),
		Sources: []Source{{Type: "mysql", Name: "main", DSN: "slowpoke:${SLOWPOKE_DB_PASSWORD}@tcp(127.0.0.1:3306)/"}}})
	if err != nil {
		t.Fatal(err)
	}
	var check *protocol.SourceCheck
	for i, c := range r.checks(context.Background(), time.Now()) {
		if c.Source == "mysql:main" {
			check = &r.checks(context.Background(), time.Now())[i]
		}
	}
	if check == nil {
		t.Fatal("the source disappeared from the checks")
	}
	if len(check.Items) == 0 || check.Items[0].Key != "connection" || check.Items[0].Status != StatusOff || check.Items[0].Reason != "needs_password" {
		t.Fatalf("items %+v", check.Items)
	}
	if check.Facts["address"] != "127.0.0.1:3306" {
		t.Fatalf("the guide needs the address to write the dsn line: %+v", check.Facts)
	}
}

// A log that has not been created yet is the normal first step of an install, not a fault: it is
// waiting, in amber, with the line to add. A log that exists but cannot be read is a real error
// (wrong group, wrong mode) and stays red.
func TestALogNotCreatedYetIsWaitingNotBroken(t *testing.T) {
	missing := fileCheckItems("nginx", fileFacts{ReadError: "open /var/log/nginx/site.slowpoke.log: no such file or directory", Missing: true})
	if missing[0].Status != StatusWaiting || missing[0].Reason != "not_created_yet" {
		t.Fatalf("missing log: %+v", missing)
	}
	denied := fileCheckItems("nginx", fileFacts{ReadError: "open /var/log/nginx/access.log: permission denied"})
	if denied[0].Status != StatusError || denied[0].Reason != "unreadable" {
		t.Fatalf("unreadable log: %+v", denied)
	}
}

// Whoever installs an agent on a production machine wants to know what it costs there, measured,
// not promised: the agent reports its own CPU share and memory, and the panel shows them.
func TestTheAgentReportsItsOwnCost(t *testing.T) {
	dir := t.TempDir()
	// utime=100 stime=200 at the first reading: 3 seconds of CPU already spent starting up.
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte("1 (slowpoke-agent) S 1 1 1 0 -1 0 0 0 0 0 100 200 0 0 20 0 5 0 0 0 0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte("Name:\tslowpoke-agent\nVmRSS:\t17408 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The first check happens seconds after the start, when the startup cost is the whole
	// window: a number like "200% of one core" would be true and useless, so there is none.
	m := &costMeter{}
	start := time.Date(2026, 9, 18, 4, 0, 0, 0, time.UTC)
	if f := m.read(dir, start.Add(2*time.Second)); len(f) != 0 {
		t.Fatalf("a number out of a two second window: %v", f)
	}
	// 3 more seconds of CPU (300 ticks) used since that first reading.
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte("1 (slowpoke-agent) S 1 1 1 0 -1 0 0 0 0 0 400 200 0 0 20 0 5 0 0 0 0"), 0o644); err != nil {
		t.Fatal(err)
	}
	facts := m.read(dir, start.Add(20*time.Minute))
	if facts["agent_memory_mb"] != "17" {
		t.Errorf("memory: %v", facts)
	}
	// 3 s of CPU in the 20 minutes since the first reading is 0.25% of one core.
	if facts["agent_cpu_percent"] != "0.25" {
		t.Errorf("cpu: %v", facts)
	}
	// Nothing to read: no numbers invented.
	if f := (&costMeter{}).read(t.TempDir(), start); len(f) != 0 {
		t.Errorf("made up facts: %v", f)
	}
}
