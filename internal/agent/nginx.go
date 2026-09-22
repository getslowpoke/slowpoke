package agent

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LineParser receives the lines of a file source, one at a time.
type LineParser interface {
	Feed(line string)
}

// Flusher is implemented by multi-line parsers: called after each read round,
// when the last entry of the file is surely complete.
type Flusher interface {
	Flush()
}

type nginxParser struct {
	src Source
	agg *Aggregator
}

func NewNginxParser(src Source, agg *Aggregator) LineParser {
	return &nginxParser{src: src, agg: agg}
}

var (
	// 1.2.3.4 - - [16/Sep/2026:12:00:03 +0200] "GET /x HTTP/1.1" 200 512 ...
	combinedRe = regexp.MustCompile(`^\S+ \S+ \S+ \[([^\]]+)\] "(\S+) (\S+)[^"]*" (\d{3}) `)
	rtRe       = regexp.MustCompile(`\b(?:rt|request_time)=([\d.]+)`)
	lastNumRe  = regexp.MustCompile(`"\s+([\d]+\.[\d]+)\s*$`)
)

func (p *nginxParser) Feed(line string) {
	var (
		when    time.Time
		method  string
		uri     string
		status  int
		seconds float64
		ok      bool
	)
	if strings.HasPrefix(strings.TrimSpace(line), "{") {
		when, method, uri, status, seconds, ok = parseNginxJSON(line)
	} else {
		when, method, uri, status, seconds, ok = parseNginxText(line, p.src.Format == "apache" || p.src.Type == "apache")
	}
	if !ok {
		return
	}
	// WebSocket upgrades live for hours: they are connections, not slow requests.
	if status == 101 {
		p.agg.AddMetric(when, p.kind()+"."+p.src.Name+".websocket_connections", "counter", 1)
		return
	}
	if p.ignored(uri) {
		return
	}
	source := p.kind() + ":" + p.src.Name
	p.agg.SourceSite(source, p.site())
	p.agg.AddRequest(source, when, method, uri, status, seconds, p.src.SlowSeconds)
}

// site is the host this virtual host answers for, and only when it really is one: "site:" as
// configured, or the name of the source when detect took it from a server_name. A generic name
// ("nginx", "web") is not a host, and claiming it would shadow an application that answers for
// a container called the same.
func (p *nginxParser) site() string {
	if p.src.Site != "" {
		return p.src.Site
	}
	if strings.Contains(p.src.Name, ".") {
		return p.src.Name
	}
	return ""
}

// kind is the web server type used as source prefix (nginx, apache, caddy).
func (p *nginxParser) kind() string {
	switch p.src.Type {
	case "apache", "caddy":
		return p.src.Type
	}
	return "nginx"
}

func (p *nginxParser) ignored(uri string) bool {
	path := uri
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	for _, pattern := range p.src.IgnorePaths {
		if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		} else if path == pattern {
			return true
		}
	}
	return false
}

var lastIntRe = regexp.MustCompile(`"\s+(\d+)\s*$`)

// parseNginxText reads the combined format with the duration as rt=, trailing
// seconds (nginx $request_time) or, for Apache, trailing microseconds (%D).
func parseNginxText(line string, apache bool) (time.Time, string, string, int, float64, bool) {
	m := combinedRe.FindStringSubmatch(line)
	if m == nil {
		return time.Time{}, "", "", 0, 0, false
	}
	when, err := time.Parse("02/Jan/2006:15:04:05 -0700", m[1])
	if err != nil {
		return time.Time{}, "", "", 0, 0, false
	}
	var rt string
	if r := rtRe.FindStringSubmatch(line); r != nil {
		rt = r[1]
	} else if r := lastIntRe.FindStringSubmatch(line); r != nil && apache {
		micro, err := strconv.ParseFloat(r[1], 64)
		if err != nil {
			return time.Time{}, "", "", 0, 0, false
		}
		status, _ := strconv.Atoi(m[4])
		return when, m[2], m[3], status, micro / 1e6, true
	} else if r := lastNumRe.FindStringSubmatch(line); r != nil {
		rt = r[1]
	} else {
		return time.Time{}, "", "", 0, 0, false
	}
	seconds, err := strconv.ParseFloat(rt, 64)
	if err != nil {
		return time.Time{}, "", "", 0, 0, false
	}
	status, _ := strconv.Atoi(m[4])
	return when, m[2], m[3], status, seconds, true
}

func parseNginxJSON(line string) (time.Time, string, string, int, float64, bool) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return time.Time{}, "", "", 0, 0, false
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			switch v := raw[k].(type) {
			case string:
				return v
			case float64:
				return strconv.FormatFloat(v, 'f', -1, 64)
			}
		}
		return ""
	}

	// Caddy nests the request: {"request":{"method":..,"uri":..}}.
	if req, ok := raw["request"].(map[string]any); ok {
		for k, v := range req {
			if _, exists := raw[k]; !exists {
				raw[k] = v
			}
		}
	}
	var when time.Time
	var err error
	if ts, ok := raw["ts"].(float64); ok {
		when = time.Unix(int64(ts), int64((ts-float64(int64(ts)))*1e9))
	} else {
		timeStr := pick("time_iso8601", "time", "timestamp", "time_local")
		if when, err = time.Parse(time.RFC3339, timeStr); err != nil {
			if when, err = time.Parse("02/Jan/2006:15:04:05 -0700", timeStr); err != nil {
				return time.Time{}, "", "", 0, 0, false
			}
		}
	}
	seconds, err := strconv.ParseFloat(pick("request_time", "rt", "duration"), 64)
	if err != nil {
		return time.Time{}, "", "", 0, 0, false
	}
	status, _ := strconv.Atoi(pick("status"))
	method := pick("request_method", "method")
	uri := pick("request_uri", "uri", "path")
	if method == "" || uri == "" {
		if parts := strings.Fields(pick("request")); len(parts) >= 2 {
			method, uri = parts[0], parts[1]
		}
	}
	if method == "" {
		return time.Time{}, "", "", 0, 0, false
	}
	return when, method, uri, status, seconds, true
}
