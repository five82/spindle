package queue

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	sqlite "modernc.org/sqlite" // Pure-Go SQLite driver.
	sqlite3 "modernc.org/sqlite/lib"
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

// isBusyError checks if an error indicates SQLite BUSY or LOCKED, matching
// on the driver's error code (masked to cover extended codes such as
// SQLITE_BUSY_SNAPSHOT) rather than error-message substrings.
func isBusyError(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	switch se.Code() & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	}
	return false
}

// allColumns is the column list for SELECT queries.
const allColumns = `id, disc_title, stage, in_progress, failed_at_stage, error_message,
    created_at, updated_at, rip_spec_data, disc_fingerprint, metadata_json,
    needs_review, review_reason, encoding_details_json, user_stopped`

// scanItem scans a row into an Item.
func scanItem(row interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var discTitle, failedAtStage, errorMessage sql.NullString
	var createdAt, updatedAt sql.NullString
	var ripSpecData, discFingerprint, metadataJSON sql.NullString
	var reviewReason, encodingDetailsJSON sql.NullString
	var stage string

	err := row.Scan(
		&it.ID, &discTitle, &stage, &it.InProgress,
		&failedAtStage, &errorMessage,
		&createdAt, &updatedAt,
		&ripSpecData, &discFingerprint, &metadataJSON,
		&it.NeedsReview, &reviewReason,
		&encodingDetailsJSON, &it.userStopped,
	)
	if err != nil {
		return nil, err
	}

	it.Stage = Stage(stage)
	it.DiscTitle = discTitle.String
	it.FailedAtStage = Stage(failedAtStage.String)
	it.ErrorMessage = errorMessage.String
	it.CreatedAt = createdAt.String
	it.UpdatedAt = updatedAt.String
	it.RipSpecData = ripSpecData.String
	it.DiscFingerprint = discFingerprint.String
	it.MetadataJSON = metadataJSON.String
	it.ReviewReason = reviewReason.String
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

// StartStage marks an item as actively processing. Progress lives on the
// task rows, so there is nothing to reset here.
func (s *Store) StartStage(item *Item) error {
	item.InProgress = 1
	return retryOnBusy(func() error {
		_, err := s.db.Exec(`
			UPDATE queue_items SET in_progress = 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			item.ID,
		)
		if err != nil {
			return fmt.Errorf("start item %d: %w", item.ID, err)
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

// execUnlessStopped runs update (which must filter on user_stopped = 0) and
// then applies mutate to the in-memory item. Zero rows affected means a user
// stop raced the write: the item is refreshed from the database instead so
// the caller observes the stopped state. label prefixes error messages.
func (s *Store) execUnlessStopped(item *Item, label string, mutate func(), query string, args ...any) error {
	return retryOnBusy(func() error {
		res, err := s.db.Exec(query, args...)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("%s rows affected: %w", label, err)
		}
		if rows == 0 {
			return s.refreshItem(item)
		}
		if mutate != nil {
			mutate()
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

	return s.execUnlessStopped(item, fmt.Sprintf("complete stage item %d", item.ID), func() {
		item.Stage = nextStage
		item.InProgress = 0
		item.userStopped = 0
	}, `
		UPDATE queue_items SET
			stage = ?, in_progress = 0,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_stopped = 0`,
		string(nextStage), item.ID,
	)
}

// FailStage marks an item failed at a specific stage unless the item has
// already been explicitly stopped by the user.
func (s *Store) FailStage(item *Item, failedAt Stage, errMsg string) error {
	return s.execUnlessStopped(item, fmt.Sprintf("fail item %d at %s", item.ID, failedAt), func() {
		item.Stage = StageFailed
		item.InProgress = 0
		item.FailedAtStage = failedAt
		item.ErrorMessage = errMsg
		item.userStopped = 0
	}, `
		UPDATE queue_items SET
			stage = ?, in_progress = 0,
			failed_at_stage = ?, error_message = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_stopped = 0`,
		string(StageFailed), string(failedAt), errMsg, item.ID,
	)
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
	return s.execUnlessStopped(item, fmt.Sprintf("update work state item %d", item.ID), nil, `
		UPDATE queue_items SET
			disc_title = ?,
			updated_at = CURRENT_TIMESTAMP,
			rip_spec_data = ?, disc_fingerprint = ?, metadata_json = ?,
			needs_review = ?, review_reason = ?,
			encoding_details_json = ?
		WHERE id = ? AND user_stopped = 0`,
		item.DiscTitle,
		item.RipSpecData, item.DiscFingerprint, item.MetadataJSON,
		item.NeedsReview, item.ReviewReason,
		item.EncodingDetailsJSON,
		item.ID,
	)
}

// UpdateEncodingDetails persists ONLY the encoding telemetry column. The
// encoding task is the column's single writer.
func (s *Store) UpdateEncodingDetails(item *Item) error {
	return s.execUnlessStopped(item, fmt.Sprintf("update encoding details item %d", item.ID), nil, `
		UPDATE queue_items SET
			encoding_details_json = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_stopped = 0`,
		item.EncodingDetailsJSON, item.ID,
	)
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

// HasDiscDependentItem returns true if a drive-claiming task (identification
// or ripping) is currently running. Task state is exact here where the item
// stage label is not: during rip-to-encode overlap the item may still be
// labeled ripping long after the drive is free.
func (s *Store) HasDiscDependentItem() (bool, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM tasks WHERE state = ? AND type IN (?, ?)",
		string(TaskRunning), string(StageIdentification), string(StageRipping),
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("has disc dependent item: %w", err)
	}
	return count > 0, nil
}

// Stats returns the count of items grouped by displayed stage: the terminal
// stage for failed/completed items, else the earliest running task's type,
// else the item's coarse stage. The item stage column intentionally lags
// running tasks during overlap windows (it only advances when the item goes
// idle), so counting it raw would report a long-finished stage.
func (s *Store) Stats() (map[Stage]int, error) {
	rows, err := s.db.Query(`
		SELECT COALESCE(
			CASE WHEN i.stage IN (?, ?) THEN i.stage END,
			(SELECT t.type FROM tasks t WHERE t.item_id = i.id AND t.state = ? ORDER BY t.id LIMIT 1),
			i.stage) AS display_stage, COUNT(*)
		FROM queue_items i GROUP BY display_stage`,
		string(StageFailed), string(StageCompleted), string(TaskRunning))
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

			targetStage := item.ResumeStage()

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
				item.FailedAtStage = stoppedAt
			}

			_, err = s.db.Exec(`
				UPDATE queue_items SET
					stage = ?, in_progress = 0, failed_at_stage = ?,
					needs_review = ?, review_reason = ?, user_stopped = 1,
					updated_at = CURRENT_TIMESTAMP
				WHERE id = ?`,
				string(StageFailed), string(item.FailedAtStage), item.NeedsReview, item.ReviewReason, id,
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
