package agent

import "testing"

// detect writes the MySQL source with a ${SLOWPOKE_DB_PASSWORD} placeholder: until someone puts
// a real password there the agent must not try to log in. Failed logins every 30 s count towards
// max_connect_errors on the monitored database, which is exactly the harm the agent must never do.
func TestSourceWithAnUnsetPasswordIsNotOpened(t *testing.T) {
	for _, dsn := range []string{
		"slowpoke:${SLOWPOKE_DB_PASSWORD}@tcp(127.0.0.1:3306)/",
		"postgres://slowpoke:${SLOWPOKE_DB_PASSWORD}@127.0.0.1:5432/postgres",
	} {
		if !hasUnsetPlaceholder(dsn) {
			t.Errorf("%s: placeholder not seen", dsn)
		}
	}
	for _, dsn := range []string{
		"slowpoke:a-real-password@tcp(127.0.0.1:3306)/",
		"postgres://slowpoke:p%24ssword@127.0.0.1:5432/postgres",
	} {
		if hasUnsetPlaceholder(dsn) {
			t.Errorf("%s: a real password was taken for a placeholder", dsn)
		}
	}

	dir := t.TempDir()
	r, err := NewRunner(&Config{Server: "http://127.0.0.1:1", Token: "t", Host: "db-1", StateDir: dir,
		Sources: []Source{{Type: "mysql", DSN: "slowpoke:${SLOWPOKE_DB_PASSWORD}@tcp(127.0.0.1:3306)/"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.dbs) != 0 {
		t.Fatalf("the source was opened anyway: %d databases", len(r.dbs))
	}
}
