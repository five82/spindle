package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNew_EmptyURL(t *testing.T) {
	c := New("", "some-key")
	if c != nil {
		t.Fatal("expected nil client when url is empty")
	}
}

func TestNew_EmptyAPIKey(t *testing.T) {
	c := New("http://localhost", "")
	if c != nil {
		t.Fatal("expected nil client when apiKey is empty")
	}
}

func TestNew_BothEmpty(t *testing.T) {
	c := New("", "")
	if c != nil {
		t.Fatal("expected nil client when both url and apiKey are empty")
	}
}

func TestNew_Valid(t *testing.T) {
	c := New("http://localhost", "test-key")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestRefresh_NilClient(t *testing.T) {
	var c *Client
	err := c.Refresh(context.Background())
	if err != nil {
		t.Fatalf("expected nil error on nil client, got: %v", err)
	}
}

func TestCheckHealth_NilClient(t *testing.T) {
	var c *Client
	err := c.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error on nil client")
	}
}

func TestRefresh_Success(t *testing.T) {
	var gotMethod, gotPath, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Emby-Token")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-api-key")
	err := c.Refresh(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/Library/Refresh" {
		t.Errorf("expected /Library/Refresh, got %s", gotPath)
	}
	if gotToken != "test-api-key" {
		t.Errorf("expected X-Emby-Token test-api-key, got %s", gotToken)
	}
}

func TestCheckHealth_Success(t *testing.T) {
	var gotMethod, gotPath, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Emby-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "health-key")
	err := c.CheckHealth(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("expected GET, got %s", gotMethod)
	}
	if gotPath != "/Users" {
		t.Errorf("expected /Users, got %s", gotPath)
	}
	if gotToken != "health-key" {
		t.Errorf("expected X-Emby-Token health-key, got %s", gotToken)
	}
}

func TestRefresh_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "key")
	err := c.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected error on 500 status")
	}
}

func TestCheckHealth_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(srv.URL, "key")
	err := c.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error on 403 status")
	}
}
