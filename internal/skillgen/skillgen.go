// Package skillgen renders the Slowpoke skill (skills/slowpoke) for the other
// coding assistants, so there is one source of truth.
package skillgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type File struct {
	Path    string
	Content string
}

const header = "<!-- Generated from skills/slowpoke by `go run ./cmd/slowpoke-skillgen`. Do not edit. -->\n\n"

// Render reads skills/slowpoke under root and returns the generated files.
func Render(root string) ([]File, error) {
	skillRaw, err := os.ReadFile(filepath.Join(root, "skills", "slowpoke", "SKILL.md"))
	if err != nil {
		return nil, err
	}
	body := stripFrontmatter(string(skillRaw))
	description := frontmatterValue(string(skillRaw), "description")

	refsDir := filepath.Join(root, "skills", "slowpoke", "references")
	entries, err := os.ReadDir(refsDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var refs strings.Builder
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(refsDir, n))
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&refs, "\n\n---\n\n<!-- references/%s -->\n\n%s", n, strings.TrimSpace(string(raw)))
	}
	// Other assistants get everything in one file: references are inlined.
	combined := strings.ReplaceAll(strings.TrimSpace(body), "in `references/` (see below)", "in the stack recipes at the end of this file")
	combined = strings.ReplaceAll(combined, "`references/", "`")
	full := combined + refs.String() + "\n"

	return []File{
		{Path: "integrations/codex/AGENTS.md", Content: header + full},
		{Path: "integrations/copilot/copilot-instructions.md", Content: header + full},
		{Path: "integrations/cursor/slowpoke.mdc", Content: "---\ndescription: " + description + "\nglobs:\nalwaysApply: false\n---\n" + header + full},
		{Path: "integrations/windsurf/slowpoke.md", Content: header + full},
	}, nil
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	if end := strings.Index(s[4:], "\n---\n"); end >= 0 {
		return s[4+end+5:]
	}
	return s
}

func frontmatterValue(s, key string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	return ""
}
