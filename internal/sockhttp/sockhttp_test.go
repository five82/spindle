package sockhttp

import (
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestNewUnixClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, "ready")
	})}
	go func() { _ = server.Serve(ln) }()
	defer func() { _ = server.Close() }()

	client := NewUnixClient(path, time.Second)
	if client.Timeout != time.Second {
		t.Fatalf("timeout = %v, want 1s", client.Timeout)
	}
	req, err := http.NewRequest(http.MethodGet, "http://localhost/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	SetAuth(req, "secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "ready" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
}

func TestSetAuthEmptyToken(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://localhost/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "existing")
	SetAuth(req, "")
	if got := req.Header.Get("Authorization"); got != "existing" {
		t.Errorf("Authorization = %q, want existing", got)
	}
}
