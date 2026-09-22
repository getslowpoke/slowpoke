package ingest

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// A machine reports one check per configured source plus one per thing found on it and not read:
// a web server with a dozen sites, two PHP versions and a database fits well inside this.
const maxChecksPerHost = 128

// checks replaces what the machine's sources can do. Agents send checks every few minutes;
// payloads without them leave the last ones in place.
func (w writer) checks(ctx context.Context, hostID int64, checks []protocol.SourceCheck) error {
	if len(checks) == 0 {
		return nil
	}
	if len(checks) > maxChecksPerHost {
		checks = checks[:maxChecksPerHost]
	}
	if _, err := w.q.Exec(ctx, "DELETE FROM host_checks WHERE host_id = ?", hostID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range checks {
		source := truncate(strings.TrimSpace(c.Source), 191)
		if source == "" || seen[source] {
			continue
		}
		seen[source] = true
		items := make([]protocol.CheckItem, 0, len(c.Items))
		for i, it := range c.Items {
			if i >= 16 {
				break
			}
			items = append(items, protocol.CheckItem{Key: truncate(it.Key, 64), Status: truncate(it.Status, 16), Optional: it.Optional,
				Reason: truncate(it.Reason, 64), Detail: truncate(it.Detail, 300)})
		}
		facts := map[string]string{}
		for k, v := range c.Facts {
			if len(facts) >= 24 || secretKey(k) {
				continue
			}
			facts[truncate(k, 64)] = truncate(v, 300)
		}
		rawItems, _ := json.Marshal(items)
		rawFacts, _ := json.Marshal(facts)
		if _, err := w.q.Exec(ctx, "INSERT INTO host_checks (host_id, source, items, facts, checked_at) VALUES (?, ?, ?, ?, ?)",
			hostID, source, string(rawItems), string(rawFacts), w.now.Unix()); err != nil {
			return err
		}
	}
	return nil
}

// secretKey refuses facts that look like credentials, whatever an agent sends.
func secretKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"password", "passwd", "secret", "token", "dsn", "key"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}
