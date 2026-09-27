package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingConfigUsesLocalPathDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load(filepath.Join(home, "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIBind != "" || cfg.APIToken != "" {
		t.Fatalf("API config = bind:%q token:%q, want empty", cfg.APIBind, cfg.APIToken)
	}
	if cfg.StateDir != filepath.Join(home, ".local", "state", "spindle") {
		t.Fatalf("StateDir = %q", cfg.StateDir)
	}
}

func TestLoadParsesCurrentSpindleConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[api]
bind = "  10.0.0.5:9999  "
token = "  secret  "

[paths]
state_dir = "  ~/.local/state/custom-spindle  "
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIBind != "10.0.0.5:9999" || cfg.APIToken != "secret" {
		t.Fatalf("API config = bind:%q token:%q", cfg.APIBind, cfg.APIToken)
	}
	wantState := filepath.Join(home, ".local", "state", "custom-spindle")
	if cfg.StateDir != wantState {
		t.Fatalf("StateDir = %q, want %q", cfg.StateDir, wantState)
	}
	if cfg.DaemonLogPath() != filepath.Join(wantState, "daemon.log") {
		t.Fatalf("DaemonLogPath = %q", cfg.DaemonLogPath())
	}
}

func TestLoadUsesXDGConfigHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configHome := t.TempDir()
	path := filepath.Join(configHome, "spindle", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[api]\nbind = \"127.0.0.1:7487\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", configHome)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIBind != "127.0.0.1:7487" {
		t.Fatalf("APIBind = %q", cfg.APIBind)
	}
}

func TestLoadEmptyValuesKeepEmptyAPIAndDefaultState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[api]\nbind = \"   \"\n[paths]\nstate_dir = \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIBind != "" || cfg.StateDir != filepath.Join(home, ".local", "state", "spindle") {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestLoadInvalidTOMLFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`[api`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("Load error = %v, want parse error", err)
	}
}

func TestExpandPathExpandsTildeAndReturnsAbs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := expandPath("~/a/b")
	if err != nil {
		t.Fatalf("expandPath: %v", err)
	}
	if want := filepath.Join(home, "a", "b"); got != want {
		t.Fatalf("expandPath = %q, want %q", got, want)
	}
}

func TestExpandPathEmptyErrors(t *testing.T) {
	if _, err := expandPath("   "); err == nil {
		t.Fatal("expandPath returned nil error")
	}
}
