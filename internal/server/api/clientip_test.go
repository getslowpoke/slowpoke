package api

import (
	"net/http/httptest"
	"testing"
)

// On the staging server where this was found, Slowpoke sits behind nginx and Cloudflare: every visitor
// reached it from 127.0.0.1, so ten wrong passwords from anyone locked the login for all.
func TestClientIPBehindTrustedProxies(t *testing.T) {
	proxies, err := ParseTrustedProxies("127.0.0.1, ::1, 10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, remote, xff string
		trusted           bool
		want              string
	}{
		{"no trusted proxies: headers are ignored", "127.0.0.1:5000", "1.2.3.4", false, "127.0.0.1"},
		{"behind the local proxy", "127.0.0.1:5000", "1.2.3.4", true, "1.2.3.4"},
		{"a forged entry on the left is skipped", "127.0.0.1:5000", "6.6.6.6, 1.2.3.4", true, "1.2.3.4"},
		{"a chain of trusted proxies", "127.0.0.1:5000", "1.2.3.4, 10.1.2.3", true, "1.2.3.4"},
		{"no header from the proxy", "127.0.0.1:5000", "", true, "127.0.0.1"},
		{"an untrusted peer cannot choose its address", "5.5.5.5:443", "1.2.3.4", true, "5.5.5.5"},
		{"IPv6 peer", "[::1]:443", "2001:db8::7", true, "2001:db8::7"},
		{"IPv6 peer without proxies", "[2001:db8::9]:443", "", false, "2001:db8::9"},
		{"garbage in the header", "127.0.0.1:5000", "not-an-ip", true, "127.0.0.1"},
	}
	for _, c := range cases {
		s := &Server{}
		if c.trusted {
			s.TrustedProxies = proxies
		}
		r := httptest.NewRequest("POST", "/api/login", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := s.clientIP(r); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}

	if _, err := ParseTrustedProxies("127.0.0.1, nonsense"); err == nil {
		t.Fatal("invalid proxy accepted")
	}
}
