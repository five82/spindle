package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultLogDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if got, want := DefaultLogDir(), filepath.Join(os.Getenv("XDG_STATE_HOME"), "reel", "logs"); got != want {
		t.Errorf("DefaultLogDir() = %q, want %q", got, want)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", t.TempDir())
	if got, want := DefaultLogDir(), filepath.Join(os.Getenv("HOME"), ".local", "state", "reel", "logs"); got != want {
		t.Errorf("DefaultLogDir() = %q, want %q", got, want)
	}
}

func TestSetupAndLevels(t *testing.T) {
	var nilLogger *Logger
	if nilLogger.FilePath() != "" || nilLogger.Close() != nil {
		t.Fatal("nil logger should be inert")
	}
	nilLogger.Info("ignored")
	nilLogger.Debug("ignored")
	if n, err := nilLogger.Writer().Write([]byte("ignored")); n != 7 || err != nil {
		t.Fatalf("nil writer: %d, %v", n, err)
	}
	if l, err := Setup("", false, true, nil); l != nil || err != nil {
		t.Fatalf("disabled setup = %v, %v", l, err)
	}

	for _, verbose := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "nested", "logs")
		l, err := Setup(dir, verbose, false, []string{"reel", "movie.mkv"})
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(l.FilePath()) != dir {
			t.Errorf("log path = %q", l.FilePath())
		}
		l.Info("number %d", 42)
		l.Debug("secret %s", "detail")
		if _, err := l.Writer().Write([]byte("raw output\n")); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(l.FilePath())
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, want := range []string{"Command: reel movie.mkv", "Reel encoder starting", "number 42", "raw output"} {
			if !strings.Contains(text, want) {
				t.Errorf("log missing %q: %s", want, text)
			}
		}
		if strings.Contains(text, "secret detail") != verbose {
			t.Errorf("debug filtering incorrect (verbose=%v): %s", verbose, text)
		}
	}
}

func TestSetupDirectoryError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(filepath.Join(file, "logs"), false, false, nil); err == nil || !strings.Contains(err.Error(), "failed to create log directory") {
		t.Errorf("Setup() error = %v", err)
	}
}
