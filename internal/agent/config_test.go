package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An unset ${VAR} used to expand to nothing, so a DSN written by detect became a login with an
// empty password and the agent knocked on the production database every 30 s. What is not set
// stays written as it is, and the source is skipped with one clear line.
func TestUnsetVariablesAreLeftAsTheyAre(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	body := "server: http://127.0.0.1:8090\ntoken: ${SLOWPOKE_TOKEN}\nsources:\n  - type: mysql\n    dsn: \"slowpoke:${SLOWPOKE_DB_PASSWORD}@tcp(127.0.0.1:3306)/\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLOWPOKE_TOKEN", "spa_real")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "spa_real" {
		t.Fatalf("a variable that is set must be expanded: %q", cfg.Token)
	}
	if !strings.Contains(cfg.Sources[0].DSN, "${SLOWPOKE_DB_PASSWORD}") {
		t.Fatalf("unset variable swallowed: %q", cfg.Sources[0].DSN)
	}
}
