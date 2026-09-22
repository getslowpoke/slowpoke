package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The agent replaces itself with the binary its own server hands out: nothing from the
// internet, the checksum is checked, and a binary that does not run is never put in place.
func TestTheAgentReplacesItselfWithTheServersBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "slowpoke-agent")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho 0.1.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBinary := []byte("#!/bin/sh\necho 0.2.0\n")
	sum := fmt.Sprintf("%x  slowpoke-agent\n", sha256.Sum256(newBinary))
	fetch := func(_ context.Context, path string) ([]byte, error) {
		switch path {
		case "/agent/linux-amd64":
			return newBinary, nil
		case "/agent/linux-amd64.sha256":
			return []byte(sum), nil
		}
		return nil, fmt.Errorf("unexpected path %q", path)
	}

	if err := replaceBinary(context.Background(), fetch, "/agent/linux-amd64", "0.2.0", exe); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != string(newBinary) {
		t.Fatalf("the binary was not replaced: %q", got)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("the temporary file stayed behind")
	}

	// A binary that does not match its checksum never touches the disk.
	broken := func(ctx context.Context, path string) ([]byte, error) {
		if strings.HasSuffix(path, ".sha256") {
			return []byte("0000000000000000000000000000000000000000000000000000000000000000\n"), nil
		}
		return newBinary, nil
	}
	if err := replaceBinary(context.Background(), broken, "/agent/linux-amd64", "0.2.0", exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a wrong checksum must stop the update: %v", err)
	}

	// Neither does one that says it is another version (wrong architecture, truncated download).
	wrong := func(ctx context.Context, path string) ([]byte, error) {
		body := []byte("#!/bin/sh\necho 0.1.0\n")
		if strings.HasSuffix(path, ".sha256") {
			return []byte(fmt.Sprintf("%x\n", sha256.Sum256(body))), nil
		}
		return body, nil
	}
	if err := replaceBinary(context.Background(), wrong, "/agent/linux-amd64", "0.2.0", exe); err == nil {
		t.Fatal("a binary that does not report the expected version must stop the update")
	}
	if got, _ := os.ReadFile(exe); string(got) != string(newBinary) {
		t.Fatal("a failed update must leave the working agent in place")
	}
}
