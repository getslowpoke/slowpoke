package agent

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   string        `yaml:"server"`
	Token    string        `yaml:"token"`
	Host     string        `yaml:"host"`
	Interval time.Duration `yaml:"interval"`
	StateDir string        `yaml:"state_dir"`
	// Send real query samples to the server. Off by default: the server gets
	// redacted samples (values removed) and EXPLAIN runs here with the real one.
	ShareSamples bool `yaml:"share_samples"`
	// Allow a plain http:// server that is not on this machine (the token travels in clear).
	AllowInsecure bool `yaml:"allow_insecure"`
	// Replace this binary with the one the server hands out when the server is a different
	// version. Off by default: updating a binary on a production machine is your decision.
	AutoUpdate bool `yaml:"auto_update"`

	// Resource limits: the agent must never compete with the application.
	MaxProcs    int      `yaml:"max_procs"`       // default 1
	MemoryLimit int      `yaml:"memory_limit_mb"` // soft limit, default 64
	Sources     []Source `yaml:"sources"`
}

// Source is one thing to read. Only the fields of its type are used.
type Source struct {
	Type string `yaml:"type"` // nginx | php-fpm | mysql-slowlog | mysql | postgres | host | statsd | prometheus | redis | rabbitmq | otlp
	Name string `yaml:"name"`

	// File sources.
	Path      string `yaml:"path"`
	FromStart bool   `yaml:"from_start"`
	Timezone  string `yaml:"timezone"` // for logs without offset (php-fpm slowlog)

	// nginx: the host this virtual host answers for ("shop.example.com"). Only needed when the
	// name of the source is not the host itself: it is how the server recognises that the
	// application reporting the same host, from another machine, is reporting the same requests.
	Site string `yaml:"site"`

	// nginx
	Format      string  `yaml:"format"` // auto | json | rt | combined_time
	SlowSeconds float64 `yaml:"slow_seconds"`
	// Paths that are long by design (SSE streams, long polling): "/events/*" or exact paths.
	IgnorePaths []string `yaml:"ignore_paths"`

	// mysql-slowlog
	IgnoreUsers []string `yaml:"ignore_users"`

	// mysql, postgres, mysql-slowlog: only these databases (schemas). Empty = all. A server often
	// hosts databases of several applications or environments that belong to other projects.
	Schemas []string `yaml:"schemas"`

	// statsd: UDP address to listen on (default :8125). prometheus: URL to scrape.
	Listen string `yaml:"listen"`

	// redis: address (default 127.0.0.1:6379), password (use ${VAR}), slow log on by default.
	// rabbitmq: url of the management API (default http://127.0.0.1:15672), user and password.
	Addr     string `yaml:"addr"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	SlowLog  *bool  `yaml:"slowlog"`
	// otlp: directory where the application is deployed, stripped from code paths.
	CodeRoot string `yaml:"code_root"`
	URL      string `yaml:"url"`

	// mysql / postgres
	DSN              string  `yaml:"dsn"`
	Status           *bool   `yaml:"status"`
	Digests          *bool   `yaml:"digests"`
	Explain          *bool   `yaml:"explain"`
	SlowQuerySeconds float64 `yaml:"slow_query_seconds"`
}

func (s Source) enabled(flag *bool) bool { return flag == nil || *flag }

// schemaAllowed tells whether statements of this database are measured. With a list, statements
// without a known database are left out: they cannot be attributed to the listed ones.
func (s Source) schemaAllowed(name string) bool {
	if len(s.Schemas) == 0 {
		return true
	}
	for _, allowed := range s.Schemas {
		if strings.EqualFold(allowed, name) {
			return true
		}
	}
	return false
}

func checkServerURL(raw string, allowInsecure bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("server must be an http(s) URL, got %q", raw)
	}
	if u.Scheme == "https" || allowInsecure {
		return nil
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	private := host == "localhost" || (ip != nil && (ip.IsLoopback() || ip.IsPrivate())) ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") || (ip == nil && !strings.Contains(host, "."))
	if !private {
		return fmt.Errorf("server %q uses plain http: the token would travel in clear (use https or set allow_insecure: true)", raw)
	}
	return nil
}

// ConfigWarnings reports risky file permissions: the file holds the token and database passwords.
func ConfigWarnings(path string) []string {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if info.Mode().Perm()&0o004 != 0 {
		return []string{fmt.Sprintf("%s is readable by every user: run chmod 640 %s", path, path)}
	}
	return nil
}

// expandKnown replaces ${VAR} with the environment, and leaves alone what the environment does
// not have: an unset password must stay visible as a placeholder, not become an empty password
// the agent tries against the monitored database.
func expandKnown(s string) string {
	return os.Expand(s, func(name string) string {
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return "${" + name + "}"
	})
}

func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expandKnown(string(raw))), cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if cfg.Server == "" || cfg.Token == "" {
		return nil, fmt.Errorf("config %s: server and token are required", path)
	}
	if err := checkServerURL(cfg.Server, cfg.AllowInsecure); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if cfg.Host == "" {
		cfg.Host, _ = os.Hostname()
	}
	if cfg.Interval == 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.MaxProcs <= 0 {
		cfg.MaxProcs = 1
	}
	if cfg.MemoryLimit <= 0 {
		cfg.MemoryLimit = 64
	}
	if cfg.StateDir == "" {
		cfg.StateDir = "/var/lib/slowpoke"
	}
	for i := range cfg.Sources {
		s := &cfg.Sources[i]
		if s.Name == "" {
			s.Name = s.Type
		}
		if s.SlowSeconds == 0 {
			s.SlowSeconds = 1
		}
		if s.SlowQuerySeconds == 0 {
			s.SlowQuerySeconds = 1
		}
	}
	return cfg, nil
}
