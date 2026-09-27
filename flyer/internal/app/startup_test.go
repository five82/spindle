package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/five82/flyer/internal/spindle"
	"github.com/five82/flyer/internal/ui"
)

func TestRunStartsUIWithConfiguredAndOverriddenAPI(t *testing.T) {
	for _, tc := range []struct {
		name         string
		override     bool
		wantPID      int
		wantInterval time.Duration
	}{
		{name: "config defaults", wantPID: 11, wantInterval: defaultPollInterval},
		{name: "explicit overrides", override: true, wantPID: 22, wantInterval: 7 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			server := func(pid int, token string) *httptest.Server {
				t.Helper()
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer "+token {
						http.Error(w, "wrong token", http.StatusUnauthorized)
						return
					}
					switch r.URL.Path {
					case "/api/status":
						_ = json.NewEncoder(w).Encode(spindle.StatusResponse{PID: pid})
					case "/api/queue":
						_ = json.NewEncoder(w).Encode(spindle.QueueListResponse{Items: []spindle.QueueItem{{ID: int64(pid)}}})
					default:
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(s.Close)
				return s
			}
			configServer := server(11, "config-token")
			configPath := filepath.Join(home, "spindle.toml")
			if err := os.WriteFile(configPath, []byte(fmt.Sprintf("[api]\nbind = %q\ntoken = %q\n[paths]\nstate_dir = %q\n", configServer.URL, "config-token", filepath.Join(home, "state"))), 0600); err != nil {
				t.Fatal(err)
			}
			prefsPath := filepath.Join(home, "prefs.toml")
			if err := os.WriteFile(prefsPath, []byte("theme = 'Amber'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			opts := Options{ConfigPath: configPath, PrefsPath: prefsPath}
			if tc.override {
				opts.APIEndpoint = server(22, "cli-token").URL
				opts.APIToken = "cli-token"
				opts.PollEvery = 7
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			err := run(ctx, opts, func(got ui.Options) error {
				called = true
				if got.Context != ctx || got.PrefsPath != prefsPath || got.ThemeName != "Amber" || got.PollTick != tc.wantInterval {
					t.Errorf("UI options: context=%v prefs=%q theme=%q tick=%v", got.Context, got.PrefsPath, got.ThemeName, got.PollTick)
				}
				if got.Config == nil || got.Config.APIBind != configServer.URL || got.Config.DaemonLogPath() != filepath.Join(home, "state", "daemon.log") {
					t.Errorf("UI config = %+v", got.Config)
				}
				if got.Store == nil || got.Client == nil || got.Refresh == nil {
					t.Fatal("UI missing store, client or refresh callback")
				}
				// The initial refresh must finish before the UI starts. A manual
				// refresh must remain wired to the same client and store.
				checkSnapshot := func(stage string) {
					t.Helper()
					snap := got.Store.Snapshot()
					if !snap.HasStatus || snap.LastError != nil || snap.Status.PID != tc.wantPID || len(snap.Queue) != 1 || snap.Queue[0].ID != int64(tc.wantPID) {
						t.Errorf("%s snapshot = %+v; want status and queue for PID %d", stage, snap, tc.wantPID)
					}
				}
				checkSnapshot("initial")
				if err := got.Refresh(); err != nil {
					t.Fatalf("manual refresh: %v", err)
				}
				checkSnapshot("manual")
				return nil
			})
			if err != nil || !called {
				t.Fatalf("run() = %v, UI called = %v", err, called)
			}
		})
	}
}

func TestRunReturnsUIErrorWhenAPIUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := errors.New("UI stopped")
	called := false
	err := run(ctx, Options{
		ConfigPath:  filepath.Join(home, "missing.toml"),
		PrefsPath:   filepath.Join(home, "missing-prefs.toml"),
		APIEndpoint: server.URL,
	}, func(got ui.Options) error {
		called = true
		if got.ThemeName != "Slate" || got.Store.Snapshot().LastError == nil {
			t.Errorf("missing prefs or failed initial refresh: theme=%q snapshot=%+v", got.ThemeName, got.Store.Snapshot())
		}
		return want
	})
	if !called || !errors.Is(err, want) {
		t.Fatalf("run() = %v, UI called = %v; want UI error", err, called)
	}
}
