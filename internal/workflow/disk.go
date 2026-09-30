package workflow

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/unix"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripper"
	"github.com/five82/spindle/internal/ripspec"
)

const gib = int64(1 << 30)

// diskRequirement leaves room for encoding and other writers after the rip.
// Cache retention is governed separately by the rip cache cap.
func (m *Manager) diskRequirement(task *queue.Task, item *queue.Item) int64 {
	switch task.Type {
	case queue.StageIdentification:
		return 150 * gib
	case queue.StageEncoding:
		return 100 * gib
	case queue.StageRipping:
		required := 150 * gib
		if m.ripCache != nil && item.DiscFingerprint != "" {
			if meta, err := m.ripCache.GetMetadata(item.DiscFingerprint); err == nil && meta.TotalBytes > 0 {
				return max(required, meta.TotalBytes*11/10+100*gib)
			}
		}
		env, err := ripspec.Parse(item.RipSpecData)
		if err != nil {
			return required // Identification or the rip handler will report the invalid spec.
		}
		var bytes int64
		switch env.Metadata.MediaType {
		case "movie":
			if title, ok, _, _, _ := ripper.PrimaryTitleDecisionSummary(env.Titles); ok {
				bytes = title.SizeBytes
			}
		case "tv":
			selected := make(map[int]bool, len(env.Episodes))
			for _, episode := range env.Episodes {
				selected[episode.TitleID] = true
			}
			for _, title := range env.Titles {
				if selected[title.ID] && title.SizeBytes > 0 {
					bytes += title.SizeBytes
				}
			}
		}
		return max(required, bytes*11/10+100*gib)
	default:
		return 0
	}
}

// waitForDisk keeps an eligible task pending until the staging volume can
// accommodate it. The persisted task activity is both Flyer's live warning
// and the deduplication marker for ntfy across scheduler passes and restarts.
func (m *Manager) waitForDisk(ctx context.Context, task *queue.Task, item *queue.Item) bool {
	if m.stagingDir == "" {
		return false
	}
	required := m.diskRequirement(task, item)
	if required == 0 {
		return false
	}
	check := m.diskFree
	if check == nil {
		check = func(path string) (int64, error) {
			var fs unix.Statfs_t
			err := unix.Statfs(path, &fs)
			return int64(fs.Bavail) * int64(fs.Bsize), err
		}
	}
	free, err := check(m.stagingDir)
	if err == nil && free >= required {
		if len(task.Activities) > 0 && task.Activities[0].Operation == "disk_space" {
			since := task.Activities[0].StartedAt
			task.Activities = nil
			if saveErr := m.store.UpdateTaskProgress(task); saveErr != nil {
				m.pipeline.logger.Warn("disk space recovery persistence failed", "event_type", "progress_persist_error", "error_hint", saveErr.Error(), "impact", "wait display may be stale")
				return true
			}
			if err := m.store.RecordEvent(queue.Event{ItemID: item.ID, TaskID: task.ID, Type: "disk_space_available", Stage: task.Type}); err != nil {
				m.pipeline.logger.Warn("item journal write failed", "event_type", "journal_write_failed", "error_hint", err.Error(),
					"impact", "disk-space transition missing from item history")
			}
			m.pipeline.logger.Info("disk space available", "item_id", item.ID, "stage", task.Type,
				"decision_type", logs.DecisionStageExecution, "decision_result", "unblocked",
				"decision_reason", "staging volume has enough available space", "wait_started_at", since)
		}
		return false
	}
	message := fmt.Sprintf("Waiting for disk space: need %.1f GiB, available %.1f GiB on %s", float64(required)/float64(gib), float64(free)/float64(gib), m.stagingDir)
	if err != nil {
		message = fmt.Sprintf("Waiting for disk space: cannot check %s: %v", m.stagingDir, err)
	}
	first := len(task.Activities) == 0 || task.Activities[0].Operation != "disk_space"
	started := time.Now().UTC().Format(time.RFC3339Nano)
	if !first {
		started = task.Activities[0].StartedAt
	}
	if first || task.Activities[0].Message != message {
		task.Activities = []queue.Activity{{ID: "work", Operation: "disk_space", State: "waiting", Message: message,
			StartedAt: started, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
		if saveErr := m.store.UpdateTaskProgress(task); saveErr != nil {
			m.pipeline.logger.Warn("disk space wait persistence failed", "event_type", "progress_persist_error", "error_hint", saveErr.Error(), "impact", "wait display may be stale")
			return true
		}
	}
	if first {
		logger := m.pipeline.logger.With("item_id", item.ID, "stage", task.Type)
		if err := m.store.RecordEvent(queue.Event{ItemID: item.ID, TaskID: task.ID, Type: "disk_space_wait", Stage: task.Type, Message: message}); err != nil {
			logger.Warn("item journal write failed", "event_type", "journal_write_failed", "error_hint", err.Error(),
				"impact", "disk-space transition missing from item history")
		}
		logger.Info("task waiting for disk space", "decision_type", logs.DecisionStageExecution,
			"decision_result", "blocked", "decision_reason", message,
			"required_bytes", required, "free_bytes", free)
		_ = notify.SendLogged(ctx, m.notifier, logger, notify.EventDiskSpaceLow,
			fmt.Sprintf("Spindle: item %d waiting for disk space", item.ID), message)
	}
	return true
}
