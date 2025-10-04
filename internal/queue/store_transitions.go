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

// ReclaimStaleProcessing returns items stuck in processing back to the start of their current stage when heartbeats expire.
func (s *Store) ReclaimStaleProcessing(ctx context.Context, cutoff time.Time) (int64, error) {
	now := time.Now().UTC()
	pairs := processingRollbackTransitions()
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
	res, err := s.execWithRetry(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("retry selected items: %w", err)
	}
	return res.RowsAffected()
}
