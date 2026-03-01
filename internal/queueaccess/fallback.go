package queueaccess

import (
	"fmt"

	"spindle/internal/ipc"
	"spindle/internal/queue"
)

// Session represents a queue access handle and its cleanup function.
type Session struct {
	Access Access
	close  func() error
}

// Close releases resources associated with the session.
func (s Session) Close() error {
	if s.close == nil {
		return nil
	}
	return s.close()
}

// OpenWithFallback tries IPC-backed access first, then falls back to direct store access.
func OpenWithFallback(
	dial func() (*ipc.Client, error),
	openStore func() (*queue.Store, error),
) (Session, error) {
	if dial != nil {
		if client, err := dial(); err == nil {
			return Session{
				Access: NewIPCAccess(client),
				close:  client.Close,
			}, nil
		}
	}

	if openStore == nil {
		return Session{}, fmt.Errorf("open queue store: no store opener configured")
	}
	store, err := openStore()
	if err != nil {
		return Session{}, fmt.Errorf("open queue store: %w", err)
	}
	return Session{
		Access: NewStoreAccess(store),
		close:  store.Close,
	}, nil
}
