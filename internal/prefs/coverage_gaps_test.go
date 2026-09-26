package prefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPathAndSaveFailures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if DefaultPath() != defaultPrefsPath {
		t.Fatalf("default path = %q", DefaultPath())
	}
	path := filepath.Join(home, "prefs.toml")
	if err := Save(path, Prefs{Theme: "Nightfox"}); err != nil {
		t.Fatal(err)
	}
	if got := Load(path).Theme; got != "Nightfox" {
		t.Fatalf("saved theme = %q", got)
	}
	if got := Load(filepath.Join(home, "missing")).Theme; got != defaultTheme {
		t.Fatalf("missing theme = %q", got)
	}
	if err := Save(filepath.Join(path, "child.toml"), Prefs{}); err == nil || !strings.Contains(err.Error(), "create prefs dir") {
		t.Fatalf("file as parent: %v", err)
	}
	dir := filepath.Join(home, "is-directory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, Prefs{}); err == nil || !strings.Contains(err.Error(), "write prefs") {
		t.Fatalf("directory as file: %v", err)
	}
}
