package plex

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"spindle/internal/config"
)

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
		m.httpClient = client
		m.plexClient = nil
	}
}

// WithBaseURL overrides the base URL for Plex API calls (used in tests).
func WithBaseURL(baseURL string) TokenManagerOption {
	return func(m *TokenManager) {
		m.baseURL = strings.TrimRight(baseURL, "/")
		m.plexClient = nil
	}
}

// WithTokenStore injects a custom persistence layer.
func WithTokenStore(store TokenStore) TokenManagerOption {
	return func(m *TokenManager) {
		m.store = store
	}
}

// WithPlexClient injects a prebuilt Plex API client.
func WithPlexClient(client PlexClient) TokenManagerOption {
	return func(m *TokenManager) {
		m.plexClient = client
	}
}

// WithKeyManager injects custom key management behaviour.
func WithKeyManager(manager KeyManager) TokenManagerOption {
	return func(m *TokenManager) {
		m.keyManager = manager
	}
}

// TokenManager persists Plex authentication state and refreshes the 7-day JWT.
type TokenManager struct {
	cfg *config.Config

	httpClient HTTPDoer
	baseURL    string
	store      TokenStore
	plexClient PlexClient
	keyManager KeyManager

	stateMu sync.RWMutex
	state   tokenState
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

	statePath := filepath.Join(cfg.LogDir, stateFileName)
	mgr := &TokenManager{
		cfg:        cfg,
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		store:      NewFileTokenStore(statePath),
	}

	for _, opt := range opts {
		opt(mgr)
	}

	if mgr.httpClient == nil {
		mgr.httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if mgr.store == nil {
		mgr.store = NewFileTokenStore(statePath)
	}
	if mgr.plexClient == nil {
		mgr.plexClient = NewHTTPPlexClient(mgr.baseURL, mgr.httpClient)
	}
	if mgr.keyManager == nil {
		mgr.keyManager = NewDefaultKeyManager(mgr.plexClient)
	}

	if err := mgr.loadInitialState(); err != nil {
		return nil, err
	}
	return mgr, nil
}

func (m *TokenManager) loadInitialState() error {
	state, err := m.store.Load()
	if err != nil {
		return err
	}

	dirty := false
	if state.ClientIdentifier == "" {
		state.ClientIdentifier = strings.ReplaceAll(uuid.New().String(), "-", "")
		dirty = true
	}
	m.state = state

	if dirty {
		if err := m.store.Save(m.state); err != nil {
			return err
		}
	}
	return nil
}

// HasAuthorization reports whether a long-lived Plex authorization token is available.
func (m *TokenManager) HasAuthorization() bool {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return strings.TrimSpace(m.state.AuthorizationToken) != ""
}

// Token ensures a current 7-day Plex JWT and returns it.
func (m *TokenManager) Token(ctx context.Context) (string, error) {
	if token, ok := m.cachedToken(); ok {
		return token, nil
	}
	return m.refreshToken(ctx)
}

func (m *TokenManager) cachedToken() (string, bool) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	if m.state.Token != "" && time.Until(m.state.TokenExpiresAt) > tokenRefreshLeeway {
		return m.state.Token, true
	}
	return "", false
}

func (m *TokenManager) refreshToken(ctx context.Context) (string, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	if m.state.Token != "" && time.Until(m.state.TokenExpiresAt) > tokenRefreshLeeway {
		return m.state.Token, nil
	}

	state := m.state
	if strings.TrimSpace(state.AuthorizationToken) == "" {
		if err := m.reloadLocked(); err != nil {
			return "", err
		}
		state = m.state
		if strings.TrimSpace(state.AuthorizationToken) == "" {
			return "", ErrAuthorizationMissing
		}
	}

	updatedState, err := m.keyManager.Ensure(ctx, state, state.AuthorizationToken, state.ClientIdentifier)
	if err != nil {
		return "", err
	}

	token, expiresAt, err := m.plexClient.ExchangeToken(ctx, updatedState.ClientIdentifier, updatedState.AuthorizationToken, updatedState.KeyID)
	if err != nil {
		return "", err
	}
	updatedState.Token = token
	updatedState.TokenExpiresAt = expiresAt

	if err := m.store.Save(updatedState); err != nil {
		return "", err
	}
	m.state = updatedState
	return updatedState.Token, nil
}

// SetAuthorizationToken stores the long-lived authorization token from Plex link flow.
func (m *TokenManager) SetAuthorizationToken(token string) error {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return errors.New("authorization token is empty")
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	updated := m.state
	updated.AuthorizationToken = trimmed
	updated.KeyID = ""
	updated.KeyExpiresAt = time.Time{}
	updated.Token = ""
	updated.TokenExpiresAt = time.Time{}

	if err := m.store.Save(updated); err != nil {
		return err
	}
	m.state = updated
	return nil
}

// RequestPin starts the Plex device linking flow.
func (m *TokenManager) RequestPin(ctx context.Context) (*Pin, error) {
	m.stateMu.RLock()
	clientID := m.state.ClientIdentifier
	m.stateMu.RUnlock()

	return m.plexClient.RequestPin(ctx, clientID)
}

// PollPin checks whether the user has approved the Plex link code.
func (m *TokenManager) PollPin(ctx context.Context, id int64) (*PinStatus, error) {
	m.stateMu.RLock()
	clientID := m.state.ClientIdentifier
	m.stateMu.RUnlock()

	return m.plexClient.PollPin(ctx, clientID, id)
}

type Pin struct {
	ID        int64
	Code      string
	ExpiresAt time.Time
}

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

func (m *TokenManager) reloadLocked() error {
	loaded, err := m.store.Load()
	if err != nil {
		return err
	}
	if loaded.ClientIdentifier == "" {
		loaded.ClientIdentifier = m.state.ClientIdentifier
	}
	m.state = loaded
	return nil
}
