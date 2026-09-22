package agent

import (
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/agent/detect"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// discoveries says what lives on this machine that Slowpoke is not reading: a database nobody
// looks at, a PHP-FPM pool without its slow log, a site whose requests are invisible, an
// application without the framework package. The panel turns each of them into the lines that
// close the gap, so what is missing is as visible as what works.
func discoveries(r detect.Report, sources []Source, server string) []protocol.SourceCheck {
	paths, types, addrs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, s := range sources {
		if s.Path != "" {
			paths[s.Path] = true
		}
		types[s.Type] = true
		if s.Addr != "" {
			addrs[s.Addr] = true
		}
	}
	found := func(kind, name string, reason string, facts map[string]string) protocol.SourceCheck {
		return protocol.SourceCheck{
			Source: "found:" + kind + ":" + name,
			Items:  []protocol.CheckItem{{Key: "configured", Status: StatusOff, Reason: reason}},
			Facts:  facts,
		}
	}

	var out []protocol.SourceCheck
	for _, s := range r.Sites {
		if paths[s.LogPath()] || detect.ServesSlowpoke(s, server) {
			continue
		}
		out = append(out, found("site", s.Name, "not_configured", map[string]string{
			"site": s.Name, "path": s.LogPath(), "access_log": s.AccessLog, "file": s.File,
			"inherited_log": yesNo(s.InheritedLog), "has_duration": yesNo(s.HasDuration),
		}))
	}
	for _, p := range r.Pools {
		if paths[p.SlowLogPath()] {
			continue
		}
		out = append(out, found("pool", p.Name, "not_configured", map[string]string{
			"pool": p.Name, "version": p.Version, "path": p.SlowLogPath(), "file": p.File,
			"slow_log_on": yesNo(p.SlowLogOn),
		}))
	}
	for _, d := range r.Databases {
		// A database is covered when its own source is configured, or when its slow log is read.
		if types[d.Engine] || (d.SlowLog != "" && paths[d.SlowLog]) {
			continue
		}
		out = append(out, found("database", d.Engine, "not_configured", map[string]string{
			"engine": d.Engine, "process": d.Process, "slow_log": d.SlowLog,
			"address": defaultAddress(d.Engine),
		}))
	}
	for _, c := range r.Caches {
		if types["redis"] || addrs["127.0.0.1:"+strconv.Itoa(c.Port)] {
			continue
		}
		out = append(out, found("cache", strconv.Itoa(c.Port), "not_configured", map[string]string{
			"engine": c.Engine, "port": strconv.Itoa(c.Port), "password": yesNo(c.Password),
		}))
	}
	for _, a := range r.Apps {
		if a.HasSlowpoke {
			continue
		}
		out = append(out, found("app", a.Dir, "no_package", map[string]string{
			"framework": a.Framework, "version": a.Version, "dir": a.Dir, "site": a.Site,
			"language": strings.ToLower(a.Language), "otel": yesNo(a.HasOpenTelemetry),
		}))
	}
	return out
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func defaultAddress(engine string) string {
	if engine == "postgres" {
		return "127.0.0.1:5432"
	}
	return "127.0.0.1:3306"
}

// scanEvery is how often the agent looks at the machine again. Config files change when somebody
// edits them, which is rare: reading them once an hour is enough and costs nothing in between.
const scanEvery = time.Hour

// machineChecks reports what is on the machine but not configured, re-reading it at most once an
// hour and answering from the last scan in between.
func (r *Runner) machineChecks(now time.Time) []protocol.SourceCheck {
	if r.scan == nil {
		r.scan = detect.Scan
	}
	if r.scanRoot == "" {
		r.scanRoot = "/"
	}
	if r.scannedAt.IsZero() || now.Sub(r.scannedAt) >= scanEvery {
		r.scanned = discoveries(r.scan(r.scanRoot), r.cfg.Sources, r.cfg.Server)
		r.scannedAt = now
	}
	return r.scanned
}
