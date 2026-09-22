package main

import (
	"go/build"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Slowpoke ships under two licences: the agent and everything it carries are MIT (LICENSE-MIT),
// the server and the panel are AGPL (LICENSE). That split is only honest while the two halves
// share no code, and an import added without thinking would silently break it. So the import
// graph is checked here rather than trusted.
const modulePath = "github.com/getslowpoke/slowpoke"

// The exact list printed in LICENSE-MIT. Adding a package here means putting it there too.
var mitPackages = map[string]bool{
	"cmd/slowpoke-agent":    true,
	"internal/agent":        true,
	"internal/agent/detect": true,
	"internal/fingerprint":  true,
	"internal/protocol":     true,
	"internal/sqlsafe":      true,
}

func TestTheAgentCarriesOnlyMITPackages(t *testing.T) {
	var extra []string
	for _, p := range ownDependencies(t, "cmd/slowpoke-agent") {
		if !mitPackages[p] {
			extra = append(extra, p)
		}
	}
	if len(extra) > 0 {
		t.Fatalf("the agent now imports packages that LICENSE-MIT does not cover: %v\n"+
			"either drop the import or add the package to LICENSE-MIT and to mitPackages", extra)
	}
}

func TestTheServerCarriesNothingOfTheAgent(t *testing.T) {
	for _, p := range ownDependencies(t, "cmd/slowpoke-server") {
		if p == "cmd/slowpoke-agent" || p == "internal/agent" || strings.HasPrefix(p, "internal/agent/") {
			t.Fatalf("the AGPL server imports the MIT agent package %q: "+
				"move the shared code into internal/protocol or internal/fingerprint", p)
		}
	}
}

// ownDependencies lists, transitively, the packages of this module that dir needs, dir included.
// Packages outside the module are someone else's licence and not our business here.
func ownDependencies(t *testing.T, dir string) []string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(pkg string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		p, err := build.ImportDir(filepath.Join(root, pkg), 0)
		if err != nil {
			t.Fatalf("reading %s: %v", pkg, err)
		}
		for _, imp := range p.Imports {
			if rel := strings.TrimPrefix(imp, modulePath+"/"); rel != imp {
				walk(rel)
			}
		}
	}
	walk(dir)
	out := make([]string, 0, len(seen))
	for pkg := range seen {
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}
