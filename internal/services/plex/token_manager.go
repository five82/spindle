package plex

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"spindle/internal/config"
)

// HTTPDoer abstracts http.Client.Do for testing.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

var (
	// ErrAuthorizationMissing is returned when no Plex authorization token has been linked yet.
	ErrAuthorizationMissing = errors.New("plex authorization token not linked")
)

const (
	defaultBaseURL        = "https://plex.tv"
	stateFileName         = "plex_auth.json"
	tokenRefreshLeeway    = time.Hour
	keyRefreshLeeway      = 24 * time.Hour
	managedProductName    = "Spindle"
	managedProductVersion = "1.0.0"
)

// TokenManagerOption customises TokenManager construction.
type TokenManagerOption func(*TokenManager)

// WithHTTPClient overrides the HTTP client used for Plex API calls.
func WithHTTPClient(client HTTPDoer) TokenManagerOption {
	return func(m *TokenManager) {
		m.client = client
	}
}

// WithBaseURL overrides the base URL for Plex API calls (used in tests).
func WithBaseURL(baseURL string) TokenManagerOption {
	return func(m *TokenManager) {
		m.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// TokenManager persists Plex authentication state and refreshes the 7-day JWT.
type TokenManager struct {
	cfg       *config.Config
	client    HTTPDoer
	baseURL   string
	statePath string

	mu    sync.Mutex
	state tokenState
}

type tokenState struct {
	AuthorizationToken string    `json:"authorization_token"`
	ClientIdentifier   string    `json:"client_identifier"`
	PrivateKey         string    `json:"private_key"`
	PublicKey          string    `json:"public_key"`
	KeyID              string    `json:"key_id"`
	KeyExpiresAt       time.Time `json:"key_expires_at"`
	Token              string    `json:"token"`
	TokenExpiresAt     time.Time `json:"token_expires_at"`
}

// NewTokenManager builds a TokenManager using the provided configuration.
func NewTokenManager(cfg *config.Config, opts ...TokenManagerOption) (*TokenManager, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	mgr := &TokenManager{
		cfg:       cfg,
		baseURL:   defaultBaseURL,
		statePath: filepath.Join(cfg.LogDir, stateFileName),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(mgr)
	}
	if mgr.client == nil {
		mgr.client = &http.Client{Timeout: 10 * time.Second}
	}

	if err := mgr.loadState(); err != nil {
		return nil, err
	}

	dirty := false
	if mgr.state.ClientIdentifier == "" {
		id := strings.ReplaceAll(uuid.New().String(), "-", "")
		mgr.state.ClientIdentifier = id
		dirty = true
	}
	if dirty {
		if err := mgr.saveState(); err != nil {
			return nil, err
		}
	}

	return mgr, nil
}

// HasAuthorization reports whether a long-lived Plex authorization token is available.
func (m *TokenManager) HasAuthorization() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.TrimSpace(m.state.AuthorizationToken) != ""
}

// Token ensures a current 7-day Plex JWT and returns it.
func (m *TokenManager) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state.Token != "" && time.Until(m.state.TokenExpiresAt) > tokenRefreshLeeway {
		return m.state.Token, nil
	}

	if err := m.refreshLocked(ctx); err != nil {
		return "", err
	}
	return m.state.Token, nil
}

// SetAuthorizationToken stores the long-lived authorization token from Plex link flow.
func (m *TokenManager) SetAuthorizationToken(token string) error {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return errors.New("authorization token is empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.AuthorizationToken = trimmed
	m.state.KeyID = ""
	m.state.KeyExpiresAt = time.Time{}
	m.state.Token = ""
	m.state.TokenExpiresAt = time.Time{}

	return m.saveState()
}

// RequestPin starts the Plex device linking flow.

func (m *TokenManager) RequestPin(ctx context.Context) (*Pin, error) {
	var resp pinResponse
	if err := m.doJSONRequest(ctx, http.MethodPost, "/api/v2/pins", nil, nil, &resp); err != nil {
		return nil, err
	}

	return &Pin{
		ID:        resp.ID,
		Code:      resp.Code,
		ExpiresAt: resp.expirationTime(),
	}, nil
}

// PollPin checks whether the user has approved the Plex link code.
func (m *TokenManager) PollPin(ctx context.Context, id int64) (*PinStatus, error) {
	path := fmt.Sprintf("/api/v2/pins/%d", id)
	var resp pinResponse
	if err := m.doJSONRequest(ctx, http.MethodGet, path, nil, nil, &resp); err != nil {
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

// Pin represents a Plex linking code.
type Pin struct {
	ID        int64
	Code      string
	ExpiresAt time.Time
}

// PinStatus represents the latest status of a link PIN.
type PinStatus struct {
	Authorized         bool
	AuthorizationToken string
	ExpiresAt          time.Time
}

type pinResponse struct {
	ID        int64   `json:"id"`
	Code      string  `json:"code"`
	AuthToken string  `json:"authToken"`
	ExpiresIn float64 `json:"expires_in"`
	ExpiresAt string  `json:"expires_at"`
}

func (p pinResponse) expirationTime() time.Time {
	if p.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, p.ExpiresAt); err == nil {
			return t
		}
	}
	if p.ExpiresIn > 0 {
		return time.Now().Add(time.Duration(p.ExpiresIn) * time.Second)
	}
	return time.Time{}
}

func (m *TokenManager) refreshLocked(ctx context.Context) error {
	if strings.TrimSpace(m.state.AuthorizationToken) == "" {
		if err := m.reloadLocked(); err != nil {
			return err
		}
		if strings.TrimSpace(m.state.AuthorizationToken) == "" {
			return ErrAuthorizationMissing
		}
	}

	if err := m.ensureKeyLocked(ctx); err != nil {
		return err
	}
	if err := m.exchangeTokenLocked(ctx); err != nil {
		return err
	}
	return m.saveState()
}

func (m *TokenManager) ensureKeyLocked(ctx context.Context) error {
	if m.state.PrivateKey == "" || m.state.PublicKey == "" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("generate ed25519 key: %w", err)
		}
		m.state.PrivateKey = base64.StdEncoding.EncodeToString(priv)
		m.state.PublicKey = base64.StdEncoding.EncodeToString(pub)
		m.state.KeyID = ""
		m.state.KeyExpiresAt = time.Time{}
	}

	if m.state.KeyID != "" && time.Until(m.state.KeyExpiresAt) > keyRefreshLeeway {
		return nil
	}

	return m.registerKeyLocked(ctx)
}

func (m *TokenManager) registerKeyLocked(ctx context.Context) error {
	var reqBody = map[string]string{
		"public_key": m.state.PublicKey,
	}
	var resp struct {
		KeyID     string  `json:"key_id"`
		ExpiresIn float64 `json:"expires_in"`
		ExpiresAt string  `json:"expires_at"`
	}

	headers := map[string]string{
		"X-Plex-Token": m.state.AuthorizationToken,
	}
	if err := m.doJSONRequest(ctx, http.MethodPost, "/api/v2/auth/keys", reqBody, headers, &resp); err != nil {
		return err
	}

	if resp.KeyID == "" {
		return errors.New("plex auth: missing key_id in response")
	}
	m.state.KeyID = resp.KeyID
	m.state.KeyExpiresAt = deriveExpiration(resp.ExpiresAt, resp.ExpiresIn)
	return nil
}

func (m *TokenManager) exchangeTokenLocked(ctx context.Context) error {
	if m.state.KeyID == "" {
		return errors.New("plex auth: key not registered")
	}

	var reqBody = map[string]string{
		"client_identifier":   m.state.ClientIdentifier,
		"key_id":              m.state.KeyID,
		"authorization_token": m.state.AuthorizationToken,
	}
	var resp struct {
		Token     string  `json:"token"`
		ExpiresIn float64 `json:"expires_in"`
		ExpiresAt string  `json:"expires_at"`
	}

	headers := map[string]string{
		"X-Plex-Token": m.state.AuthorizationToken,
	}
	if err := m.doJSONRequest(ctx, http.MethodPost, "/api/v2/auth/token", reqBody, headers, &resp); err != nil {
		return err
	}

	if resp.Token == "" {
		return errors.New("plex auth: missing token in response")
	}
	m.state.Token = resp.Token
	m.state.TokenExpiresAt = deriveExpiration(resp.ExpiresAt, resp.ExpiresIn)
	return nil
}

func deriveExpiration(expiresAt string, expiresIn float64) time.Time {
	if expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			return t
		}
	}
	if expiresIn > 0 {
		return time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	return time.Now().Add(7 * 24 * time.Hour)
}

func (m *TokenManager) doJSONRequest(ctx context.Context, method, path string, body any, headers map[string]string, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	url := m.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	m.applyHeaders(req)
	for k, v := range headers {
		if strings.TrimSpace(v) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := m.client.Do(req)
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

func (m *TokenManager) applyHeaders(req *http.Request) {
	req.Header.Set("X-Plex-Client-Identifier", m.state.ClientIdentifier)
	req.Header.Set("X-Plex-Product", managedProductName)
	req.Header.Set("X-Plex-Version", managedProductVersion)
	req.Header.Set("X-Plex-Device-Name", managedProductName)
	req.Header.Set("X-Plex-Platform", runtime.GOOS)
}

func (m *TokenManager) loadState() error {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			m.state = tokenState{}
			return nil
		}
		return fmt.Errorf("read plex auth state: %w", err)
	}

	if err := json.Unmarshal(data, &m.state); err != nil {
		return fmt.Errorf("decode plex auth state: %w", err)
	}
	return nil
}

func (m *TokenManager) saveState() error {
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o755); err != nil {
		return fmt.Errorf("ensure auth state directory: %w", err)
	}

	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plex auth state: %w", err)
	}

	// Restrict permissions because the file contains secrets.
	if err := os.WriteFile(m.statePath, data, 0o600); err != nil {
		return fmt.Errorf("write plex auth state: %w", err)
	}
	return nil
}

func (m *TokenManager) reloadLocked() error {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reload plex auth state: %w", err)
	}

	var state tokenState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode plex auth state: %w", err)
	}

	// Preserve client identifier if absent on disk to avoid regenerating.
	if state.ClientIdentifier == "" {
		state.ClientIdentifier = m.state.ClientIdentifier
	}
	m.state = state
	return nil
}
