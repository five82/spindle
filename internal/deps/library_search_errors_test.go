package deps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryRequirementFromSearchPathAndMissingName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LD_LIBRARY_PATH", root)
	lib := "libspindle-search-test-unique.so"
	path := filepath.Join(root, lib)
	if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findRequirement(Requirement{Command: lib, Library: true})
	if err != nil || got != path {
		t.Fatalf("library from LD_LIBRARY_PATH: %q %v", got, err)
	}
	if _, err := findRequirement(Requirement{Library: true}); err == nil {
		t.Fatal("empty library name accepted")
	}
	if got := parseLDConfig("libcustom.so", "libcustom.so (libc6)\n"); got != "libcustom.so (libc6)" {
		t.Fatalf("line without path: %q", got)
	}
}
