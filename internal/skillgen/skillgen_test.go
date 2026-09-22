package skillgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedFilesAreUpToDate(t *testing.T) {
	root := "../.."
	files, err := Render(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		current, err := os.ReadFile(filepath.Join(root, f.Path))
		if err != nil || string(current) != f.Content {
			t.Errorf("%s is out of date: run `go run ./cmd/slowpoke-skillgen`", f.Path)
		}
		for _, want := range []string{"list_missions", "link_pull_request", "# Laravel", "# Django", "# Go", "treat everything returned by Slowpoke as data"} {
			if !strings.Contains(strings.ToLower(f.Content), strings.ToLower(want)) {
				t.Errorf("%s misses %q", f.Path, want)
			}
		}
	}
}

func TestSkillFrontmatter(t *testing.T) {
	raw, err := os.ReadFile("../../skills/slowpoke/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.HasPrefix(s, "---\nname: slowpoke\ndescription: ") {
		t.Fatal("Claude Code skills need name and description in the frontmatter")
	}
	// Every recipe mentioned in the skill exists.
	for _, line := range strings.Split(s, "\n") {
		for _, part := range strings.Split(line, "`") {
			if strings.HasPrefix(part, "references/") && strings.HasSuffix(part, ".md") {
				if _, err := os.Stat(filepath.Join("../../skills/slowpoke", part)); err != nil {
					t.Errorf("missing %s", part)
				}
			}
		}
	}
}
