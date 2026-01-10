package queue

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
)

//go:embed schema.sql
var schemaSQL string

// schemaVersion is the current schema version. Bump this when the schema changes.
// Users will need to clear their queue database after schema changes.
const schemaVersion = 1

// ErrSchemaMismatch indicates the database schema version doesn't match the expected version.
var ErrSchemaMismatch = errors.New("schema version mismatch")

func (s *Store) initSchema(ctx context.Context) error {
	// Check if schema_version table exists (indicates an initialized database)
	var tableExists int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='schema_version'",
	).Scan(&tableExists)
	if err != nil {
		return fmt.Errorf("check schema_version table: %w", err)
	}

	if tableExists == 0 {
		// New database: create schema
		return s.createSchema(ctx)
	}

	// Existing database: verify version
	var version int
	err = s.db.QueryRowContext(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if version != schemaVersion {
		return fmt.Errorf("%w: database has version %d, expected %d (run 'spindle queue clear' or delete the database)",
			ErrSchemaMismatch, version, schemaVersion)
	}

	return nil
}

func (s *Store) createSchema(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (?)", schemaVersion); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema: %w", err)
	}
	return nil
}
