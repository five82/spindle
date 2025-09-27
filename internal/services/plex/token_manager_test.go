package plex

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/config"
)

func TestTokenManagerReturnsCachedToken(t *testing.T) {
	cfg := config.Default()
	cfg.LogDir = t.TempDir()

	state := map[string]any{
		"client_identifier":   "cached-client",
		"authorization_token": "auth-token",
		"token":               "cached-token",
		"token_expires_at":    time.Now().Add(12 * time.Hour).Format(time.RFC3339),
	}
	writeTokenState(t, filepath.Join(cfg.LogDir, stateFileName), state)

	manager, err := NewTokenManager(&cfg)
	if err != nil {
		t.Fatalf("new token manager: %v", err)
	}

	token, err := manager.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if token != "cached-token" {
		t.Fatalf("expected cached token, got %q", token)
	}
}

func TestTokenManagerRefreshesExpiredToken(t *testing.T) {
	cfg := config.Default()
	cfg.LogDir = t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/keys":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"key_id":"key-123","expires_in":604800}`))
		case "/api/v2/auth/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"fresh-token","expires_in":604800}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := server.Client()

	state := map[string]any{
		"client_identifier":   "refresh-client",
		"authorization_token": "auth-token",
		"token":               "stale",
		"token_expires_at":    time.Now().Add(-time.Hour).Format(time.RFC3339),
	}
	writeTokenState(t, filepath.Join(cfg.LogDir, stateFileName), state)

	manager, err := NewTokenManager(&cfg, WithHTTPClient(client), WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("new token manager: %v", err)
	}

	token, err := manager.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if token != "fresh-token" {
		t.Fatalf("expected refreshed token, got %q", token)
	}

	data, err := os.ReadFile(filepath.Join(cfg.LogDir, stateFileName))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if stored["token"].(string) != "fresh-token" {
		t.Fatalf("state token not updated: %v", stored["token"])
	}
}

func writeTokenState(t *testing.T, path string, state map[string]any) {
	t.Helper()

	if state["private_key"] == nil {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		state["private_key"] = base64.StdEncoding.EncodeToString(priv)
		state["public_key"] = base64.StdEncoding.EncodeToString(pub)
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}
