package queue

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		versions = append(versions, entry.Name())
	}
	sort.Strings(versions)

	migrations := make([]migration, 0, len(versions))
	for _, name := range versions {
		data, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		version := strings.TrimSuffix(name, ".sql")
		migrations = append(migrations, migration{version: version, sql: string(data)})
	}
	return migrations, nil
}

func (s *Store) applyMigrations(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)"); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	for _, migration := range migrations {
		var count int
		row := tx.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = ?", migration.version)
		if err := row.Scan(&count); err != nil {
			return fmt.Errorf("scan migration version: %w", err)
		}
		if count > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", migration.version); err != nil {
			return fmt.Errorf("record migration %s: %w", migration.version, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}
