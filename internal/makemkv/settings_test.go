package makemkv

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnsureSettingsPreservesOtherKeysAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".MakeMKV", "settings.conf")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := EnsureSettings(logger); err != nil {
		t.Fatal(err)
	}
	got, order, err := readSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, requiredSettings) {
		t.Fatalf("new settings = %v", got)
	}
	if len(order) != len(requiredSettings) {
		t.Fatalf("order = %v", order)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSettings(logger); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("idempotence: %v %q", err, unchanged)
	}

	input := "# user comment\nother = \"has=equals\"\napp_LibdriveIO = \"false\"\nother = \"last\"\ninvalid line\n\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSettings(logger); err != nil {
		t.Fatal(err)
	}
	got, order, err = readSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["other"] != "last" || got["app_LibdriveIO"] != "true" || got["app_DefaultSelectionString"] != requiredSettings["app_DefaultSelectionString"] {
		t.Fatalf("updated settings = %v", got)
	}
	if order[0] != "other" || order[1] != "app_LibdriveIO" {
		t.Fatalf("original order lost: %v", order)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(text), "other =") != 1 {
		t.Fatalf("duplicate key: %s", text)
	}
}

func TestSettingsReadWriteErrorsAndEscaping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.conf")
	if got, order, err := readSettings(path); err != nil || len(got) != 0 || len(order) != 0 {
		t.Fatalf("missing: %v %v %v", got, order, err)
	}
	if err := writeSettings(path, map[string]string{"b": "quote\"value", "a": "first"}, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "a =") || !strings.Contains(string(raw), `quote\"value`) {
		t.Fatalf("sorted/escaped: %q", raw)
	}
	if err := writeSettings(path, map[string]string{"b": "second", "a": "first"}, []string{"b", "", "b", "a"}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil || string(raw) != "b = \"second\"\na = \"first\"\n" {
		t.Fatalf("order: %q %v", raw, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSettings(path); err == nil {
		t.Fatal("directory opened as settings")
	}
	if err := writeSettings(path, nil, nil); err == nil {
		t.Fatal("wrote to directory")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applySettings(filepath.Join(path, "nested", "settings.conf"), requiredSettings, slog.Default()); err == nil {
		t.Fatal("created directory inside file")
	}
	if err := os.WriteFile(path, append([]byte("key = value\n"), make([]byte, 70*1024)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSettings(path); err == nil {
		t.Fatal("scanner accepted oversized line")
	}
}
