package plex

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// KeyManager controls key generation and registration with Plex.
type KeyManager interface {
	Ensure(ctx context.Context, state tokenState, authorizationToken, clientIdentifier string) (tokenState, error)
}

// DefaultKeyManager generates ed25519 keys and registers them with Plex.
type DefaultKeyManager struct {
	client PlexClient
}

// NewDefaultKeyManager wires a Plex client into a KeyManager.
func NewDefaultKeyManager(client PlexClient) *DefaultKeyManager {
	return &DefaultKeyManager{client: client}
}

// Ensure materializes keys in the provided state and guarantees registration.
func (k *DefaultKeyManager) Ensure(ctx context.Context, state tokenState, authorizationToken, clientIdentifier string) (tokenState, error) {
	if strings.TrimSpace(authorizationToken) == "" {
		return state, ErrAuthorizationMissing
	}
	updated := state

	if updated.PrivateKey == "" || updated.PublicKey == "" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return state, fmt.Errorf("generate ed25519 key: %w", err)
		}
		updated.PrivateKey = base64.StdEncoding.EncodeToString(priv)
		updated.PublicKey = base64.StdEncoding.EncodeToString(pub)
		updated.KeyID = ""
		updated.KeyExpiresAt = time.Time{}
	}

	if updated.KeyID != "" && time.Until(updated.KeyExpiresAt) > keyRefreshLeeway {
		return updated, nil
	}

	keyID, expiresAt, err := k.client.RegisterKey(ctx, clientIdentifier, authorizationToken, updated.PublicKey)
	if err != nil {
		return state, err
	}
	updated.KeyID = keyID
	updated.KeyExpiresAt = expiresAt
	return updated, nil
}
