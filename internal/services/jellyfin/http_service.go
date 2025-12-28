package jellyfin

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"spindle/internal/config"
)

// HTTPDoer describes the HTTP client used by the Jellyfin service.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type httpService struct {
	simple  *SimpleService
	baseURL string
	apiKey  string
	client  HTTPDoer
}

// NewConfiguredService returns a Jellyfin service that moves files into the
// library and triggers Jellyfin scans when credentials are available.
func NewConfiguredService(cfg *config.Config) Service {
	if cfg == nil {
		return NewSimpleService("", "", "", false)
	}
	simple := NewSimpleService(cfg.Paths.LibraryDir, cfg.Library.MoviesDir, cfg.Library.TVDir, cfg.Library.OverwriteExisting)
	if !cfg.Jellyfin.Enabled {
		return simple
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.Jellyfin.URL), "/")
	apiKey := strings.TrimSpace(cfg.Jellyfin.APIKey)
	if baseURL == "" || apiKey == "" {
		return simple
	}
	return &httpService{
		simple:  simple,
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  http.DefaultClient,
	}
}

// NewHTTPService constructs an HTTP-backed Jellyfin service.
func NewHTTPService(simple *SimpleService, baseURL, apiKey string, client HTTPDoer) Service {
	return &httpService{
		simple:  simple,
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		client:  client,
	}
}

func (s *httpService) Organize(ctx context.Context, sourcePath string, meta MediaMetadata) (string, error) {
	return s.simple.Organize(ctx, sourcePath, meta)
}

func (s *httpService) Refresh(ctx context.Context, meta MediaMetadata) error {
	if s == nil || s.client == nil || s.baseURL == "" || s.apiKey == "" {
		return nil
	}
	refreshURL := fmt.Sprintf("%s/Library/Refresh", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, nil)
	if err != nil {
		return fmt.Errorf("build jellyfin refresh request: %w", err)
	}
	req.Header.Set("X-Emby-Token", s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh jellyfin library: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("jellyfin refresh returned %d", resp.StatusCode)
	}
	return nil
}
