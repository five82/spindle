package plex

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFileTokenStoreLoadMissingFile(t *testing.T) {
	store := NewFileTokenStore(filepath.Join(t.TempDir(), "missing.json"))

	state, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if state != (tokenState{}) {
		t.Fatalf("expected zero state, got %#v", state)
	}
}

func TestFileTokenStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewFileTokenStore(path)

	expected := tokenState{
		AuthorizationToken: "auth",
		ClientIdentifier:   "client",
		PrivateKey:         "priv",
		PublicKey:          "pub",
		KeyID:              "key",
		KeyExpiresAt:       time.Now().Add(time.Hour).Round(time.Second),
		Token:              "token",
		TokenExpiresAt:     time.Now().Add(2 * time.Hour).Round(time.Second),
	}

	if err := store.Save(expected); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.AuthorizationToken != expected.AuthorizationToken {
		t.Fatalf("authorization mismatch: got %q want %q", got.AuthorizationToken, expected.AuthorizationToken)
	}
	if got.ClientIdentifier != expected.ClientIdentifier {
		t.Fatalf("client id mismatch: got %q want %q", got.ClientIdentifier, expected.ClientIdentifier)
	}
	if got.PrivateKey != expected.PrivateKey || got.PublicKey != expected.PublicKey {
		t.Fatalf("key material mismatch: got %#v want %#v", got, expected)
	}
	if got.KeyID != expected.KeyID {
		t.Fatalf("key id mismatch: got %q want %q", got.KeyID, expected.KeyID)
	}
	if !got.KeyExpiresAt.Equal(expected.KeyExpiresAt) {
		t.Fatalf("key expiry mismatch: got %v want %v", got.KeyExpiresAt, expected.KeyExpiresAt)
	}
	if got.Token != expected.Token {
		t.Fatalf("token mismatch: got %q want %q", got.Token, expected.Token)
	}
	if !got.TokenExpiresAt.Equal(expected.TokenExpiresAt) {
		t.Fatalf("token expiry mismatch: got %v want %v", got.TokenExpiresAt, expected.TokenExpiresAt)
	}
}
