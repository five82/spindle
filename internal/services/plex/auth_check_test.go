package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"spindle/internal/config"
)

type stubTokenProvider struct {
	token string
	err   error
}

func (s *stubTokenProvider) Token(ctx context.Context) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.token, nil
}

func TestCheckAuthSuccess(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/sections" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Plex-Token"); got != "token-123" {
			t.Fatalf("expected token header token-123, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.PlexURL = server.URL

	err := CheckAuth(context.Background(), &cfg, server.Client(), &stubTokenProvider{token: "token-123"})
	if err != nil {
		t.Fatalf("CheckAuth returned error: %v", err)
	}
}

func TestCheckAuthUnauthorized(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.PlexURL = server.URL

	err := CheckAuth(context.Background(), &cfg, server.Client(), &stubTokenProvider{token: "anything"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err != ErrAuthorizationMissing {
		t.Fatalf("expected ErrAuthorizationMissing, got %v", err)
	}
}

func TestCheckAuthServerError(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.PlexURL = server.URL

	err := CheckAuth(context.Background(), &cfg, server.Client(), &stubTokenProvider{token: "anything"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}
