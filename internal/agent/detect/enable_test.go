package detect

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeNginx(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(path, body string) {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("etc/nginx/nginx.conf", `http {
	access_log /var/log/nginx/access.log;
	include /etc/nginx/conf.d/*.conf;
	include /etc/nginx/sites-enabled/*;
}`)
	// Inherits the log of the http block: the change has to repeat it, or this site stops
	// writing where it wrote before.
	write("etc/nginx/sites-enabled/shop", `server {
	server_name shop.example.com;
	location / {
		proxy_pass http://127.0.0.1:3000;
	}
}`)
	// Has its own log: that one is left exactly as it is.
	write("etc/nginx/sites-enabled/api", `server {
	server_name api.example.com;
	access_log /var/log/nginx/api.log;
	location / {
		proxy_pass http://127.0.0.1:4000;
	}
}`)
	// Serves nothing: a certbot redirect has no requests worth measuring.
	write("etc/nginx/sites-enabled/old", `server {
	server_name old.example.com;
	return 301 https://shop.example.com$request_uri;
}`)
	return root
}

// The change nginx needs to say how long each request took, written out before anything is
// touched: this is what --dry-run shows.
func TestPlanningTheDurationChange(t *testing.T) {
	root := fakeNginx(t)
	changes, err := PlanDuration(root, Scan(root))
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Change{}
	for _, c := range changes {
		byPath[strings.TrimPrefix(c.Path, root)] = c
	}
	if len(changes) != 3 {
		for p := range byPath {
			t.Log(p)
		}
		t.Fatalf("expected the format plus two sites, got %d", len(changes))
	}

	format := byPath["/etc/nginx/conf.d/slowpoke.conf"]
	if !strings.Contains(format.After, "log_format slowpoke") || !strings.Contains(format.After, "rt=$request_time") || format.Before != "" {
		t.Fatalf("the log format: %+v", format)
	}

	shop := byPath["/etc/nginx/sites-enabled/shop"]
	if strings.Count(shop.After, "access_log") != 2 {
		t.Fatalf("a site that inherits its log keeps it and gains ours:\n%s", shop.After)
	}
	if !strings.Contains(shop.After, "access_log /var/log/nginx/access.log;") ||
		!strings.Contains(shop.After, "access_log /var/log/nginx/shop.example.com.slowpoke.log slowpoke;") {
		t.Fatalf("shop:\n%s", shop.After)
	}

	api := byPath["/etc/nginx/sites-enabled/api"]
	if strings.Count(api.After, "access_log") != 2 || !strings.Contains(api.After, "access_log /var/log/nginx/api.log;") {
		t.Fatalf("a site with its own log only gains ours:\n%s", api.After)
	}
	if _, ok := byPath["/etc/nginx/sites-enabled/old"]; ok {
		t.Fatal("a redirect serves nothing to measure: it must be left alone")
	}

	// Applied, then planned again: nothing left to do.
	if err := Apply(changes, filepath.Join(root, "backup"), Runner{}); err != nil {
		t.Fatal(err)
	}
	again, err := PlanDuration(root, Scan(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("running it twice changes something again: %+v", again)
	}
}

// The part that matters on a production machine with no staging: if nginx does not like the
// result, every file goes back to what it was, and the backup is never left where nginx reads
// configuration from.
func TestTheChangeIsUndoneWhenNginxRefusesIt(t *testing.T) {
	root := fakeNginx(t)
	before := map[string]string{}
	for _, name := range []string{"shop", "api"} {
		p := filepath.Join(root, "etc/nginx/sites-enabled", name)
		b, _ := os.ReadFile(p)
		before[p] = string(b)
	}
	changes, err := PlanDuration(root, Scan(root))
	if err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(root, "var/lib/slowpoke/nginx-backup")
	err = Apply(changes, backup, Runner{Test: func() error { return errors.New("duplicate listen options") }})
	if err == nil {
		t.Fatal("a configuration nginx refuses must not be kept")
	}
	for p, body := range before {
		now, _ := os.ReadFile(p)
		if string(now) != body {
			t.Fatalf("%s was not put back:\n%s", p, now)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "etc/nginx/conf.d/slowpoke.conf")); !os.IsNotExist(err) {
		t.Fatal("the file it created must be removed too")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "etc/nginx/sites-enabled"))
	for _, e := range entries {
		if strings.Contains(e.Name(), "backup") || strings.HasSuffix(e.Name(), ".bak") {
			t.Fatalf("nginx includes everything in sites-enabled: %s must not be there", e.Name())
		}
	}
}

// Undo puts back what the last run changed, for somebody who wants it gone.
func TestUndoPutsEverythingBack(t *testing.T) {
	root := fakeNginx(t)
	original, _ := os.ReadFile(filepath.Join(root, "etc/nginx/sites-enabled/shop"))
	changes, err := PlanDuration(root, Scan(root))
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(root, "var/lib/slowpoke/nginx-backup")
	if err := Apply(changes, backup, Runner{}); err != nil {
		t.Fatal(err)
	}
	if err := Undo(backup, Runner{}); err != nil {
		t.Fatal(err)
	}
	now, _ := os.ReadFile(filepath.Join(root, "etc/nginx/sites-enabled/shop"))
	if string(now) != string(original) {
		t.Fatalf("shop after undo:\n%s", now)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/nginx/conf.d/slowpoke.conf")); !os.IsNotExist(err) {
		t.Fatal("the created file is gone too")
	}
}
