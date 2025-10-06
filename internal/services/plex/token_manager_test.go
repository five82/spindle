package plex

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
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

	nonceCalls := 0
	tokenCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/nonce":
			nonceCalls++
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method for nonce: %s", r.Method)
			}
			if got := r.Header.Get("X-Plex-Client-Identifier"); got != "refresh-client" {
				t.Fatalf("unexpected client id header: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"nonce":"abc-123"}`))
		case "/api/v2/auth/token":
			tokenCalls++
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method for token: %s", r.Method)
			}
			if got := r.Header.Get("X-Plex-Client-Identifier"); got != "refresh-client" {
				t.Fatalf("unexpected client id header on token: %q", got)
			}
			if got := r.Header.Get("X-Plex-Token"); got != "auth-token" {
				t.Fatalf("unexpected plex token header: %q", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read token request: %v", err)
			}
			var payload map[string]string
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode token request: %v", err)
			}
			if payload["jwt"] == "" {
				t.Fatalf("expected device jwt in request, got %#v", payload)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"auth_token":"fresh-token","expires_in":604800}`))
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
	if stored["authorization_token"].(string) != "fresh-token" {
		t.Fatalf("authorization token not updated: %v", stored["authorization_token"])
	}
	if nonceCalls != 1 || tokenCalls != 1 {
		t.Fatalf("unexpected call counts: nonce=%d token=%d", nonceCalls, tokenCalls)
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
