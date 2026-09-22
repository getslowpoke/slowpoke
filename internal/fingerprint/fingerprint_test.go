package fingerprint

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSharedFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../spec/fingerprint_fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, SQL, Fingerprint string }
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := Of(c.SQL); got != c.Fingerprint {
			t.Errorf("%s:\n got  %q\n want %q", c.Name, got, c.Fingerprint)
		}
	}
}

func TestHashMatchesPHP(t *testing.T) {
	// substr(sha1('select 1'), 0, 16)
	if got := Hash("select 1"); got != "3232003928f9fe86" {
		t.Errorf("got %s", got)
	}
}
