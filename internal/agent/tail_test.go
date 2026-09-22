package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func collect(t *testing.T, p *Positions, file string, fromStart bool) []string {
	t.Helper()
	var lines []string
	if err := ReadNewLines(p, file, fromStart, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	return lines
}

func appendTo(t *testing.T, file, s string) {
	t.Helper()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestFirstRunStartsFromEndUnlessFromStart(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "access.log")
	appendTo(t, file, "old 1\nold 2\n")

	if got := collect(t, LoadPositions(dir), file, false); len(got) != 0 {
		t.Fatalf("history must be skipped, got %v", got)
	}
	if got := collect(t, LoadPositions(t.TempDir()), file, true); !reflect.DeepEqual(got, []string{"old 1", "old 2"}) {
		t.Fatalf("from_start must read history, got %v", got)
	}
}

func TestReadsOnlyNewCompleteLines(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "access.log")
	appendTo(t, file, "")
	p := LoadPositions(dir)
	collect(t, p, file, false)

	appendTo(t, file, "a\nb\npart")
	if got := collect(t, p, file, false); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	appendTo(t, file, "ial\nc\r\n")
	if got := collect(t, p, file, false); !reflect.DeepEqual(got, []string{"partial", "c"}) {
		t.Fatalf("got %v", got)
	}
	if got := collect(t, p, file, false); len(got) != 0 {
		t.Fatalf("nothing new expected, got %v", got)
	}
}

func TestFollowsRotationAndTruncation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "access.log")
	appendTo(t, file, "")
	p := LoadPositions(dir)
	collect(t, p, file, false)
	appendTo(t, file, "before rotation\n")
	collect(t, p, file, false)

	// logrotate: rename + new file
	if err := os.Rename(file, file+".1"); err != nil {
		t.Fatal(err)
	}
	appendTo(t, file, "after rotation\n")
	if got := collect(t, p, file, false); !reflect.DeepEqual(got, []string{"after rotation"}) {
		t.Fatalf("rotation: got %v", got)
	}

	// copytruncate: same inode, smaller size
	if err := os.Truncate(file, 0); err != nil {
		t.Fatal(err)
	}
	appendTo(t, file, "x\n")
	if got := collect(t, p, file, false); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("truncate: got %v", got)
	}
}

func TestPositionsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "access.log")
	appendTo(t, file, "")
	p := LoadPositions(dir)
	collect(t, p, file, false)
	appendTo(t, file, "one\n")
	collect(t, p, file, false)
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	appendTo(t, file, "two\n")

	if got := collect(t, LoadPositions(dir), file, false); !reflect.DeepEqual(got, []string{"two"}) {
		t.Fatalf("got %v", got)
	}
}
