package plex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TokenStore abstracts persistence for Plex authentication state.
type TokenStore interface {
	Load() (tokenState, error)
	Save(tokenState) error
}

// FileTokenStore writes token state to a JSON file on disk.
type FileTokenStore struct {
	path string
}

// NewFileTokenStore builds a FileTokenStore rooted at the provided path.
func NewFileTokenStore(path string) *FileTokenStore {
	return &FileTokenStore{path: path}
}

// Load reads token state from disk. A missing file resolves to an empty state.
func (s *FileTokenStore) Load() (tokenState, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return tokenState{}, nil
		}
		return tokenState{}, fmt.Errorf("read plex auth state: %w", err)
	}

	var state tokenState
	if err := json.Unmarshal(data, &state); err != nil {
		return tokenState{}, fmt.Errorf("decode plex auth state: %w", err)
	}
	return state, nil
}

// Save persists token state to disk with restricted permissions.
func (s *FileTokenStore) Save(state tokenState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("ensure auth state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plex auth state: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write plex auth state: %w", err)
	}
	return nil
}
