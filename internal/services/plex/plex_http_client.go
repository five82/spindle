package plex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// PlexClient handles HTTP communication with Plex APIs required for auth.
type PlexClient interface {
	RequestPin(ctx context.Context, clientIdentifier string) (*Pin, error)
	PollPin(ctx context.Context, clientIdentifier string, id int64) (*PinStatus, error)
	RegisterKey(ctx context.Context, clientIdentifier, authorizationToken, publicKey string) (string, time.Time, error)
	ExchangeToken(ctx context.Context, clientIdentifier, authorizationToken, keyID string) (string, time.Time, error)
}

// HTTPDoer abstracts http.Client.Do for testing.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// httpPlexClient implements PlexClient using HTTP JSON requests.
type httpPlexClient struct {
	baseURL string
	client  HTTPDoer
}

// NewHTTPPlexClient constructs a Plex API client using the provided HTTP backend.
func NewHTTPPlexClient(baseURL string, client HTTPDoer) PlexClient {
	trimmed := strings.TrimRight(baseURL, "/")
	return &httpPlexClient{baseURL: trimmed, client: client}
}

func (c *httpPlexClient) RequestPin(ctx context.Context, clientIdentifier string) (*Pin, error) {
	var resp pinResponse
	if err := c.doJSONRequest(ctx, http.MethodPost, "/api/v2/pins", nil, nil, clientIdentifier, "", &resp); err != nil {
		return nil, err
	}

	return &Pin{
		ID:        resp.ID,
		Code:      resp.Code,
		ExpiresAt: resp.expirationTime(),
	}, nil
}

func (c *httpPlexClient) PollPin(ctx context.Context, clientIdentifier string, id int64) (*PinStatus, error) {
	path := fmt.Sprintf("/api/v2/pins/%d", id)
	var resp pinResponse
	if err := c.doJSONRequest(ctx, http.MethodGet, path, nil, nil, clientIdentifier, "", &resp); err != nil {
		return nil, err
	}

	status := &PinStatus{
		ExpiresAt: resp.expirationTime(),
	}
	if token := strings.TrimSpace(resp.AuthToken); token != "" {
		status.Authorized = true
		status.AuthorizationToken = token
	}
	return status, nil
}

func (c *httpPlexClient) RegisterKey(ctx context.Context, clientIdentifier, authorizationToken, publicKey string) (string, time.Time, error) {
	var reqBody = map[string]string{
		"public_key": publicKey,
	}
	var resp struct {
		KeyID     string  `json:"key_id"`
		ExpiresIn float64 `json:"expires_in"`
		ExpiresAt string  `json:"expires_at"`
	}

	headers := map[string]string{
		"X-Plex-Token": authorizationToken,
	}
	if err := c.doJSONRequest(ctx, http.MethodPost, "/api/v2/auth/keys", reqBody, headers, clientIdentifier, authorizationToken, &resp); err != nil {
		return "", time.Time{}, err
	}

	if resp.KeyID == "" {
		return "", time.Time{}, errors.New("plex auth: missing key_id in response")
	}
	return resp.KeyID, deriveExpiration(resp.ExpiresAt, resp.ExpiresIn), nil
}

func (c *httpPlexClient) ExchangeToken(ctx context.Context, clientIdentifier, authorizationToken, keyID string) (string, time.Time, error) {
	var reqBody = map[string]string{
		"client_identifier":   clientIdentifier,
		"key_id":              keyID,
		"authorization_token": authorizationToken,
	}
	var resp struct {
		Token     string  `json:"token"`
		ExpiresIn float64 `json:"expires_in"`
		ExpiresAt string  `json:"expires_at"`
	}

	headers := map[string]string{
		"X-Plex-Token": authorizationToken,
	}
	if err := c.doJSONRequest(ctx, http.MethodPost, "/api/v2/auth/token", reqBody, headers, clientIdentifier, authorizationToken, &resp); err != nil {
		return "", time.Time{}, err
	}

	if resp.Token == "" {
		return "", time.Time{}, errors.New("plex auth: missing token in response")
	}
	return resp.Token, deriveExpiration(resp.ExpiresAt, resp.ExpiresIn), nil
}

func (c *httpPlexClient) doJSONRequest(ctx context.Context, method, path string, body any, headers map[string]string, clientIdentifier, authorizationToken string, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	applyStandardHeaders(req, clientIdentifier)
	if authorizationToken != "" {
		req.Header.Set("X-Plex-Token", authorizationToken)
	}
	for k, v := range headers {
		if strings.TrimSpace(v) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("plex request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		errBody := strings.TrimSpace(string(bodyBytes))
		if resp.StatusCode == http.StatusUnauthorized {
			return ErrAuthorizationMissing
		}
		return fmt.Errorf("plex %s %s returned %d: %s", method, path, resp.StatusCode, errBody)
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func applyStandardHeaders(req *http.Request, clientIdentifier string) {
	req.Header.Set("X-Plex-Client-Identifier", clientIdentifier)
	req.Header.Set("X-Plex-Product", managedProductName)
	req.Header.Set("X-Plex-Version", managedProductVersion)
	req.Header.Set("X-Plex-Device-Name", managedProductName)
	req.Header.Set("X-Plex-Platform", runtime.GOOS)
}
