package detect

// Making nginx log how long each request took is the one change Slowpoke asks for, and the one
// place where a small team without a staging server has something to lose. So it is a command
// that writes the change, keeps the previous bytes outside the directories nginx reads, asks
// nginx whether it likes the result, and puts everything back at the first sign it does not.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Change is one file to write, and why.
type Change struct {
	Path   string `json:"path"`
	Before string `json:"-"` // empty when the file did not exist
	After  string `json:"-"`
	Why    string `json:"why"`
	New    bool   `json:"new"`
}

// Runner is what talks to the machine around the files: kept out of here so the whole thing can
// be tested without an nginx.
type Runner struct {
	Test   func() error // nginx -t
	Reload func() error // systemctl reload nginx
	Verify func() error // anything else worth checking after the reload
}

const logFormat = `# Written by Slowpoke. The same line nginx already writes, plus how long the
# request took. Existing logs are untouched: this only adds a format that can be used.
log_format slowpoke '$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent '
                    '"$http_referer" "$http_user_agent" rt=$request_time';
`

// PlanDuration returns what would be written to make every site say how long it takes. It reads
// files and nothing else: this is what --dry-run prints.
func PlanDuration(root string, r Report) ([]Change, error) {
	if !r.Nginx {
		return nil, nil
	}
	var out []Change
	s := scanner{root: root}

	needFormat := true
	for _, path := range append(s.glob("/etc/nginx/nginx.conf"), append(s.glob("/etc/nginx/conf.d/*.conf"), s.glob("/etc/nginx/sites-enabled/*")...)...) {
		if strings.Contains(s.read(strings.TrimPrefix(path, root)), "log_format slowpoke") {
			needFormat = false
			break
		}
	}

	var todo []Site
	for _, site := range r.Sites {
		if site.HasDuration || site.File == "" || site.AccessLog == "off" {
			continue
		}
		todo = append(todo, site)
	}
	if len(todo) == 0 {
		return nil, nil
	}
	if needFormat {
		if !r.ConfD {
			return nil, fmt.Errorf("nginx here does not include /etc/nginx/conf.d/*.conf: add the slowpoke log_format to the http block by hand")
		}
		out = append(out, Change{
			Path: filepath.Join(root, "etc/nginx/conf.d/slowpoke.conf"),
			After: logFormat, New: true,
			Why: "defines the log format once, for every site on this machine",
		})
	}

	edited := map[string]string{} // file -> latest content, so two sites in one file both land
	for _, site := range todo {
		path := filepath.Join(root, strings.TrimPrefix(site.File, "/"))
		body, ok := edited[path]
		if !ok {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			body = string(raw)
		}
		keep := ""
		if site.InheritedLog && site.AccessLog != "" {
			// An access_log inside a server block replaces the inherited one: without this line
			// the site would stop writing where it writes today.
			keep = site.AccessLog
		}
		next, n := addAccessLog(body, site.Name, keep, site.LogPath())
		if n == 0 {
			continue
		}
		edited[path] = next
	}
	for path, body := range edited {
		raw, _ := os.ReadFile(path)
		out = append(out, Change{Path: path, Before: string(raw), After: body,
			Why: "writes the duration of every request of this site, next to the log it already writes"})
	}
	return out, nil
}

var serverBlock = regexp.MustCompile(`\bserver\s*\{`)

// addAccessLog puts the log lines at the top of every server block that serves this site. It
// leaves alone the blocks that answer nothing (a redirect), and the ones already done.
func addAccessLog(text, name, keep, slowpokeLog string) (string, int) {
	type edit struct{ at int }
	var edits []edit
	for _, m := range serverBlock.FindAllStringIndex(text, -1) {
		start, depth, i := m[1], 0, m[1]-1
		for ; i < len(text); i++ {
			if text[i] == '{' {
				depth++
			} else if text[i] == '}' {
				if depth--; depth == 0 {
					break
				}
			}
		}
		body := text[start:i]
		if !strings.Contains(body, name) {
			continue
		}
		if strings.Contains(body, "slowpoke") {
			continue // already done
		}
		// A block with nowhere to send a request has no requests to measure.
		if !strings.Contains(body, "proxy_pass") && !strings.Contains(body, "root ") && !strings.Contains(body, "fastcgi_pass") {
			continue
		}
		edits = append(edits, edit{at: start})
	}
	if len(edits) == 0 {
		return text, 0
	}
	lines := "\n    # Slowpoke: how long each request takes."
	if keep != "" {
		lines += "\n    # The first line keeps the log this site already writes: a log line here\n    # replaces the one inherited from the http block.\n    access_log " + keep + ";"
	}
	lines += "\n    access_log " + slowpokeLog + " slowpoke;\n"
	// Backwards, so the earlier offsets stay valid.
	for i := len(edits) - 1; i >= 0; i-- {
		text = text[:edits[i].at] + lines + text[edits[i].at:]
	}
	return text, len(edits)
}

type backupEntry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	Saved   string `json:"saved,omitempty"`
}

// Apply writes the changes, keeping what was there in backupDir — never next to the originals,
// because nginx includes whole directories and a copy of a server block is a second server
// block. If nginx refuses the result, or the check after the reload fails, everything goes back.
func Apply(changes []Change, backupDir string, run Runner) error {
	if len(changes) == 0 {
		return nil
	}
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return err
	}
	var saved []backupEntry
	for i, c := range changes {
		entry := backupEntry{Path: c.Path, Existed: !c.New}
		if entry.Existed {
			entry.Saved = fmt.Sprintf("%02d.bak", i)
			if err := os.WriteFile(filepath.Join(backupDir, entry.Saved), []byte(c.Before), 0o640); err != nil {
				return err
			}
		}
		saved = append(saved, entry)
	}
	manifest, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(backupDir, "manifest.json"), manifest, 0o640); err != nil {
		return err
	}

	undo := func() {
		for _, e := range saved {
			if !e.Existed {
				_ = os.Remove(e.Path)
				continue
			}
			if body, err := os.ReadFile(filepath.Join(backupDir, e.Saved)); err == nil {
				_ = os.WriteFile(e.Path, body, 0o644)
			}
		}
	}
	for _, c := range changes {
		if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
			undo()
			return err
		}
		if err := os.WriteFile(c.Path, []byte(c.After), 0o644); err != nil {
			undo()
			return err
		}
	}
	// In order, and stopping at the first one that complains.
	for _, step := range []struct {
		what string
		fn   func() error
	}{{"nginx -t", run.Test}, {"reload", run.Reload}, {"check after the reload", run.Verify}} {
		if step.fn == nil {
			continue
		}
		if err := step.fn(); err != nil {
			undo()
			if step.what != "nginx -t" && run.Reload != nil {
				_ = run.Reload() // put the running nginx back on the old configuration too
			}
			return fmt.Errorf("%s: %w (nothing was kept)", step.what, err)
		}
	}
	return nil
}

// Undo puts back what the last Apply changed.
func Undo(backupDir string, run Runner) error {
	raw, err := os.ReadFile(filepath.Join(backupDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("nothing to undo in %s: %w", backupDir, err)
	}
	var saved []backupEntry
	if err := json.Unmarshal(raw, &saved); err != nil {
		return err
	}
	for _, e := range saved {
		if !e.Existed {
			if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		body, err := os.ReadFile(filepath.Join(backupDir, e.Saved))
		if err != nil {
			return err
		}
		if err := os.WriteFile(e.Path, body, 0o644); err != nil {
			return err
		}
	}
	if run.Test != nil {
		if err := run.Test(); err != nil {
			return fmt.Errorf("nginx -t after putting everything back: %w", err)
		}
	}
	if run.Reload != nil {
		return run.Reload()
	}
	return nil
}
