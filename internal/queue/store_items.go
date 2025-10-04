package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// NewDisc inserts a new item for an optical disc awaiting identification.
func (s *Store) NewDisc(ctx context.Context, discTitle, fingerprint string) (*Item, error) {
	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339Nano)

	res, err := s.execWithRetry(
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

	res, err := s.execWithRetry(
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
	if err := s.execWithoutResultRetry(
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
	); err != nil {
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

// Remove deletes an item by identifier.
func (s *Store) Remove(ctx context.Context, id int64) (bool, error) {
	res, err := s.execWithRetry(ctx, `DELETE FROM queue_items WHERE id = ?`, id)
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
	res, err := s.execWithRetry(ctx, `DELETE FROM queue_items WHERE status = ?`, StatusCompleted)
	if err != nil {
		return 0, fmt.Errorf("clear completed: %w", err)
	}
	return res.RowsAffected()
}

// Clear removes all items from the queue.
func (s *Store) Clear(ctx context.Context) (int64, error) {
	res, err := s.execWithRetry(ctx, `DELETE FROM queue_items`)
	if err != nil {
		return 0, fmt.Errorf("clear queue: %w", err)
	}
	return res.RowsAffected()
}

// ClearFailed removes only failed items from the queue.
func (s *Store) ClearFailed(ctx context.Context) (int64, error) {
	res, err := s.execWithRetry(ctx, `DELETE FROM queue_items WHERE status = ?`, StatusFailed)
	if err != nil {
		return 0, fmt.Errorf("clear failed: %w", err)
	}
	return res.RowsAffected()
}
