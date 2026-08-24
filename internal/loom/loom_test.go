package loom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNew_EmptyURL(t *testing.T) {
	if c := New("", nil); c != nil {
		t.Fatal("expected nil client when url is empty")
	}
}

func TestNew_Valid(t *testing.T) {
	if c := New("http://localhost", nil); c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestScan_NilClient(t *testing.T) {
	var c *Client
	if err := c.Scan(context.Background()); err != nil {
		t.Fatalf("expected nil error on nil client, got: %v", err)
	}
}

func TestScan_Success(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var gotMethod, gotPath string
			var gotBody []byte
			var gotAuth, gotToken string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				gotBody, _ = io.ReadAll(r.Body)
				gotAuth = r.Header.Get("Authorization")
				gotToken = r.Header.Get("X-Emby-Token")
				w.WriteHeader(status)
			}))
			defer srv.Close()

			if err := New(srv.URL+"/", nil).Scan(context.Background()); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotMethod != http.MethodPost {
				t.Errorf("expected POST, got %s", gotMethod)
			}
			if gotPath != "/api/v1/scan" {
				t.Errorf("expected /api/v1/scan, got %s", gotPath)
			}
			if len(gotBody) != 0 {
				t.Errorf("expected empty request body, got %q", gotBody)
			}
			if gotAuth != "" || gotToken != "" {
				t.Errorf("unexpected auth headers: Authorization=%q X-Emby-Token=%q", gotAuth, gotToken)
			}
		})
	}
}

func TestScan_ErrorStatus(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status >= http.StatusMultipleChoices && status < http.StatusBadRequest {
					w.Header().Set("Location", "/api/v1/scan")
				}
				w.WriteHeader(status)
			}))
			defer srv.Close()

			if err := New(srv.URL, nil).Scan(context.Background()); err == nil {
				t.Fatalf("expected error on status %d", status)
			}
		})
	}
}
