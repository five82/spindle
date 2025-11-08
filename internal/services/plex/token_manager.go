package plex

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	defaultBaseURL        = "https://clients.plex.tv"
	stateFileName         = "plex_auth.json"
	tokenRefreshLeeway    = time.Hour
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

// TokenManager persists Plex authentication state and refreshes the 7-day JWT.
type TokenManager struct {
	cfg *config.Config

	httpClient HTTPDoer
	baseURL    string
	store      TokenStore
	plexClient PlexClient

	stateMu sync.RWMutex
	state   tokenState
}

type tokenState struct {
	AuthorizationToken string    `json:"authorization_token"`
	ClientIdentifier   string    `json:"client_identifier"`
	PrivateKey         string    `json:"private_key"`
	PublicKey          string    `json:"public_key"`
	KeyID              string    `json:"key_id"`
	Token              string    `json:"token"`
	TokenExpiresAt     time.Time `json:"token_expires_at"`
}

// NewTokenManager builds a TokenManager using the provided configuration.
func NewTokenManager(cfg *config.Config, opts ...TokenManagerOption) (*TokenManager, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	statePath := strings.TrimSpace(cfg.PlexAuthPath)
	if statePath == "" {
		statePath = filepath.Join(cfg.LogDir, stateFileName)
	}
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
	material, cached, err := m.prepareRefresh()
	if err != nil {
		return "", err
	}
	if cached != "" {
		return cached, nil
	}

	nonce, err := m.plexClient.RequestNonce(ctx, material.clientID)
	if err != nil {
		return "", err
	}

	claims := deviceJWTClaims{
		Audience: "plex.tv",
		Issuer:   material.clientID,
		Nonce:    nonce,
		TTL:      5 * time.Minute,
	}
	deviceJWT, err := signDeviceJWT(material.privateKey, material.keyID, claims)
	if err != nil {
		return "", err
	}

	token, expiresAt, err := m.plexClient.ExchangeToken(ctx, material.clientID, material.authorizationToken, deviceJWT)
	if err != nil {
		return "", err
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(7 * 24 * time.Hour)
	}
	return m.persistRefreshedToken(token, expiresAt)
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
	updated.Token = trimmed
	updated.TokenExpiresAt = time.Now().Add(7 * 24 * time.Hour)

	if err := m.store.Save(updated); err != nil {
		return err
	}
	m.state = updated
	return nil
}

// RequestPin starts the Plex device linking flow.
func (m *TokenManager) RequestPin(ctx context.Context) (*Pin, error) {
	m.stateMu.Lock()
	if err := m.ensureKeyMaterialLocked(); err != nil {
		m.stateMu.Unlock()
		return nil, err
	}
	state := m.state
	m.stateMu.Unlock()

	jwk, err := buildDeviceJWK(state.PublicKey, state.KeyID)
	if err != nil {
		return nil, err
	}
	req := PinRequest{
		JWK:    jwk,
		Strong: true,
	}
	pin, err := m.plexClient.RequestPin(ctx, state.ClientIdentifier, req)
	if err != nil {
		return nil, err
	}
	if pin != nil {
		pin.AuthURL = buildAuthURL(state.ClientIdentifier, pin.Code)
	}
	return pin, nil
}

// PollPin checks whether the user has approved the Plex link code.
func (m *TokenManager) PollPin(ctx context.Context, id int64) (*PinStatus, error) {
	m.stateMu.Lock()
	if err := m.ensureKeyMaterialLocked(); err != nil {
		m.stateMu.Unlock()
		return nil, err
	}
	state := m.state
	m.stateMu.Unlock()

	claims := deviceJWTClaims{
		Audience: "plex.tv",
		Issuer:   state.ClientIdentifier,
		TTL:      5 * time.Minute,
	}
	deviceJWT, err := signDeviceJWT(state.PrivateKey, state.KeyID, claims)
	if err != nil {
		return nil, err
	}
	return m.plexClient.PollPin(ctx, state.ClientIdentifier, id, deviceJWT)
}

type Pin struct {
	ID        int64
	Code      string
	ExpiresAt time.Time
	AuthURL   string
}

type PinStatus struct {
	Authorized         bool
	AuthorizationToken string
	ExpiresAt          time.Time
}

type refreshMaterial struct {
	clientID           string
	authorizationToken string
	privateKey         string
	keyID              string
}

type deviceJWTClaims struct {
	Audience string
	Issuer   string
	Nonce    string
	Scope    []string
	TTL      time.Duration
}

func (m *TokenManager) prepareRefresh() (refreshMaterial, string, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	if m.state.Token != "" && time.Until(m.state.TokenExpiresAt) > tokenRefreshLeeway {
		return refreshMaterial{}, m.state.Token, nil
	}

	if err := m.reloadLocked(); err != nil {
		return refreshMaterial{}, "", err
	}
	if m.state.Token != "" && time.Until(m.state.TokenExpiresAt) > tokenRefreshLeeway {
		return refreshMaterial{}, m.state.Token, nil
	}

	if err := m.ensureKeyMaterialLocked(); err != nil {
		return refreshMaterial{}, "", err
	}

	authToken := strings.TrimSpace(m.state.AuthorizationToken)
	if authToken == "" {
		return refreshMaterial{}, "", ErrAuthorizationMissing
	}

	material := refreshMaterial{
		clientID:           m.state.ClientIdentifier,
		authorizationToken: authToken,
		privateKey:         m.state.PrivateKey,
		keyID:              m.state.KeyID,
	}
	return material, "", nil
}

func (m *TokenManager) persistRefreshedToken(token string, expiresAt time.Time) (string, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return "", errors.New("plex auth: refreshed token is empty")
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(7 * 24 * time.Hour)
	}

	m.state.AuthorizationToken = trimmed
	m.state.Token = trimmed
	m.state.TokenExpiresAt = expiresAt

	if err := m.store.Save(m.state); err != nil {
		return "", err
	}
	return m.state.Token, nil
}

func (m *TokenManager) ensureKeyMaterialLocked() error {
	state := m.state
	dirty := false

	if strings.TrimSpace(state.PrivateKey) == "" || strings.TrimSpace(state.PublicKey) == "" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("generate ed25519 key: %w", err)
		}
		state.PrivateKey = base64.StdEncoding.EncodeToString(priv)
		state.PublicKey = base64.StdEncoding.EncodeToString(pub)
		dirty = true
	}

	if strings.TrimSpace(state.KeyID) == "" {
		pubBytes, err := base64.StdEncoding.DecodeString(state.PublicKey)
		if err != nil {
			return fmt.Errorf("decode stored public key: %w", err)
		}
		hash := sha256.Sum256(pubBytes)
		state.KeyID = base64.RawURLEncoding.EncodeToString(hash[:])
		dirty = true
	}

	if dirty {
		m.state = state
		if err := m.store.Save(m.state); err != nil {
			return err
		}
	} else {
		m.state = state
	}
	return nil
}

func buildDeviceJWK(publicKeyB64, keyID string) (DeviceJWK, error) {
	pubBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyB64))
	if err != nil {
		return DeviceJWK{}, fmt.Errorf("decode stored public key: %w", err)
	}
	return DeviceJWK{
		Kty: "OKP",
		Crv: "Ed25519",
		X:   base64.RawURLEncoding.EncodeToString(pubBytes),
		Use: "sig",
		Alg: "EdDSA",
		Kid: strings.TrimSpace(keyID),
	}, nil
}

func signDeviceJWT(privateKeyB64, keyID string, claims deviceJWTClaims) (string, error) {
	privBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKeyB64))
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("invalid ed25519 private key length: %d", len(privBytes))
	}

	headers := map[string]any{
		"alg": "EdDSA",
		"typ": "JWT",
	}
	if kid := strings.TrimSpace(keyID); kid != "" {
		headers["kid"] = kid
	}

	audience := strings.TrimSpace(claims.Audience)
	if audience == "" {
		audience = "plex.tv"
	}
	issuer := strings.TrimSpace(claims.Issuer)
	if issuer == "" {
		return "", errors.New("plex auth: device JWT issuer is empty")
	}

	now := time.Now().UTC()
	ttl := claims.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	payload := map[string]any{
		"aud": audience,
		"iss": issuer,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
	if claims.Nonce != "" {
		payload["nonce"] = claims.Nonce
	}
	if len(claims.Scope) > 0 {
		payload["scope"] = strings.Join(claims.Scope, ",")
	}

	headJSON, err := json.Marshal(headers)
	if err != nil {
		return "", fmt.Errorf("encode jwt header: %w", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode jwt payload: %w", err)
	}

	headSegment := base64.RawURLEncoding.EncodeToString(headJSON)
	payloadSegment := base64.RawURLEncoding.EncodeToString(payloadJSON)
	message := headSegment + "." + payloadSegment

	signature := ed25519.Sign(ed25519.PrivateKey(privBytes), []byte(message))
	sigSegment := base64.RawURLEncoding.EncodeToString(signature)

	return message + "." + sigSegment, nil
}

func buildAuthURL(clientID, code string) string {
	clientID = strings.TrimSpace(clientID)
	code = strings.TrimSpace(code)
	if clientID == "" || code == "" {
		return ""
	}
	values := url.Values{}
	values.Set("clientID", clientID)
	values.Set("code", code)
	values.Set("context[device][product]", managedProductName)
	return "https://app.plex.tv/auth#?" + values.Encode()
}

type pinResponse struct {
	ID        int64   `json:"id"`
	Code      string  `json:"code"`
	AuthToken string  `json:"authToken"`
	JWTToken  string  `json:"auth_token"`
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
