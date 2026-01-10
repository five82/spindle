package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Stats returns a count of items grouped by status.
func (s *Store) Stats(ctx context.Context) (map[Status]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(1) FROM queue_items GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("queue stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[Status]int)
	for rows.Next() {
		var status Status
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		stats[status] = count
	}
	return stats, rows.Err()
}

// Health aggregates queue state for diagnostic output.
func (s *Store) Health(ctx context.Context) (HealthSummary, error) {
	stats, err := s.Stats(ctx)
	if err != nil {
		return HealthSummary{}, err
	}
	health := HealthSummary{}
	for status, count := range stats {
		health.Total += count
		switch status {
		case StatusPending:
			health.Pending += count
		case StatusFailed:
			health.Failed += count
		case StatusCompleted:
			health.Completed += count
		default:
			if _, ok := processingStatuses[status]; ok {
				health.Processing += count
			}
		}
	}
	return health, nil
}

// CheckHealth returns diagnostic information about the queue database.
func (s *Store) CheckHealth(ctx context.Context) (DatabaseHealth, error) {
	health := DatabaseHealth{
		DBPath:        s.path,
		SchemaVersion: "current",
	}

	if s.path == "" {
		return health, errors.New("queue database path is unknown")
	}

	info, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			health.DatabaseExists = false
			return health, nil
		}
		return health, fmt.Errorf("stat queue database: %w", err)
	}
	if info.IsDir() {
		return health, fmt.Errorf("queue database path %q is a directory", s.path)
	}
	health.DatabaseExists = true

	if s.db == nil {
		return health, errors.New("queue database connection unavailable")
	}

	connCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := s.db.PingContext(connCtx); err != nil {
		health.Error = err.Error()
		return health, fmt.Errorf("ping queue database: %w", err)
	}
	health.DatabaseReadable = true

	var tableName string
	row := s.db.QueryRowContext(connCtx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'queue_items'")
	if err := row.Scan(&tableName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			health.TableExists = false
		} else {
			health.Error = err.Error()
			return health, fmt.Errorf("query table info: %w", err)
		}
	} else {
		health.TableExists = true
	}

	if health.TableExists {
		colsRows, err := s.db.QueryContext(connCtx, "PRAGMA table_info(queue_items)")
		if err != nil {
			health.Error = err.Error()
			return health, fmt.Errorf("table info: %w", err)
		}
		defer colsRows.Close()

		var columns []string
		for colsRows.Next() {
			var (
				cid     int
				name    string
				typeStr string
				notNull int
				dflt    any
				pk      int
			)
			if err := colsRows.Scan(&cid, &name, &typeStr, &notNull, &dflt, &pk); err != nil {
				health.Error = err.Error()
				return health, fmt.Errorf("scan table info: %w", err)
			}
			columns = append(columns, name)
		}
		if err := colsRows.Err(); err != nil {
			health.Error = err.Error()
			return health, fmt.Errorf("iterate table info: %w", err)
		}
		health.ColumnsPresent = append(health.ColumnsPresent, columns...)

		expected := []string{
			"id",
			"source_path",
			"disc_title",
			"status",
			"media_info_json",
			"ripped_file",
			"encoded_file",
			"final_file",
			"item_log_path",
			"active_episode_key",
			"error_message",
			"created_at",
			"updated_at",
			"progress_stage",
			"progress_percent",
			"progress_message",
			"progress_bytes_copied",
			"progress_total_bytes",
			"encoding_details_json",
			"drapto_preset_profile",
			"rip_spec_data",
			"disc_fingerprint",
			"metadata_json",
			"last_heartbeat",
			"needs_review",
			"review_reason",
		}
		missingMap := make(map[string]struct{}, len(expected))
		for _, col := range expected {
			missingMap[col] = struct{}{}
		}
		for _, col := range columns {
			delete(missingMap, col)
		}
		for col := range missingMap {
			health.MissingColumns = append(health.MissingColumns, col)
		}

		row = s.db.QueryRowContext(connCtx, "SELECT COUNT(*) FROM queue_items")
		if err := row.Scan(&health.TotalItems); err != nil {
			health.Error = err.Error()
			return health, fmt.Errorf("count queue items: %w", err)
		}
	}

	row = s.db.QueryRowContext(connCtx, "PRAGMA integrity_check")
	var integrityResult string
	if err := row.Scan(&integrityResult); err != nil {
		health.Error = err.Error()
		return health, fmt.Errorf("integrity check: %w", err)
	}
	health.IntegrityCheck = strings.EqualFold(integrityResult, "ok")

	return health, nil
}
