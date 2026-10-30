package main

import (
	"errors"
	"reflect"
	"testing"
)

// Running the binary with no arguments used to start a second server on :8080 with a database
// in the current directory. On a machine that already runs Slowpoke that is a surprise nobody
// asked for: with nothing to do, the binary says what it can do.
func TestBareInvocationDoesNotStartAServer(t *testing.T) {
	cases := []struct {
		args    []string
		command string
		rest    []string
	}{
		{nil, "", nil},
		{[]string{"serve"}, "serve", []string{}},
		{[]string{"migrate"}, "migrate", []string{}},
		{[]string{"create-token", "-project", "shop"}, "create-token", []string{"-project", "shop"}},
		// Flags without a verb still mean serve: that is how the unit files are written.
		{[]string{"-addr", ":9000"}, "serve", []string{"-addr", ":9000"}},
		// 2026-10-01 on the demo machine: "-version" to check a new binary started a server on
		// :8080 with a database in /root. Asking what it is never starts it.
		{[]string{"-version"}, "version", []string{}},
		{[]string{"--version"}, "version", []string{}},
		{[]string{"-h"}, "", nil},
		{[]string{"--help"}, "", nil},
	}
	for _, c := range cases {
		command, rest := commandFrom(c.args)
		if command != c.command || (len(rest) > 0 || len(c.rest) > 0) && !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("commandFrom(%q) = %q %q, want %q %q", c.args, command, rest, c.command, c.rest)
		}
	}
}

// The new password can arrive three ways, and the order matters: a password on the command line
// is visible in `ps` and stays in the shell history, so it is the explicit choice, never the
// default, and typing it at the prompt is what happens when nobody said anything.
func TestNewPasswordPrefersTheQuietestSource(t *testing.T) {
	typed := func(answers ...string) func(string) (string, error) {
		i := 0
		return func(string) (string, error) {
			if i >= len(answers) {
				return "", errors.New("asked once too often")
			}
			i++
			return answers[i-1], nil
		}
	}

	if p, err := newPassword("from the flag", "from the environment", typed("typed", "typed")); p != "from the flag" || err != nil {
		t.Errorf("flag wins: %q %v", p, err)
	}
	if p, err := newPassword("", "from the environment", typed("typed", "typed")); p != "from the environment" || err != nil {
		t.Errorf("environment second: %q %v", p, err)
	}
	if p, err := newPassword("", "", typed("typed twice over", "typed twice over")); p != "typed twice over" || err != nil {
		t.Errorf("prompt last: %q %v", p, err)
	}

	// A typo in a password nobody can see would lock the person out for good.
	if _, err := newPassword("", "", typed("typed twice over", "typed twice ovre")); err == nil {
		t.Error("two different passwords were accepted")
	}
	// The length rule is the same one CreateUser applies, wherever the password came from.
	if _, err := newPassword("short", "", typed("a long enough password", "a long enough password")); err == nil {
		t.Error("a short password was accepted from the flag")
	}
	if _, err := newPassword("", "short", nil); err == nil {
		t.Error("a short password was accepted from the environment")
	}
	// No flag, no environment and no terminal to ask: say how to do it instead of hanging.
	if _, err := newPassword("", "", nil); err == nil {
		t.Error("with nothing to read from, the command must explain itself")
	}
}
