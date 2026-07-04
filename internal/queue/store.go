package queue

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver.
)

const createTableSQL = `
CREATE TABLE IF NOT EXISTS queue_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    disc_title TEXT,
    stage TEXT NOT NULL,
    in_progress INTEGER NOT NULL DEFAULT 0,
    failed_at_stage TEXT,
    error_message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    rip_spec_data TEXT,
    disc_fingerprint TEXT,
    metadata_json TEXT,
    needs_review INTEGER NOT NULL DEFAULT 0,
    review_reason TEXT,
    progress_stage TEXT,
    progress_percent REAL DEFAULT 0.0,
    progress_message TEXT,
    active_episode_key TEXT,
    progress_bytes_copied INTEGER DEFAULT 0,
    progress_total_bytes INTEGER DEFAULT 0,
    encoding_details_json TEXT,
    user_stopped INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_queue_stage ON queue_items(stage);
CREATE INDEX IF NOT EXISTS idx_queue_fingerprint ON queue_items(disc_fingerprint);
`

// Store provides SQLite-backed queue operations.
type Store struct {
	db *sql.DB
}

// Open opens a read-write SQLite database at path with WAL, foreign keys,
// and busy timeout pragmas. Creates the queue table if it does not exist.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open queue db: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set pragma %q: %w", p, err)
		}
	}

	if _, err := db.Exec(createTableSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create queue table: %w", err)
	}

	if _, err := db.Exec(createTasksTableSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create tasks table: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// retryOnBusy retries fn with exponential backoff when SQLite returns BUSY.
// 5 attempts, 10ms initial delay, 200ms max delay, doubling per attempt.
func retryOnBusy(fn func() error) error {
	const (
		maxAttempts = 5
		initialWait = 10 * time.Millisecond
		maxWait     = 200 * time.Millisecond
	)

	wait := initialWait
	for attempt := range maxAttempts {
		err := fn()
		if err == nil {
			return nil
		}
		if !isBusyError(err) {
			return err
		}
		if attempt == maxAttempts-1 {
			return fmt.Errorf("database busy after %d attempts: %w", maxAttempts, err)
		}
		time.Sleep(wait)
		wait *= 2
		if wait > maxWait {
			wait = maxWait
		}
	}
	return nil // unreachable
}

// isBusyError checks if an error indicates SQLite BUSY.
func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "(5)")
}

// allColumns is the column list for SELECT queries.
const allColumns = `id, disc_title, stage, in_progress, failed_at_stage, error_message,
    created_at, updated_at, rip_spec_data, disc_fingerprint, metadata_json,
    needs_review, review_reason, progress_stage, progress_percent, progress_message,
    active_episode_key, progress_bytes_copied, progress_total_bytes, encoding_details_json,
    user_stopped`

// scanItem scans a row into an Item.
func scanItem(row interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var discTitle, failedAtStage, errorMessage sql.NullString
	var createdAt, updatedAt sql.NullString
	var ripSpecData, discFingerprint, metadataJSON sql.NullString
	var reviewReason, progressStage, progressMessage sql.NullString
	var activeEpisodeKey, encodingDetailsJSON sql.NullString
	var stage string

	err := row.Scan(
		&it.ID, &discTitle, &stage, &it.InProgress,
		&failedAtStage, &errorMessage,
		&createdAt, &updatedAt,
		&ripSpecData, &discFingerprint, &metadataJSON,
		&it.NeedsReview, &reviewReason,
		&progressStage, &it.ProgressPercent, &progressMessage,
		&activeEpisodeKey, &it.ProgressBytesCopied, &it.ProgressTotalBytes,
		&encodingDetailsJSON, &it.userStopped,
	)
	if err != nil {
		return nil, err
	}

	it.Stage = Stage(stage)
	it.DiscTitle = discTitle.String
	it.FailedAtStage = failedAtStage.String
	it.ErrorMessage = errorMessage.String
	it.CreatedAt = createdAt.String
	it.UpdatedAt = updatedAt.String
	it.RipSpecData = ripSpecData.String
	it.DiscFingerprint = discFingerprint.String
	it.MetadataJSON = metadataJSON.String
	it.ReviewReason = reviewReason.String
	it.ProgressStage = progressStage.String
	it.ProgressMessage = progressMessage.String
	it.ActiveEpisodeKey = activeEpisodeKey.String
	it.EncodingDetailsJSON = encodingDetailsJSON.String

	return &it, nil
}

func (s *Store) refreshItem(item *Item) error {
	if item == nil || item.ID == 0 {
		return nil
	}
	fresh, err := s.GetByID(item.ID)
	if err != nil {
		return err
	}
	if fresh != nil {
		*item = *fresh
	}
	return nil
}

// Refresh reloads the item's row from the database into item.
func (s *Store) Refresh(item *Item) error {
	return s.refreshItem(item)
}

// NewDisc inserts a new queue item at the identification stage and returns it with its ID.
func (s *Store) NewDisc(title, fingerprint string) (*Item, error) {
	return s.insertItem(title, fingerprint, StageIdentification, "", "")
}

// NewCachedRip inserts a cached-rip queue item directly at the ripping stage.
func (s *Store) NewCachedRip(title, fingerprint, ripSpecData, metadataJSON string) (*Item, error) {
	return s.insertItem(title, fingerprint, StageRipping, ripSpecData, metadataJSON)
}

func (s *Store) insertItem(title, fingerprint string, stage Stage, ripSpecData, metadataJSON string) (*Item, error) {
	var id int64
	err := retryOnBusy(func() error {
		res, err := s.db.Exec(
			`INSERT INTO queue_items (disc_title, stage, disc_fingerprint, rip_spec_data, metadata_json) VALUES (?, ?, ?, ?, ?)`,
			title, string(stage), fingerprint, ripSpecData, metadataJSON,
		)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("new %s item: %w", stage, err)
	}
	return s.GetByID(id)
}

// GetByID fetches a single item by primary key. Returns nil if not found.
func (s *Store) GetByID(id int64) (*Item, error) {
	row := s.db.QueryRow("SELECT "+allColumns+" FROM queue_items WHERE id = ?", id)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get by id %d: %w", id, err)
	}
	return it, nil
}

// FindByFingerprint finds the first item matching a disc fingerprint.
// Returns nil if not found.
func (s *Store) FindByFingerprint(fp string) (*Item, error) {
	row := s.db.QueryRow(
		"SELECT "+allColumns+" FROM queue_items WHERE disc_fingerprint = ? ORDER BY created_at LIMIT 1",
		fp,
	)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find by fingerprint: %w", err)
	}
	return it, nil
}

// StartStage marks an item as actively processing a stage and resets stale
// progress from the previous stage.
func (s *Store) StartStage(item *Item, stage Stage) error {
	item.InProgress = 1
	item.ProgressStage = string(stage)
	item.ProgressPercent = 0
	item.ProgressMessage = ""
	item.ActiveEpisodeKey = ""
	item.ProgressBytesCopied = 0
	item.ProgressTotalBytes = 0
	return retryOnBusy(func() error {
		_, err := s.db.Exec(`
			UPDATE queue_items SET
				in_progress = 1,
				progress_stage = ?, progress_percent = 0, progress_message = '',
				active_episode_key = '', progress_bytes_copied = 0, progress_total_bytes = 0,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			string(stage), item.ID,
		)
		if err != nil {
			return fmt.Errorf("start %s item %d: %w", stage, item.ID, err)
		}
		return nil
	})
}

// ClearInProgress releases an item's active-processing flag without changing
// stage ownership or work products.
func (s *Store) ClearInProgress(item *Item) error {
	item.InProgress = 0
	return retryOnBusy(func() error {
		_, err := s.db.Exec(`UPDATE queue_items SET in_progress = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, item.ID)
		if err != nil {
			return fmt.Errorf("clear in_progress item %d: %w", item.ID, err)
		}
		return nil
	})
}

// MoveToStage routes an item to a new stage without touching work products.
func (s *Store) MoveToStage(item *Item, stage Stage) error {
	item.Stage = stage
	item.InProgress = 0
	// The item's position changed out from under the scheduler: drop its
	// task rows so they recompile from the new stage.
	if err := s.DeleteTasks(item.ID); err != nil {
		return err
	}
	return retryOnBusy(func() error {
		_, err := s.db.Exec(`
			UPDATE queue_items SET
				stage = ?, in_progress = 0,
				failed_at_stage = NULL, error_message = NULL,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			string(stage), item.ID,
		)
		if err != nil {
			return fmt.Errorf("move item %d to %s: %w", item.ID, stage, err)
		}
		return nil
	})
}

// CompleteStage finalizes a successful stage execution. If advance is true,
// the item moves to nextStage; otherwise only in_progress is cleared. A
// user-stopped item keeps its failed/stopped state even if a handler finishes
// after the stop request.
func (s *Store) CompleteStage(item *Item, nextStage Stage, advance bool) error {
	if !advance {
		return s.ClearInProgress(item)
	}

	targetStage := nextStage
	progressStage := ""
	progressPercent := 0.0
	progressMessage := ""
	if targetStage == StageCompleted {
		progressStage = string(StageCompleted)
		progressPercent = 100
		progressMessage = "Completed"
	}

	return retryOnBusy(func() error {
		res, err := s.db.Exec(`
			UPDATE queue_items SET
				stage = ?, in_progress = 0, active_episode_key = '',
				progress_stage = ?, progress_percent = ?, progress_message = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND user_stopped = 0`,
			string(targetStage), progressStage, progressPercent, progressMessage, item.ID,
		)
		if err != nil {
			return fmt.Errorf("complete stage item %d: %w", item.ID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("complete stage item %d rows affected: %w", item.ID, err)
		}
		if rows == 0 {
			return s.refreshItem(item)
		}
		item.Stage = targetStage
		item.InProgress = 0
		item.ActiveEpisodeKey = ""
		item.ProgressStage = progressStage
		item.ProgressPercent = progressPercent
		item.ProgressMessage = progressMessage
		item.userStopped = 0
		return nil
	})
}

// FailStage marks an item failed at a specific stage unless the item has
// already been explicitly stopped by the user.
func (s *Store) FailStage(item *Item, failedAt Stage, errMsg string) error {
	return retryOnBusy(func() error {
		res, err := s.db.Exec(`
			UPDATE queue_items SET
				stage = ?, in_progress = 0,
				failed_at_stage = ?, error_message = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND user_stopped = 0`,
			string(StageFailed), string(failedAt), errMsg, item.ID,
		)
		if err != nil {
			return fmt.Errorf("fail item %d at %s: %w", item.ID, failedAt, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("fail item %d at %s rows affected: %w", item.ID, failedAt, err)
		}
		if rows == 0 {
			return s.refreshItem(item)
		}
		item.Stage = StageFailed
		item.InProgress = 0
		item.FailedAtStage = string(failedAt)
		item.ErrorMessage = errMsg
		item.userStopped = 0
		return nil
	})
}

// UpdateDiscTitle changes only the queue item's display title.
func (s *Store) UpdateDiscTitle(item *Item, title string) error {
	item.DiscTitle = title
	return retryOnBusy(func() error {
		_, err := s.db.Exec(`UPDATE queue_items SET disc_title = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, title, item.ID)
		if err != nil {
			return fmt.Errorf("update title item %d: %w", item.ID, err)
		}
		return nil
	})
}

// UpdateWorkState updates queue-visible work products without changing
// lifecycle-owned fields such as stage, in_progress, failed_at_stage, or
// error_message. Stage handlers use this through stage.Session so saving a
// RipSpec or review update cannot accidentally advance, retry, or un-fail an
// item.
func (s *Store) UpdateWorkState(item *Item) error {
	return retryOnBusy(func() error {
		res, err := s.db.Exec(`
			UPDATE queue_items SET
				disc_title = ?,
				updated_at = CURRENT_TIMESTAMP,
				rip_spec_data = ?, disc_fingerprint = ?, metadata_json = ?,
				needs_review = ?, review_reason = ?,
				active_episode_key = ?, encoding_details_json = ?
			WHERE id = ? AND user_stopped = 0`,
			item.DiscTitle,
			item.RipSpecData, item.DiscFingerprint, item.MetadataJSON,
			item.NeedsReview, item.ReviewReason,
			item.ActiveEpisodeKey, item.EncodingDetailsJSON,
			item.ID,
		)
		if err != nil {
			return fmt.Errorf("update work state item %d: %w", item.ID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("update work state item %d rows affected: %w", item.ID, err)
		}
		if rows == 0 {
			return s.refreshItem(item)
		}
		return nil
	})
}

// UpdateProgress updates only progress-related columns.
func (s *Store) UpdateProgress(item *Item) error {
	return retryOnBusy(func() error {
		res, err := s.db.Exec(`
			UPDATE queue_items SET
				progress_stage = ?, progress_percent = ?, progress_message = ?,
				progress_bytes_copied = ?, progress_total_bytes = ?,
				encoding_details_json = ?, active_episode_key = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND user_stopped = 0`,
			item.ProgressStage, item.ProgressPercent, item.ProgressMessage,
			item.ProgressBytesCopied, item.ProgressTotalBytes,
			item.EncodingDetailsJSON, item.ActiveEpisodeKey,
			item.ID,
		)
		if err != nil {
			return fmt.Errorf("update progress item %d: %w", item.ID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("update progress item %d rows affected: %w", item.ID, err)
		}
		if rows == 0 {
			return s.refreshItem(item)
		}
		return nil
	})
}

// SetStageLabel updates ONLY the display stage label, leaving in_progress
// and progress columns untouched. The scheduler uses it to keep the label
// honest while sibling workers are still running (e.g. flipping ripping ->
// encoding mid-overlap); full stage completion stays with CompleteStage.
func (s *Store) SetStageLabel(item *Item, stage Stage) error {
	return retryOnBusy(func() error {
		res, err := s.db.Exec(`
			UPDATE queue_items SET
				stage = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND user_stopped = 0 AND stage NOT IN (?, ?)`,
			string(stage), item.ID, string(StageFailed), string(StageCompleted),
		)
		if err != nil {
			return fmt.Errorf("set stage label item %d: %w", item.ID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("set stage label item %d rows affected: %w", item.ID, err)
		}
		if rows == 0 {
			return s.refreshItem(item)
		}
		item.Stage = stage
		return nil
	})
}

// UpdateEncodingDetails persists ONLY the encoding telemetry column,
// leaving the shared progress columns untouched. Used while another stage
// owns the progress display (rip-to-encode streaming overlap).
func (s *Store) UpdateEncodingDetails(item *Item) error {
	return retryOnBusy(func() error {
		res, err := s.db.Exec(`
			UPDATE queue_items SET
				encoding_details_json = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND user_stopped = 0`,
			item.EncodingDetailsJSON, item.ID,
		)
		if err != nil {
			return fmt.Errorf("update encoding details item %d: %w", item.ID, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("update encoding details item %d rows affected: %w", item.ID, err)
		}
		if rows == 0 {
			return s.refreshItem(item)
		}
		return nil
	})
}

// Remove deletes a single item by ID.
func (s *Store) Remove(id int64) error {
	return retryOnBusy(func() error {
		if _, err := s.db.Exec("DELETE FROM tasks WHERE item_id = ?", id); err != nil {
			return fmt.Errorf("remove item %d tasks: %w", id, err)
		}
		_, err := s.db.Exec("DELETE FROM queue_items WHERE id = ?", id)
		if err != nil {
			return fmt.Errorf("remove item %d: %w", id, err)
		}
		return nil
	})
}

// Clear deletes all items from the queue. Returns the number removed.
func (s *Store) Clear() (int64, error) {
	var count int64
	err := retryOnBusy(func() error {
		if _, err := s.db.Exec("DELETE FROM tasks"); err != nil {
			return fmt.Errorf("clear tasks: %w", err)
		}
		res, err := s.db.Exec("DELETE FROM queue_items")
		if err != nil {
			return fmt.Errorf("clear queue: %w", err)
		}
		count, _ = res.RowsAffected()
		return nil
	})
	return count, err
}

// ClearCompleted deletes only completed items. Returns the number removed.
func (s *Store) ClearCompleted() (int64, error) {
	var count int64
	err := retryOnBusy(func() error {
		if _, err := s.db.Exec("DELETE FROM tasks WHERE item_id IN (SELECT id FROM queue_items WHERE stage = ?)", string(StageCompleted)); err != nil {
			return fmt.Errorf("clear completed tasks: %w", err)
		}
		res, err := s.db.Exec("DELETE FROM queue_items WHERE stage = ?", string(StageCompleted))
		if err != nil {
			return fmt.Errorf("clear completed: %w", err)
		}
		count, _ = res.RowsAffected()
		return nil
	})
	return count, err
}

// List returns items filtered by stages (or all if none given), ordered by created_at.
func (s *Store) List(statuses ...Stage) ([]*Item, error) {
	var query string
	var args []any

	if len(statuses) == 0 {
		query = "SELECT " + allColumns + " FROM queue_items ORDER BY created_at"
	} else {
		placeholders := make([]string, len(statuses))
		for i, st := range statuses {
			placeholders[i] = "?"
			args = append(args, string(st))
		}
		query = "SELECT " + allColumns + " FROM queue_items WHERE stage IN (" +
			strings.Join(placeholders, ",") + ") ORDER BY created_at"
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return collectItems(rows)
}

// NextForStatuses returns the oldest item with in_progress=0 matching any status.
func (s *Store) NextForStatuses(statuses ...Stage) (*Item, error) {
	if len(statuses) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, st := range statuses {
		placeholders[i] = "?"
		args[i] = string(st)
	}

	query := "SELECT " + allColumns + " FROM queue_items WHERE in_progress = 0 AND stage IN (" +
		strings.Join(placeholders, ",") + ") ORDER BY created_at LIMIT 1"

	row := s.db.QueryRow(query, args...)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("next for statuses: %w", err)
	}
	return it, nil
}

// ActiveFingerprints returns the set of all non-empty disc fingerprints in the queue.
func (s *Store) ActiveFingerprints() (map[string]struct{}, error) {
	rows, err := s.db.Query(
		"SELECT DISTINCT disc_fingerprint FROM queue_items WHERE disc_fingerprint IS NOT NULL AND disc_fingerprint != ''",
	)
	if err != nil {
		return nil, fmt.Errorf("active fingerprints: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]struct{})
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, fmt.Errorf("scan fingerprint: %w", err)
		}
		result[fp] = struct{}{}
	}
	return result, rows.Err()
}

// HasDiscDependentItem returns true if any item is in identification or ripping
// stage with in_progress=1.
func (s *Store) HasDiscDependentItem() (bool, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM queue_items WHERE in_progress = 1 AND stage IN (?, ?)",
		string(StageIdentification), string(StageRipping),
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("has disc dependent item: %w", err)
	}
	return count > 0, nil
}

// Stats returns the count of items grouped by stage.
func (s *Store) Stats() (map[Stage]int, error) {
	rows, err := s.db.Query("SELECT stage, COUNT(*) FROM queue_items GROUP BY stage")
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[Stage]int)
	for rows.Next() {
		var stage string
		var count int
		if err := rows.Scan(&stage, &count); err != nil {
			return nil, fmt.Errorf("scan stats: %w", err)
		}
		result[Stage(stage)] = count
	}
	return result, rows.Err()
}

// InProgressItems returns all items with in_progress=1, ordered by creation time.
func (s *Store) InProgressItems() ([]*Item, error) {
	rows, err := s.db.Query(
		"SELECT " + allColumns + " FROM queue_items WHERE in_progress = 1 ORDER BY created_at",
	)
	if err != nil {
		return nil, fmt.Errorf("in-progress items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return collectItems(rows)
}

// ActiveItemCount returns the number of items in non-terminal stages.
func (s *Store) ActiveItemCount() (int, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM queue_items WHERE stage NOT IN (?, ?)",
		string(StageCompleted), string(StageFailed),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("active item count: %w", err)
	}
	return count, nil
}

// ResetInProgress clears in_progress on all items (startup recovery).
func (s *Store) ResetInProgress() error {
	return retryOnBusy(func() error {
		_, err := s.db.Exec("UPDATE queue_items SET in_progress = 0, updated_at = CURRENT_TIMESTAMP WHERE in_progress = 1")
		if err != nil {
			return fmt.Errorf("reset in progress: %w", err)
		}
		return nil
	})
}

// RetryFailed routes failed items back to their retry point.
// Uses failed_at_stage if set, otherwise falls back to identification.
// Returns the number of items actually retried.
func (s *Store) RetryFailed(ids ...int64) (int, error) {
	if len(ids) == 0 {
		items, err := s.List(StageFailed)
		if err != nil {
			return 0, fmt.Errorf("list failed items: %w", err)
		}
		ids = make([]int64, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		if len(ids) == 0 {
			return 0, nil
		}
	}
	var count int
	err := retryOnBusy(func() error {
		count = 0
		for _, id := range ids {
			item, err := s.GetByID(id)
			if err != nil {
				return fmt.Errorf("retry failed get %d: %w", id, err)
			}
			if item == nil || item.Stage != StageFailed {
				continue
			}

			targetStage := StageIdentification
			if item.FailedAtStage != "" {
				targetStage = Stage(item.FailedAtStage)
			}

			_, err = s.db.Exec(`
				UPDATE queue_items SET
					stage = ?, in_progress = 0,
					failed_at_stage = NULL, error_message = NULL,
					needs_review = 0, review_reason = NULL, user_stopped = 0,
					updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`,
				string(targetStage), id,
			)
			if err != nil {
				return fmt.Errorf("retry failed %d: %w", id, err)
			}
			if _, err := s.db.Exec("DELETE FROM tasks WHERE item_id = ?", id); err != nil {
				return fmt.Errorf("retry failed %d tasks: %w", id, err)
			}
			count++
		}
		return nil
	})
	return count, err
}

// RetryWithRipSpec routes one failed item to targetStage while replacing its
// opaque RipSpec payload. Higher-level packages own any RipSpec parsing needed
// before calling this method.
func (s *Store) RetryWithRipSpec(id int64, targetStage Stage, ripSpecData string) error {
	return retryOnBusy(func() error {
		_, err := s.db.Exec(`
			UPDATE queue_items SET
				stage = ?, in_progress = 0,
				failed_at_stage = NULL, error_message = NULL,
				needs_review = 0, review_reason = NULL, user_stopped = 0,
				rip_spec_data = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			string(targetStage), ripSpecData, id,
		)
		if err != nil {
			return fmt.Errorf("retry with ripspec %d: %w", id, err)
		}
		if _, err := s.db.Exec("DELETE FROM tasks WHERE item_id = ?", id); err != nil {
			return fmt.Errorf("retry with ripspec %d tasks: %w", id, err)
		}
		return nil
	})
}

// StopItems marks items as failed with a "Stop requested by user" review reason.
// Returns the number of items actually stopped.
func (s *Store) StopItems(ids ...int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var count int
	err := retryOnBusy(func() error {
		count = 0
		for _, id := range ids {
			item, err := s.GetByID(id)
			if err != nil {
				return fmt.Errorf("stop item get %d: %w", id, err)
			}
			if item == nil {
				continue
			}

			// Record where the item was stopped so retry resumes from that
			// stage instead of restarting the whole pipeline. Re-running
			// earlier stages is not just wasted work: a re-run rip wipes
			// staging while later-stage outputs (e.g. reel's resumable
			// encode state) still live there.
			stoppedAt := item.Stage
			item.Stage = StageFailed
			item.InProgress = 0
			item.userStopped = 1
			item.AppendReviewReason(ReviewReasonUserStopped)

			if stoppedAt != StageFailed && stoppedAt != StageCompleted {
				item.FailedAtStage = string(stoppedAt)
			}

			_, err = s.db.Exec(`
				UPDATE queue_items SET
					stage = ?, in_progress = 0, failed_at_stage = ?,
					needs_review = ?, review_reason = ?, user_stopped = 1,
					updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`,
				string(StageFailed), item.FailedAtStage, item.NeedsReview, item.ReviewReason, id,
			)
			if err != nil {
				return fmt.Errorf("stop item %d: %w", id, err)
			}
			count++
		}
		return nil
	})
	return count, err
}

// collectItems reads all items from rows.
func collectItems(rows *sql.Rows) ([]*Item, error) {
	var items []*Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}
