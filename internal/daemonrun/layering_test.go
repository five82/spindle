package daemonrun

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const internalImportPrefix = "github.com/five82/spindle/internal/"

// TestPackageBoundaries makes the dependency rules executable. In particular,
// stage packages must share lower-level operations rather than reaching into
// another stage handler and coupling their lifecycles.
func TestPackageBoundaries(t *testing.T) {
	stagePackages := []string{
		"apply", "audioanalysis", "contentid", "encoder", "identify",
		"organizer", "ripper", "subtitle",
	}
	stageSet := make(map[string]bool, len(stagePackages))
	for _, pkg := range stagePackages {
		stageSet[pkg] = true
	}
	for _, pkg := range stagePackages {
		for _, imported := range packageImports(t, pkg) {
			name := strings.TrimPrefix(imported, internalImportPrefix)
			if stageSet[name] && name != pkg {
				t.Errorf("stage package %s imports stage package %s", pkg, name)
			}
		}
	}

	assertNoImports(t, "queue", map[string]bool{"ripspec": true})
	assertNoImports(t, "config", map[string]bool{
		"jellyfin": true, "keydb": true, "llm": true, "notify": true,
		"opensubtitles": true, "tmdb": true,
	})
}

func assertNoImports(t *testing.T, pkg string, banned map[string]bool) {
	t.Helper()
	for _, imported := range packageImports(t, pkg) {
		name := strings.TrimPrefix(imported, internalImportPrefix)
		if banned[name] {
			t.Errorf("package %s imports prohibited package %s", pkg, name)
		}
	}
}

func packageImports(t *testing.T, pkg string) []string {
	t.Helper()
	dir := filepath.Join("..", pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package %s: %v", pkg, err)
	}
	var imports []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s/%s: %v", pkg, entry.Name(), err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("parse import in %s/%s: %v", pkg, entry.Name(), err)
			}
			imports = append(imports, path)
		}
	}
	return imports
}
