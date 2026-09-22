// Command slowpoke-skillgen writes the skill for Codex, Cursor, Copilot and Windsurf.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/getslowpoke/slowpoke/internal/skillgen"
)

func main() {
	files, err := skillgen.Render(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(f.Path, []byte(f.Content), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", f.Path)
	}
}
