package fingerprint

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRedactSharedFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../spec/redact_fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, SQL, Redacted string }
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := Redact(c.SQL)
		if got != c.Redacted {
			t.Errorf("%s:\n got  %q\n want %q", c.Name, got, c.Redacted)
		}
		// A redacted sample must keep the same identity.
		if Of(got) != Of(c.SQL) {
			t.Errorf("%s: fingerprint changed after redaction: %q vs %q", c.Name, Of(got), Of(c.SQL))
		}
	}
}

func FuzzFingerprintAndRedact(f *testing.F) {
	for _, seed := range []string{"select 1", "select * from t where a = 'x''y' and b in (1,2)", "/* x */ update t set a = $1", "limit 1, 2", "'unterminated", "\x00\xff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, sql string) {
		fp := Of(sql)
		if len(fp) > maxLen*2 {
			t.Fatalf("fingerprint too long: %d", len(fp))
		}
		red := Redact(sql)
		if len(red) > len(sql)+len(sql)/2+8 {
			t.Fatalf("redaction grew the statement: %d -> %d", len(sql), len(red))
		}
		if Redact(red) != red {
			t.Fatalf("redaction must be idempotent: %q -> %q", red, Redact(red))
		}
		_ = Hash(fp)
	})
}
