package plex

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultKeyManagerGeneratesAndRegistersKey(t *testing.T) {
	expires := time.Now().Add(48 * time.Hour).Round(time.Second)
	client := &stubPlexClient{
		registerKeyResult: stubRegisterResult{
			keyID:   "new-key",
			expires: expires,
		},
	}

	manager := NewDefaultKeyManager(client)
	state := tokenState{AuthorizationToken: "auth-token", ClientIdentifier: "client"}

	updated, err := manager.Ensure(context.Background(), state, "auth-token", "client")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if updated.PrivateKey == "" || updated.PublicKey == "" {
		t.Fatalf("expected generated keys: %#v", updated)
	}
	if updated.KeyID != "new-key" {
		t.Fatalf("unexpected key id %q", updated.KeyID)
	}
	if !updated.KeyExpiresAt.Equal(expires) {
		t.Fatalf("unexpected key expiry %v", updated.KeyExpiresAt)
	}
	if !client.registerCalled {
		t.Fatalf("expected register key call")
	}
	if client.registerAuth != "auth-token" {
		t.Fatalf("authorization mismatch: %q", client.registerAuth)
	}
	if client.registerClient != "client" {
		t.Fatalf("client id mismatch: %q", client.registerClient)
	}
	if client.registerPublic == "" {
		t.Fatalf("missing public key transmission")
	}
}

func TestDefaultKeyManagerSkipsValidKey(t *testing.T) {
	client := &stubPlexClient{
		registerKeyResult: stubRegisterResult{err: errors.New("should not be called")},
	}
	manager := NewDefaultKeyManager(client)

	state := tokenState{
		AuthorizationToken: "auth",
		ClientIdentifier:   "client",
		PrivateKey:         "priv",
		PublicKey:          "pub",
		KeyID:              "existing",
		KeyExpiresAt:       time.Now().Add(48 * time.Hour),
	}

	updated, err := manager.Ensure(context.Background(), state, "auth", "client")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if updated != state {
		t.Fatalf("state changed unexpectedly: got %#v want %#v", updated, state)
	}
	if client.registerCalled {
		t.Fatalf("register called unexpectedly")
	}
}

func TestDefaultKeyManagerRequiresAuthorization(t *testing.T) {
	manager := NewDefaultKeyManager(&stubPlexClient{})
	state := tokenState{}

	_, err := manager.Ensure(context.Background(), state, "", "client")
	if !errors.Is(err, ErrAuthorizationMissing) {
		t.Fatalf("expected ErrAuthorizationMissing, got %v", err)
	}
}

type stubRegisterResult struct {
	keyID   string
	expires time.Time
	err     error
}

type stubPlexClient struct {
	registerCalled    bool
	registerAuth      string
	registerClient    string
	registerPublic    string
	registerKeyResult stubRegisterResult
}

func (s *stubPlexClient) RequestPin(context.Context, string) (*Pin, error) {
	return nil, errors.New("not implemented")
}

func (s *stubPlexClient) PollPin(context.Context, string, int64) (*PinStatus, error) {
	return nil, errors.New("not implemented")
}

func (s *stubPlexClient) RegisterKey(_ context.Context, clientIdentifier, authorizationToken, publicKey string) (string, time.Time, error) {
	s.registerCalled = true
	s.registerClient = clientIdentifier
	s.registerAuth = authorizationToken
	s.registerPublic = publicKey
	return s.registerKeyResult.keyID, s.registerKeyResult.expires, s.registerKeyResult.err
}

func (s *stubPlexClient) ExchangeToken(context.Context, string, string, string) (string, time.Time, error) {
	return "", time.Time{}, errors.New("not implemented")
}
