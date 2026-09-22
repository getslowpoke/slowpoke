package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

func TestRunnerRoundTripFromLogFileToServer(t *testing.T) {
	var mu sync.Mutex
	var received []protocol.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, _ := gzip.NewReader(r.Body)
		var p protocol.Payload
		_ = json.NewDecoder(zr).Decode(&p)
		mu.Lock()
		received = append(received, p)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	logFile := filepath.Join(dir, "access.log")
	appendTo(t, logFile, "")

	cfg := &Config{
		Server: srv.URL, Token: "spk_x", Host: "web-1", StateDir: dir, Interval: time.Second,
		Sources: []Source{{Type: "nginx", Name: "app", Path: logFile, SlowSeconds: 1}},
	}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 16, 10, 0, 30, 0, time.UTC)
	r.Round(context.Background(), now) // positions at end of file

	appendTo(t, logFile, `1.2.3.4 - - [16/Sep/2026:10:00:10 +0000] "GET /orders/1 HTTP/1.1" 200 1 "-" "x" rt=0.300`+"\n")
	appendTo(t, logFile, `1.2.3.4 - - [16/Sep/2026:10:00:20 +0000] "GET /orders/2 HTTP/1.1" 200 1 "-" "x" rt=3.000`+"\n")

	r.Round(context.Background(), now.Add(10*time.Second)) // minute still open: nothing to send
	mu.Lock()
	for _, p := range received {
		if len(p.HTTP) > 0 {
			t.Fatalf("open minute must not be sent: %+v", p.HTTP)
		}
	}
	mu.Unlock()

	r.Round(context.Background(), now.Add(40*time.Second))

	mu.Lock()
	defer mu.Unlock()
	var http []protocol.HTTPBucket
	for _, p := range received {
		if p.Agent.Host != "web-1" || p.Agent.Kind != "agent" || p.BatchID == "" {
			t.Fatalf("agent info %+v", p)
		}
		http = append(http, p.HTTP...)
	}
	if len(http) != 1 || http[0].N != 2 || http[0].N2 != 1 || http[0].Source != "nginx:app" {
		t.Fatalf("http %+v", http)
	}
}

func TestRunnerRejectsUnknownSourceType(t *testing.T) {
	_, err := NewRunner(&Config{Server: "http://x", Token: "t", StateDir: t.TempDir(), Sources: []Source{{Type: "oracle"}}})
	if err == nil {
		t.Fatal("expected error")
	}
}

// A log that does not exist yet is the normal case on a fresh install: the nginx line is added
// later, the PHP-FPM slow log after a reload. Saying so once is help; saying it every round is
// noise in the journal of a production machine (three machines, one line every 30 s).
func TestMissingSourceIsReportedOnceAndPickedUpWhenItAppears(t *testing.T) {
	var logged bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(old)

	dir := t.TempDir()
	missing := filepath.Join(dir, "later.log")
	r, err := NewRunner(&Config{Server: "http://127.0.0.1:1", Token: "t", Host: "web-1", StateDir: dir, Interval: time.Second,
		Sources: []Source{{Type: "nginx", Name: "app", Path: missing, SlowSeconds: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 23, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		r.Round(context.Background(), now.Add(time.Duration(i)*time.Minute))
	}
	if n := strings.Count(logged.String(), "waiting for source"); n != 1 {
		t.Fatalf("missing log reported %d times, want once:\n%s", n, logged.String())
	}

	// The file appears (someone added the access_log line and reloaded nginx): the agent says so
	// and reads it, without anyone editing agent.yaml.
	appendTo(t, missing, `1.2.3.4 - - [17/Sep/2026:23:04:10 +0000] "GET /orders/1 HTTP/1.1" 200 1 "-" "x" rt=0.300`+"\n")
	r.Round(context.Background(), now.Add(5*time.Minute))
	if !strings.Contains(logged.String(), "reading source") {
		t.Fatalf("no line when the file appears:\n%s", logged.String())
	}
	r.Round(context.Background(), now.Add(6*time.Minute))
	if n := strings.Count(logged.String(), "reading source"); n != 1 {
		t.Fatalf("appearance reported %d times, want once", n)
	}
}
