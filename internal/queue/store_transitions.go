package queue

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func rollbackCaseClause(pairs []statusTransition) (string, []any) {
	var b strings.Builder
	b.WriteString("CASE status")
	args := make([]any, 0, len(pairs)*2)
	for _, pair := range pairs {
		b.WriteString(" WHEN ? THEN ?")
		args = append(args, pair.from, pair.to)
	}
	b.WriteString(" ELSE status END")
	return b.String(), args
}

func rollbackStatuses(pairs []statusTransition) []any {
	args := make([]any, len(pairs))
	for i, pair := range pairs {
		args[i] = pair.from
	}
	return args
}

func transitionsForStatuses(pairs []statusTransition, statuses ...Status) []statusTransition {
	if len(statuses) == 0 {
		return pairs
	}
	allowed := make(map[Status]struct{}, len(statuses))
	for _, status := range statuses {
		allowed[status] = struct{}{}
	}
	filtered := make([]statusTransition, 0, len(pairs))
	for _, pair := range pairs {
		if _, ok := allowed[pair.from]; ok {
			filtered = append(filtered, pair)
		}
	}
	return filtered
}

// ResetStuckProcessing resets items in processing states back to the start of their current stage.
func (s *Store) ResetStuckProcessing(ctx context.Context) (int64, error) {
	pairs := processingRollbackTransitions()
	caseExpr, caseArgs := rollbackCaseClause(pairs)
	statusArgs := rollbackStatuses(pairs)
	query := fmt.Sprintf(`UPDATE queue_items
        SET status = %s,
            progress_stage = 'Reset from stuck processing',
            progress_percent = 0, progress_message = NULL, last_heartbeat = NULL, updated_at = ?
        WHERE status IN (%s)`, caseExpr, makePlaceholders(len(statusArgs)))
	args := append(caseArgs, time.Now().UTC().Format(time.RFC3339Nano))
	args = append(args, statusArgs...)
	res, err := s.execWithRetry(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("reset stuck items: %w", err)
	}
	return res.RowsAffected()
}

// UpdateHeartbeat updates the last heartbeat timestamp for an in-flight item.
func (s *Store) UpdateHeartbeat(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	if err := s.execWithoutResultRetry(
		ctx,
		`UPDATE queue_items SET last_heartbeat = ?, updated_at = ? WHERE id = ?`,
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		id,
	); err != nil {
		return fmt.Errorf("update heartbeat: %w", err)
	}
	return nil
}

// ReclaimStaleProcessing returns items stuck in the provided processing statuses (or all processing statuses when none are specified)
// back to the start of their current stage when heartbeats expire.
func (s *Store) ReclaimStaleProcessing(ctx context.Context, cutoff time.Time, statuses ...Status) (int64, error) {
	now := time.Now().UTC()
	pairs := transitionsForStatuses(processingRollbackTransitions(), statuses...)
	if len(pairs) == 0 {
		return 0, nil
	}
	caseExpr, caseArgs := rollbackCaseClause(pairs)
	statusArgs := rollbackStatuses(pairs)
	query := fmt.Sprintf(`UPDATE queue_items
        SET status = %s,
            progress_stage = 'Reclaimed from stale processing',
            progress_percent = 0, progress_message = NULL, last_heartbeat = NULL, updated_at = ?
        WHERE status IN (%s) AND last_heartbeat IS NOT NULL AND last_heartbeat < ?`, caseExpr, makePlaceholders(len(statusArgs)))
	args := append(caseArgs, now.Format(time.RFC3339Nano))
	args = append(args, statusArgs...)
	args = append(args, cutoff.UTC().Format(time.RFC3339Nano))
	res, err := s.execWithRetry(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("reclaim stale items: %w", err)
	}
	return res.RowsAffected()
}

// RetryFailed moves failed items back to pending for reprocessing.
func (s *Store) RetryFailed(ctx context.Context, ids ...int64) (int64, error) {
	if len(ids) == 0 {
		res, err := s.execWithRetry(
			ctx,
			`UPDATE queue_items
            SET status = ?, progress_stage = 'Retry requested', progress_percent = 0,
                progress_message = NULL, error_message = NULL, needs_review = 0, review_reason = NULL, updated_at = ?
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
            progress_message = NULL, error_message = NULL, needs_review = 0, review_reason = NULL, updated_at = ?
        WHERE id IN (` + placeholders + `) AND status = '` + string(StatusFailed) + `'`
	res, err := s.execWithRetry(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("retry selected items: %w", err)
	}
	return res.RowsAffected()
}

// FailActiveOnShutdown marks all non-terminal items as failed when the daemon stops.
// Terminal states (completed, failed) are left untouched.
func (s *Store) FailActiveOnShutdown(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.execWithRetry(
		ctx,
		`UPDATE queue_items
        SET status = ?, progress_stage = 'Daemon stopped', progress_percent = 0,
            progress_message = ?, error_message = ?, last_heartbeat = NULL, updated_at = ?
        WHERE status NOT IN (?, ?)`,
		StatusFailed,
		DaemonStopReason,
		DaemonStopReason,
		now,
		StatusCompleted,
		StatusFailed,
	)
	if err != nil {
		return 0, fmt.Errorf("fail active items on shutdown: %w", err)
	}
	return res.RowsAffected()
}

// StopItems marks selected items as failed to halt further processing.
func (s *Store) StopItems(ctx context.Context, ids ...int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	placeholders := makePlaceholders(len(ids))
	args := make([]any, 0, len(ids)+6)
	args = append(args,
		StatusFailed,
		StopReviewReason,
		"Stopped",
		StopReviewReason,
		now,
	)
	for _, id := range ids {
		args = append(args, id)
	}
	query := `UPDATE queue_items
        SET status = ?, needs_review = 1, review_reason = ?, progress_stage = ?, progress_message = ?,
            progress_percent = 0, error_message = NULL, last_heartbeat = NULL, active_episode_key = NULL, updated_at = ?
        WHERE id IN (` + placeholders + `) AND status NOT IN ('` + string(StatusCompleted) + `','` + string(StatusFailed) + `')`
	res, err := s.execWithRetry(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("stop items: %w", err)
	}
	return res.RowsAffected()
}
