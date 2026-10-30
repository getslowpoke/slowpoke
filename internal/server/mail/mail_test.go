package mail

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP speaks just enough SMTP to receive one message, without STARTTLS or AUTH.
func fakeSMTP(t *testing.T) (addr string, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(s string) { conn.Write([]byte(s + "\r\n")) }
		say("220 fake ESMTP")
		var envelope, data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 fake")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				envelope.WriteString(strings.TrimSpace(line) + "\n")
				say("250 ok")
			case cmd == "DATA":
				say("354 go on")
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					data.WriteString(l)
				}
				say("250 queued")
			case cmd == "QUIT":
				say("221 bye")
				got <- envelope.String() + "\n" + data.String()
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSMTPDeliversAMessageWithTextAndHTML(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	s := SMTP{Host: host, Port: port, From: "Slowpoke <slowpoke@example.com>", Timeout: 5 * time.Second}
	if !s.Enabled() {
		t.Fatal("a configured host must be enabled")
	}
	err := s.Send(context.Background(), Message{
		To: "bob@example.com", Subject: "Ada invited you to Slowpoke",
		Text: "Open this link: https://slowpoke.example.com/invite/abc", HTML: "<p>Open <a href=\"https://slowpoke.example.com/invite/abc\">this link</a></p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	select {
	case raw = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing arrived")
	}
	for _, want := range []string{
		"MAIL FROM:<slowpoke@example.com>", "RCPT TO:<bob@example.com>",
		"From: Slowpoke <slowpoke@example.com>", "To: bob@example.com",
		"Subject: Ada invited you to Slowpoke", "multipart/alternative",
		"text/plain; charset=UTF-8", "text/html; charset=UTF-8", "/invite/abc",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("missing %q in\n%s", want, raw)
		}
	}
}

func TestHeaderInjectionIsRefused(t *testing.T) {
	s := SMTP{Host: "127.0.0.1", Port: "1", From: "slowpoke@example.com"}
	err := s.Send(context.Background(), Message{To: "bob@example.com\r\nBcc: eve@example.com", Subject: "hi", Text: "x"})
	if err == nil {
		t.Fatal("a newline in an address must be refused")
	}
}

func TestNotConfiguredIsNotEnabled(t *testing.T) {
	if (SMTP{}).Enabled() {
		t.Fatal("no host, no mail")
	}
	if (SMTP{Host: "smtp.example.com"}).Enabled() {
		t.Fatal("no sender address, no mail")
	}
}

func TestNonASCIISubjectIsEncoded(t *testing.T) {
	if h := encodeHeader("Ada ti ha invitato: è pronto"); !strings.HasPrefix(h, "=?UTF-8?") {
		t.Fatalf("%q", h)
	}
	if h := encodeHeader("plain"); h != "plain" {
		t.Fatalf("%q", h)
	}
}

func TestExplainSaysWhatToFix(t *testing.T) {
	cases := map[string]string{
		"mail: login: 535 5.7.8 Authentication failed":               "auth",
		"mail: 550 5.7.1 Sender denied":                              "sender",
		"mail: dial tcp: lookup smtp.nowhere.example: no such host":  "connect",
		"mail: dial tcp 10.0.0.1:465: connect: connection refused":   "connect",
		"mail: login: unencrypted connection":                        "tls",
		"mail: tls: first record does not look like a TLS handshake": "tls",
		"mail: 452 mailbox full":                                     "other",
	}
	for msg, want := range cases {
		if got := Explain(errors.New(msg)); got != want {
			t.Errorf("%q: got %s, want %s", msg, got, want)
		}
	}
}
