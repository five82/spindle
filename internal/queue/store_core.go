package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"spindle/internal/config"
)

// Store manages queue persistence backed by SQLite.
type Store struct {
	db   *sql.DB
	path string
}

const (
	sqliteBusyCode          = 5
	busyRetryAttempts       = 5
	busyRetryInitialBackoff = 10 * time.Millisecond
	busyRetryMaxBackoff     = 200 * time.Millisecond
)

func ensureContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	var coder interface{ Code() int }
	if errors.As(err, &coder) && coder.Code() == sqliteBusyCode {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

func retryOnBusy(ctx context.Context, op func() error) error {
	delay := busyRetryInitialBackoff
	var lastErr error
	for attempt := 0; attempt < busyRetryAttempts; attempt++ {
		lastErr = op()
		if lastErr == nil {
			return nil
		}
		if !isSQLiteBusy(lastErr) || attempt == busyRetryAttempts-1 {
			break
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
		if next := delay * 2; next <= busyRetryMaxBackoff {
			delay = next
		}
	}
	return lastErr
}

func (s *Store) execWithRetry(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx = ensureContext(ctx)
	var (
		res     sql.Result
		execErr error
	)
	if err := retryOnBusy(ctx, func() error {
		res, execErr = s.db.ExecContext(ctx, query, args...)
		return execErr
	}); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Store) execWithoutResultRetry(ctx context.Context, query string, args ...any) error {
	ctx = ensureContext(ctx)
	return retryOnBusy(ctx, func() error {
		_, err := s.db.ExecContext(ctx, query, args...)
		return err
	})
}

// Open initializes or connects to the queue database.
func Open(cfg *config.Config) (*Store, error) {
	if err := cfg.EnsureDirectories(); err != nil {
		return nil, fmt.Errorf("ensure directories: %w", err)
	}

	dbPath := filepath.Join(cfg.Paths.LogDir, "queue.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	}
	for _, pragma := range pragmas {
		if _, execErr := db.Exec(pragma); execErr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply pragma %q: %w", pragma, execErr)
		}
	}

	store := &Store{db: db, path: dbPath}
	if err := store.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
