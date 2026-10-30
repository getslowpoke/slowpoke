// Package mail sends the few emails Slowpoke writes (team invitations) through the SMTP server
// the installation already has. Without one configured nothing is sent and the panel shows the
// link to pass along by hand.
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	netmail "net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Message struct {
	To, Subject, Text, HTML string
}

// Sender is what the API needs: tests swap in a fake.
type Sender interface {
	Enabled() bool
	Send(ctx context.Context, m Message) error
}

// SMTP is configured from SLOWPOKE_SMTP_*. Port 465 means TLS from the first byte; any other
// port upgrades with STARTTLS when the server offers it, and credentials are only ever sent
// over TLS (or to localhost, as net/smtp enforces).
type SMTP struct {
	Host, Port         string
	Username, Password string
	From               string
	Timeout            time.Duration
}

func (s SMTP) Enabled() bool { return s.Host != "" && s.From != "" }

func (s SMTP) port() string {
	if s.Port == "" {
		return "587"
	}
	return s.Port
}

func noNewline(v string) error {
	if strings.ContainsAny(v, "\r\n") {
		return errors.New("mail: a header cannot contain a newline")
	}
	return nil
}

func (s SMTP) Send(ctx context.Context, m Message) error {
	if !s.Enabled() {
		return errors.New("mail: no SMTP server configured")
	}
	for _, v := range []string{m.To, m.Subject, s.From} {
		if err := noNewline(v); err != nil {
			return err
		}
	}
	from, err := netmail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("mail: sender address: %w", err)
	}
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("mail: recipient: %w", err)
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := net.JoinHostPort(s.Host, s.port())
	dialer := &net.Dialer{}
	var conn net.Conn
	if s.port() == "465" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: s.Host}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail: %w", err)
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.Host}); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("mail: login: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	if _, err := w.Write(build(s.From, m)); err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	return c.Quit()
}

func encodeHeader(v string) string {
	for _, r := range v {
		if r > 127 {
			return mime.QEncoding.Encode("UTF-8", v)
		}
	}
	return v
}

// build writes a multipart/alternative message: mail clients that cannot show HTML still get
// the link as plain text.
func build(from string, m Message) []byte {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	boundary := "slowpoke-" + hex.EncodeToString(b)
	domain := "slowpoke"
	if a, err := netmail.ParseAddress(from); err == nil {
		if _, d, ok := strings.Cut(a.Address, "@"); ok {
			domain = d
		}
	}
	var sb strings.Builder
	h := func(k, v string) { sb.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", m.To)
	h("Subject", encodeHeader(m.Subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(b)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	sb.WriteString("\r\n")
	part := func(kind, body string) {
		sb.WriteString("--" + boundary + "\r\n")
		sb.WriteString("Content-Type: " + kind + "; charset=UTF-8\r\n")
		sb.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		sb.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
		sb.WriteString("\r\n")
	}
	part("text/plain", m.Text)
	if m.HTML != "" {
		part("text/html", m.HTML)
	}
	sb.WriteString("--" + boundary + "--\r\n")
	return []byte(sb.String())
}
