package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// Open initializes or connects to the queue database and applies migrations.
func Open(cfg *config.Config) (*Store, error) {
	if err := cfg.EnsureDirectories(); err != nil {
		return nil, fmt.Errorf("ensure directories: %w", err)
	}

	dbPath := filepath.Join(cfg.LogDir, "queue.db")
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
	if err := store.applyMigrations(context.Background()); err != nil {
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

// NewDisc inserts a new item for an optical disc awaiting identification.
func (s *Store) NewDisc(ctx context.Context, discTitle, fingerprint string) (*Item, error) {
	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339Nano)

	res, err := s.db.ExecContext(
		ctx,
		`INSERT INTO queue_items (
            disc_title, status, created_at, updated_at,
            progress_stage, progress_percent, progress_message, disc_fingerprint
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		discTitle,
		StatusPending,
		timestamp,
		timestamp,
		nil,
		0.0,
		nil,
		nullableString(fingerprint),
	)
	if err != nil {
		return nil, fmt.Errorf("insert disc: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}

	return s.GetByID(ctx, id)
}

// NewFile enqueues a file that skips ripping and begins at the encoding stage.

func (s *Store) NewFile(ctx context.Context, sourcePath string) (*Item, error) {
	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339Nano)

	discTitle := inferTitleFromPath(sourcePath)
	meta := NewBasicMetadata(discTitle, true)
	metadataJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}

	res, err := s.db.ExecContext(
		ctx,
		`INSERT INTO queue_items (
            source_path, disc_title, status, ripped_file, created_at, updated_at,
            progress_stage, progress_percent, progress_message, metadata_json
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sourcePath,
		discTitle,
		StatusRipped,
		sourcePath,
		timestamp,
		timestamp,
		nil,
		0.0,
		nil,
		string(metadataJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("insert file: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}

	return s.GetByID(ctx, id)
}

// GetByID fetches a queue item by identifier.
func (s *Store) GetByID(ctx context.Context, id int64) (*Item, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM queue_items WHERE id = ?`, id)
	item, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get item: %w", err)
	}
	return item, nil
}

// FindByFingerprint returns the first item matching a fingerprint.
func (s *Store) FindByFingerprint(ctx context.Context, fingerprint string) (*Item, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT `+itemColumns+` FROM queue_items WHERE disc_fingerprint = ? ORDER BY id LIMIT 1`,
		fingerprint,
	)
	item, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find by fingerprint: %w", err)
	}
	return item, nil
}

// Update persists changes to an existing queue item.
func (s *Store) Update(ctx context.Context, item *Item) error {
	if item == nil {
		return errors.New("item is nil")
	}
	item.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE queue_items
         SET source_path = ?, disc_title = ?, status = ?, media_info_json = ?,
             ripped_file = ?, encoded_file = ?, final_file = ?, error_message = ?,
             updated_at = ?, progress_stage = ?, progress_percent = ?, progress_message = ?,
             rip_spec_data = ?, disc_fingerprint = ?, metadata_json = ?, last_heartbeat = ?,
             needs_review = ?, review_reason = ?
         WHERE id = ?`,
		nullableString(item.SourcePath),
		nullableString(item.DiscTitle),
		item.Status,
		nullableString(item.MediaInfoJSON),
		nullableString(item.RippedFile),
		nullableString(item.EncodedFile),
		nullableString(item.FinalFile),
		nullableString(item.ErrorMessage),
		item.UpdatedAt.Format(time.RFC3339Nano),
		nullableString(item.ProgressStage),
		item.ProgressPercent,
		nullableString(item.ProgressMessage),
		nullableString(item.RipSpecData),
		nullableString(item.DiscFingerprint),
		nullableString(item.MetadataJSON),
		nullableTime(item.LastHeartbeat),
		boolToInt(item.NeedsReview),
		nullableString(item.ReviewReason),
		item.ID,
	)
	if err != nil {
		return fmt.Errorf("update item: %w", err)
	}
	return nil
}

// ItemsByStatus returns items matching a status ordered by creation time.
func (s *Store) ItemsByStatus(ctx context.Context, status Status) ([]*Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM queue_items WHERE status = ? ORDER BY created_at`, status)
	if err != nil {
		return nil, fmt.Errorf("query by status: %w", err)
	}
	defer rows.Close()

	var items []*Item
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// List returns queue items filtered by status set (or all items when no status is provided).
func (s *Store) List(ctx context.Context, statuses ...Status) ([]*Item, error) {
	var (
		rows *sql.Rows
		err  error
	)

	baseQuery := `SELECT ` + itemColumns + ` FROM queue_items`
	orderClause := ` ORDER BY created_at`

	if len(statuses) == 0 {
		rows, err = s.db.QueryContext(ctx, baseQuery+orderClause)
	} else {
		placeholders := makePlaceholders(len(statuses))
		args := make([]any, len(statuses))
		for i, status := range statuses {
			args[i] = status
		}
		query := baseQuery + ` WHERE status IN (` + placeholders + `)` + orderClause
		rows, err = s.db.QueryContext(ctx, query, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("list queue items: %w", err)
	}
	defer rows.Close()

	var items []*Item
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// NextForStatuses returns the oldest item matching any of the provided statuses.
func (s *Store) NextForStatuses(ctx context.Context, statuses ...Status) (*Item, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := makePlaceholders(len(statuses))
	args := make([]any, len(statuses))
	for i, status := range statuses {
		args[i] = status
	}

	query := `SELECT ` + itemColumns + ` FROM queue_items WHERE status IN (` + placeholders + `) ORDER BY created_at LIMIT 1`
	row := s.db.QueryRowContext(ctx, query, args...)
	item, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

// ResetStuckProcessing resets items in processing states back to pending.
func (s *Store) ResetStuckProcessing(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(
		ctx,
		`UPDATE queue_items
         SET status = ?, progress_stage = 'Reset from stuck processing',
             progress_percent = 0, progress_message = NULL, updated_at = ?
         WHERE status IN (?, ?, ?)`,
		StatusPending,
		time.Now().UTC().Format(time.RFC3339Nano),
		StatusRipping,
		StatusIdentifying,
		StatusEncoding,
	)
	if err != nil {
		return 0, fmt.Errorf("reset stuck items: %w", err)
	}
	return res.RowsAffected()
}

// UpdateHeartbeat updates the last heartbeat timestamp for an in-flight item.
func (s *Store) UpdateHeartbeat(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE queue_items SET last_heartbeat = ?, updated_at = ? WHERE id = ?`,
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		id,
	)
	if err != nil {
		return fmt.Errorf("update heartbeat: %w", err)
	}
	return nil
}

// ReclaimStaleProcessing returns items stuck in processing back to pending when heartbeats expire.
func (s *Store) ReclaimStaleProcessing(ctx context.Context, cutoff time.Time) (int64, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(
		ctx,
		`UPDATE queue_items
        SET status = ?, progress_stage = 'Reclaimed from stale processing',
            progress_percent = 0, progress_message = NULL, last_heartbeat = NULL, updated_at = ?
        WHERE status IN (?, ?, ?, ?) AND last_heartbeat IS NOT NULL AND last_heartbeat < ?`,
		StatusPending,
		now.Format(time.RFC3339Nano),
		StatusIdentifying,
		StatusRipping,
		StatusEncoding,
		StatusOrganizing,
		cutoff.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("reclaim stale items: %w", err)
	}
	return res.RowsAffected()
}

// RetryFailed moves failed items back to pending for reprocessing.
func (s *Store) RetryFailed(ctx context.Context, ids ...int64) (int64, error) {
	if len(ids) == 0 {
		res, err := s.db.ExecContext(
			ctx,
			`UPDATE queue_items
            SET status = ?, progress_stage = 'Retry requested', progress_percent = 0,
                progress_message = NULL, error_message = NULL, updated_at = ?
            WHERE status = ?`,
			StatusPending,
			time.Now().UTC().Format(time.RFC3339Nano),
			StatusFailed,
		)
		if err != nil {
			return 0, fmt.Errorf("retry failed items: %w", err)
		}
		return res.RowsAffected()
	}

	placeholders := makePlaceholders(len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, StatusPending, time.Now().UTC().Format(time.RFC3339Nano))
	for _, id := range ids {
		args = append(args, id)
	}
	query := `UPDATE queue_items
        SET status = ?, progress_stage = 'Retry requested', progress_percent = 0,
            progress_message = NULL, error_message = NULL, updated_at = ?
        WHERE id IN (` + placeholders + `) AND status = '` + string(StatusFailed) + `'`
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("retry selected items: %w", err)
	}
	return res.RowsAffected()
}

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
		case StatusReview:
			health.Review += count
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

		expected := []string{"id", "source_path", "disc_title", "status", "media_info_json", "ripped_file", "encoded_file", "final_file", "error_message", "created_at", "updated_at", "progress_stage", "progress_percent", "progress_message", "rip_spec_data", "disc_fingerprint", "metadata_json", "last_heartbeat"}
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

// Remove deletes an item by identifier.
func (s *Store) Remove(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM queue_items WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete item: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return affected > 0, nil
}

// ClearCompleted removes only completed items from the queue.
func (s *Store) ClearCompleted(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM queue_items WHERE status = ?`, StatusCompleted)
	if err != nil {
		return 0, fmt.Errorf("clear completed: %w", err)
	}
	return res.RowsAffected()
}

// Clear removes all items from the queue.
func (s *Store) Clear(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM queue_items`)
	if err != nil {
		return 0, fmt.Errorf("clear queue: %w", err)
	}
	return res.RowsAffected()
}

// ClearFailed removes only failed items from the queue.
func (s *Store) ClearFailed(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM queue_items WHERE status = ?`, StatusFailed)
	if err != nil {
		return 0, fmt.Errorf("clear failed: %w", err)
	}
	return res.RowsAffected()
}

const itemColumns = "id, source_path, disc_title, status, media_info_json, ripped_file, encoded_file, final_file, error_message, created_at, updated_at, progress_stage, progress_percent, progress_message, rip_spec_data, disc_fingerprint, metadata_json, last_heartbeat, needs_review, review_reason"

func scanItem(scanner interface{ Scan(dest ...any) error }) (*Item, error) {
	var (
		id               int64
		sourcePath       sql.NullString
		discTitle        sql.NullString
		statusStr        string
		mediaInfo        sql.NullString
		rippedFile       sql.NullString
		encodedFile      sql.NullString
		finalFile        sql.NullString
		errorMessage     sql.NullString
		createdRaw       sql.NullString
		updatedRaw       sql.NullString
		progressStage    sql.NullString
		progressPercent  sql.NullFloat64
		progressMessage  sql.NullString
		ripSpec          sql.NullString
		fingerprint      sql.NullString
		metadata         sql.NullString
		lastHeartbeatRaw sql.NullString
		needsReview      sql.NullInt64
		reviewReason     sql.NullString
	)

	if err := scanner.Scan(
		&id,
		&sourcePath,
		&discTitle,
		&statusStr,
		&mediaInfo,
		&rippedFile,
		&encodedFile,
		&finalFile,
		&errorMessage,
		&createdRaw,
		&updatedRaw,
		&progressStage,
		&progressPercent,
		&progressMessage,
		&ripSpec,
		&fingerprint,
		&metadata,
		&lastHeartbeatRaw,
		&needsReview,
		&reviewReason,
	); err != nil {
		return nil, err
	}

	item := &Item{
		ID:              id,
		SourcePath:      sourcePath.String,
		DiscTitle:       discTitle.String,
		Status:          Status(statusStr),
		MediaInfoJSON:   mediaInfo.String,
		RippedFile:      rippedFile.String,
		EncodedFile:     encodedFile.String,
		FinalFile:       finalFile.String,
		ErrorMessage:    errorMessage.String,
		ProgressStage:   progressStage.String,
		ProgressPercent: progressPercent.Float64,
		ProgressMessage: progressMessage.String,
		RipSpecData:     ripSpec.String,
		DiscFingerprint: fingerprint.String,
		MetadataJSON:    metadata.String,
	}
	if needsReview.Valid {
		item.NeedsReview = needsReview.Int64 != 0
	}
	item.ReviewReason = reviewReason.String

	if created, err := parseTimeString(createdRaw.String); err == nil {
		item.CreatedAt = created
	}
	if updated, err := parseTimeString(updatedRaw.String); err == nil {
		item.UpdatedAt = updated
	}
	if lastHeartbeatRaw.Valid {
		if heartbeat, err := parseTimeString(lastHeartbeatRaw.String); err == nil {
			item.LastHeartbeat = &heartbeat
		}
	}
	return item, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	v := value.UTC().Format(time.RFC3339Nano)
	return v
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func parseTimeString(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("empty")
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}

func makePlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	placeholders := make([]byte, 0, count*2)
	for i := 0; i < count; i++ {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
	}
	return string(placeholders)
}

func inferTitleFromPath(path string) string {
	base := strings.TrimSpace(filepath.Base(path))
	if base == "" {
		return "Manual Import"
	}
	ext := filepath.Ext(base)
	base = strings.TrimSuffix(base, ext)
	cleaned := strings.TrimSpace(base)
	if cleaned == "" {
		return "Manual Import"
	}
	return cleaned
}
