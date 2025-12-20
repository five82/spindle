package plex

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"spindle/internal/config"
)

type stubTokenProvider struct {
	token string
	err   error
	id    string
	auth  string
	cache string
}

func (s *stubTokenProvider) Token(ctx context.Context) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.token, nil
}

func (s *stubTokenProvider) ClientIdentifier() string {
	return s.id
}

func (s *stubTokenProvider) AuthorizationToken() string {
	return s.auth
}

func (s *stubTokenProvider) ResolvedPlexURL() string {
	return s.cache
}

func (s *stubTokenProvider) SaveResolvedPlexURL(url string) error {
	s.cache = url
	return nil
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
		if got := r.Header.Get("X-Plex-Client-Identifier"); got != "client-123" {
			t.Fatalf("expected client identifier header client-123, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Plex.URL = server.URL

	err := CheckAuth(context.Background(), &cfg, server.Client(), &stubTokenProvider{
		token: "token-123",
		id:    "client-123",
		auth:  "token-123",
	})
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
	cfg.Plex.URL = server.URL

	err := CheckAuth(context.Background(), &cfg, server.Client(), &stubTokenProvider{
		token: "anything",
		auth:  "anything",
	})
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
	cfg.Plex.URL = server.URL

	err := CheckAuth(context.Background(), &cfg, server.Client(), &stubTokenProvider{
		token: "anything",
		auth:  "anything",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestCheckAuthHostnameMismatchResolves(t *testing.T) {
	t.Helper()

	token := "token-123"
	cfg := config.Default()
	cfg.Plex.URL = "https://internal.example:32400"

	resourcesXML := fmt.Sprintf(`
<resources>
  <resource name="exampleplex" accessToken="%s" provides="server" clientIdentifier="server-1">
    <connections>
      <connection uri="https://resolved.example.plex.direct:32400" protocol="https" local="1" relay="0"/>
    </connections>
  </resource>
</resources>`, token)

	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, resourcesXML)
	}))
	defer resources.Close()

	originalEndpoint := plexResourcesEndpoint
	plexResourcesEndpoint = resources.URL
	t.Cleanup(func() {
		plexResourcesEndpoint = originalEndpoint
	})

	doer := &fallbackHTTPDoer{}

	provider := &stubTokenProvider{
		token: token,
		auth:  token,
		id:    "client-123",
	}

	if err := CheckAuth(context.Background(), &cfg, doer, provider); err != nil {
		t.Fatalf("CheckAuth returned error: %v", err)
	}

	if provider.cache != "https://resolved.example.plex.direct:32400" {
		t.Fatalf("expected cached resolved URL, got %q", provider.cache)
	}
	if len(doer.calls) != 2 {
		t.Fatalf("expected 2 HTTP calls, got %d", len(doer.calls))
	}
	if !strings.HasPrefix(doer.calls[0], "https://internal.example:32400") {
		t.Fatalf("expected first call to configured host, got %q", doer.calls[0])
	}
	if !strings.HasPrefix(doer.calls[1], "https://resolved.example.plex.direct:32400") {
		t.Fatalf("expected second call to resolved host, got %q", doer.calls[1])
	}
}

func TestCheckAuthHTTPConfiguredURLResolvesImmediately(t *testing.T) {
	t.Helper()

	token := "token-123"
	cfg := config.Default()
	cfg.Plex.URL = "http://internal.example:32400"

	resourcesXML := fmt.Sprintf(`
<resources>
  <resource name="exampleplex" accessToken="%s" provides="server" clientIdentifier="server-1">
    <connections>
      <connection uri="https://resolved.example.plex.direct:32400" protocol="https" local="1" relay="0"/>
    </connections>
  </resource>
</resources>`, token)

	resources := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, resourcesXML)
	}))
	defer resources.Close()

	originalEndpoint := plexResourcesEndpoint
	plexResourcesEndpoint = resources.URL
	t.Cleanup(func() {
		plexResourcesEndpoint = originalEndpoint
	})

	doer := &fallbackHTTPDoer{}
	provider := &stubTokenProvider{
		token: token,
		auth:  token,
		id:    "client-123",
	}

	if err := CheckAuth(context.Background(), &cfg, doer, provider); err != nil {
		t.Fatalf("CheckAuth returned error: %v", err)
	}

	if len(doer.calls) != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", len(doer.calls))
	}
	if !strings.HasPrefix(doer.calls[0], "https://resolved.example.plex.direct:32400") {
		t.Fatalf("expected call to resolved host, got %q", doer.calls[0])
	}
}

type fallbackHTTPDoer struct {
	calls []string
}

func (d *fallbackHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	d.calls = append(d.calls, req.URL.String())
	if strings.Contains(req.URL.Host, "internal.example") {
		cert := &x509.Certificate{DNSNames: []string{"*.resolved.example.plex.direct"}}
		return nil, &url.Error{
			Op:  "Get",
			URL: req.URL.String(),
			Err: &x509.HostnameError{
				Certificate: cert,
				Host:        req.URL.Host,
			},
		}
	}

	if strings.Contains(req.URL.Host, "resolved.example.plex.direct") {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	}

	return nil, fmt.Errorf("unexpected request host %s", req.URL.Host)
}
