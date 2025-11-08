package plex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"spindle/internal/config"
)

// CheckAuth verifies that the configured Plex server accepts the current authorization token.
func CheckAuth(ctx context.Context, cfg *config.Config, client HTTPDoer, provider TokenProvider) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if provider == nil {
		return errors.New("token provider is nil")
	}

	plexURL := strings.TrimRight(strings.TrimSpace(cfg.PlexURL), "/")
	if plexURL == "" {
		return errors.New("plex_url not configured")
	}

	var requester HTTPDoer
	if client != nil {
		requester = client
	} else {
		requester = &http.Client{Timeout: 10 * time.Second}
	}

	token, err := provider.Token(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, plexURL+"/library/sections", nil)
	if err != nil {
		return fmt.Errorf("build plex auth request: %w", err)
	}
	req.Header.Set("X-Plex-Token", token)
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("User-Agent", userAgent)

	resp, err := requester.Do(req)
	if err != nil {
		return fmt.Errorf("plex auth request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ErrAuthorizationMissing
	}
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("plex auth request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
