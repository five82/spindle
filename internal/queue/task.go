package queue

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TaskState is a task's lifecycle state. Readiness (all deps done) is derived
// at query time, never stored. A task failure moves its item to the failed
// stage, so the item filter in ReadyTasks stops dispatching ALL of the item's
// remaining pending tasks (dependents and pending siblings alike) rather than
// marking them failed; already-running sibling tasks finish normally.
type TaskState string

const (
	TaskPending TaskState = "pending"
	TaskRunning TaskState = "running"
	TaskDone    TaskState = "done"
	TaskFailed  TaskState = "failed"
)

const createTasksTableSQL = `
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER NOT NULL,
    type TEXT NOT NULL,
    asset_key TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    deps TEXT NOT NULL DEFAULT '[]',
    progress_percent REAL NOT NULL DEFAULT 0,
    progress_message TEXT NOT NULL DEFAULT '',
    progress_bytes_copied INTEGER NOT NULL DEFAULT 0,
    progress_total_bytes INTEGER NOT NULL DEFAULT 0,
    active_asset_key TEXT NOT NULL DEFAULT '',
    activities TEXT NOT NULL DEFAULT '[]',
    encoding_details_json TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP,
    finished_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tasks_item ON tasks(item_id);
CREATE INDEX IF NOT EXISTS idx_tasks_state ON tasks(state);
`

// Task is one schedulable unit of work for an item. Granularity is one task
// per pipeline stage, so an item's task rows are a pure projection of
// (template, item.Stage): they are compiled lazily and simply deleted and
// recompiled whenever the item's position is mutated externally (retry,
// move). Do not repair task rows in place -- delete and let the scheduler
// recompile. Recompiled rows start at zero progress, which is also the
// progress reset on retry.
//
// Progress columns are written ONLY by the handler running the task
// (through stage.Session); the scheduler owns state/attempts/timestamps.
type Task struct {
	ID                  int64
	ItemID              int64
	Type                Stage
	AssetKey            string
	State               TaskState
	Attempts            int
	ErrorMsg            string
	Deps                []int64
	ProgressPercent     float64
	ProgressMessage     string
	ProgressBytesCopied int64
	ProgressTotalBytes  int64
	ActiveAssetKey      string
	Activities          []Activity
	EncodingDetailsJSON string
	StartedAt           string
	FinishedAt          string
}

// Activity is one current operation, not a percentage of a task. IDs name
// bounded lanes (work, video, audio, references), replaced when their scope
// changes. A positive total identifies a measured denominator; zero is unknown.
// State is running or waiting while live; an activity still open when its run
// reaches a terminal outcome is closed as "ended".
type Activity struct {
	ID         string `json:"id"`
	Operation  string `json:"operation"`
	AssetKey   string `json:"assetKey,omitempty"`
	State      string `json:"state"`
	Message    string `json:"message,omitempty"`
	StartedAt  string `json:"startedAt"`
	UpdatedAt  string `json:"updatedAt"`
	AdvancedAt string `json:"advancedAt,omitempty"`
	Completed  int64  `json:"completed,omitempty"`
	Total      int64  `json:"total,omitempty"`
	Unit       string `json:"unit,omitempty"`
}

// Duration derives the task's wall time from its start/finish timestamps;
// ok is false when either timestamp is missing, unparseable, or inverted.
func (t *Task) Duration() (d time.Duration, ok bool) {
	start, err := parseTimestamp(t.StartedAt)
	if err != nil {
		return 0, false
	}
	end, err := parseTimestamp(t.FinishedAt)
	if err != nil || end.Before(start) {
		return 0, false
	}
	return end.Sub(start), true
}

// TaskSpec describes one task type for compilation. Specs must be listed in
// topological order; DependsOn names task types that appear earlier in the
// list. An empty DependsOn means the task is a root (no dependencies).
type TaskSpec struct {
	Type      Stage
	DependsOn []Stage
}

// EnsureTasks compiles task rows for the item if none exist. Tasks for
// stages positioned before the item's current stage in the spec list are
// inserted as done; the rest are pending with their declared dependencies.
// Items whose stage is not in specs (failed, completed) compile with all
// tasks done or are skipped by callers via eligibility filters.
//
// The item's coarse stage cannot encode which parallel branch finished.
// Recompilation therefore marks only topologically earlier tasks done and
// conservatively re-pends both branches at and after the stage. Handlers make
// this safe and inexpensive by recognizing completed artifacts on retry.
func (s *Store) EnsureTasks(item *Item, specs []TaskSpec) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE item_id = ?`, item.ID).Scan(&count); err != nil {
		return fmt.Errorf("count tasks: %w", err)
	}
	if count > 0 {
		return nil
	}

	position := len(specs)
	for i, spec := range specs {
		if spec.Type == item.Stage {
			position = i
			break
		}
	}

	return retryOnBusy(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()

		idByType := make(map[Stage]int64, len(specs))
		for i, spec := range specs {
			state := TaskPending
			if i < position {
				state = TaskDone
			}
			depIDs := make([]int64, 0, len(spec.DependsOn))
			for _, dep := range spec.DependsOn {
				id, ok := idByType[dep]
				if !ok {
					return fmt.Errorf("task spec %s depends on %s, which is not declared earlier", spec.Type, dep)
				}
				depIDs = append(depIDs, id)
			}
			depsJSON, _ := json.Marshal(depIDs)
			res, err := tx.Exec(
				`INSERT INTO tasks (item_id, type, state, deps) VALUES (?, ?, ?, ?)`,
				item.ID, string(spec.Type), string(state), string(depsJSON),
			)
			if err != nil {
				return fmt.Errorf("insert task %s: %w", spec.Type, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			idByType[spec.Type] = id
		}
		return tx.Commit()
	})
}

// ReadyTasks returns pending tasks whose dependencies are all done, for
// items that are eligible to run (not failed/completed, not user-stopped),
// ordered oldest item first. Readiness is purely dependency- and
// eligibility-derived; same-item dispatch policy belongs to the scheduler.
func (s *Store) ReadyTasks() ([]*Task, error) {
	rows, err := s.db.Query(`
		SELECT `+taskColumnsPrefixed+`
		FROM tasks t
		JOIN queue_items i ON i.id = t.item_id
		WHERE t.state = ?
		  AND i.user_stopped = 0
		  AND i.stage NOT IN (?, ?)
		ORDER BY i.created_at, t.id`,
		string(TaskPending), string(StageFailed), string(StageCompleted))
	if err != nil {
		return nil, fmt.Errorf("query ready tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	states, err := s.taskStates()
	if err != nil {
		return nil, err
	}
	var ready []*Task
	for _, t := range candidates {
		ok := true
		for _, dep := range t.Deps {
			if states[dep] != TaskDone {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, t)
		}
	}
	return ready, nil
}

func (s *Store) taskStates() (map[int64]TaskState, error) {
	rows, err := s.db.Query(`SELECT id, state FROM tasks`)
	if err != nil {
		return nil, fmt.Errorf("query task states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	states := make(map[int64]TaskState)
	for rows.Next() {
		var id int64
		var state string
		if err := rows.Scan(&id, &state); err != nil {
			return nil, err
		}
		states[id] = TaskState(state)
	}
	return states, rows.Err()
}

// taskColumns is the column list scanTask expects, in order.
const taskColumns = `id, item_id, type, asset_key, state, attempts, error_message, deps,
    progress_percent, progress_message, progress_bytes_copied, progress_total_bytes,
    active_asset_key, activities, encoding_details_json, started_at, finished_at`

const taskColumnsPrefixed = `t.id, t.item_id, t.type, t.asset_key, t.state, t.attempts, t.error_message, t.deps,
    t.progress_percent, t.progress_message, t.progress_bytes_copied, t.progress_total_bytes,
    t.active_asset_key, t.activities, t.encoding_details_json, t.started_at, t.finished_at`

func scanTask(rows *sql.Rows) (*Task, error) {
	t := &Task{}
	var typ, state, deps, activities string
	var startedAt, finishedAt sql.NullString
	if err := rows.Scan(&t.ID, &t.ItemID, &typ, &t.AssetKey, &state, &t.Attempts, &t.ErrorMsg, &deps,
		&t.ProgressPercent, &t.ProgressMessage, &t.ProgressBytesCopied, &t.ProgressTotalBytes,
		&t.ActiveAssetKey, &activities, &t.EncodingDetailsJSON, &startedAt, &finishedAt); err != nil {
		return nil, fmt.Errorf("scan task: %w", err)
	}
	if err := json.Unmarshal([]byte(activities), &t.Activities); err != nil {
		return nil, fmt.Errorf("parse task activities: %w", err)
	}
	t.Type = Stage(typ)
	t.State = TaskState(state)
	t.StartedAt = startedAt.String
	t.FinishedAt = finishedAt.String
	if err := json.Unmarshal([]byte(deps), &t.Deps); err != nil {
		return nil, fmt.Errorf("parse task deps: %w", err)
	}
	return t, nil
}

// StartTask marks a task running and bumps its attempt counter.
func (s *Store) StartTask(t *Task) error {
	err := retryOnBusy(func() error {
		_, err := s.db.Exec(
			`UPDATE tasks SET state = ?, attempts = attempts + 1, started_at = CURRENT_TIMESTAMP, finished_at = NULL, error_message = '', active_asset_key = '', activities = '[]', encoding_details_json = '', progress_percent = 0, progress_message = '', progress_bytes_copied = 0, progress_total_bytes = 0 WHERE id = ?`,
			string(TaskRunning), t.ID)
		return err
	})
	if err != nil {
		return fmt.Errorf("start task %d: %w", t.ID, err)
	}
	t.State = TaskRunning
	t.ActiveAssetKey = ""
	t.Activities = nil
	t.EncodingDetailsJSON = ""
	t.ProgressPercent, t.ProgressMessage = 0, ""
	t.ProgressBytesCopied, t.ProgressTotalBytes = 0, 0
	t.Attempts++
	return nil
}

// FinishTask records a terminal (or reverted) state for a task.
func (s *Store) FinishTask(t *Task, state TaskState, errMsg string) error {
	err := retryOnBusy(func() error {
		_, err := s.db.Exec(
			`UPDATE tasks SET state = ?, error_message = ?, finished_at = CURRENT_TIMESTAMP, active_asset_key = '' WHERE id = ?`,
			string(state), errMsg, t.ID)
		return err
	})
	if err != nil {
		return fmt.Errorf("finish task %d: %w", t.ID, err)
	}
	t.State = state
	t.ActiveAssetKey = ""
	t.ErrorMsg = errMsg
	return nil
}

// ResetRunningTasks reverts running tasks to pending and returns them as they
// were. A task still running here was cut off without a terminal outcome (a
// crash or kill; a drain cancels its workers first), so each reset records a
// stage_interrupted journal event carrying the task's last progress. This
// keeps every stage_start paired with a terminal event.
func (s *Store) ResetRunningTasks() ([]*Task, error) {
	var reset []*Task
	err := retryOnBusy(func() error {
		reset = nil
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		rows, err := tx.Query(`SELECT `+taskColumns+` FROM tasks WHERE state = ? ORDER BY id`, string(TaskRunning))
		if err != nil {
			return err
		}
		for rows.Next() {
			t, err := scanTask(rows)
			if err != nil {
				_ = rows.Close()
				return err
			}
			reset = append(reset, t)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, t := range reset {
			var seconds float64
			if start, err := parseTimestamp(t.StartedAt); err == nil && now.After(start) {
				seconds = now.Sub(start).Seconds()
			}
			if _, err := tx.Exec(`INSERT INTO item_events
                (item_id, time, type, stage, task_id, attempt, episode_key, message, percent, duration_seconds)
                VALUES (?, ?, 'stage_interrupted', ?, ?, ?, ?, ?, ?, ?)`, t.ItemID, now.Format(time.RFC3339Nano),
				string(t.Type), t.ID, t.Attempts, t.ActiveAssetKey, t.ProgressMessage, t.ProgressPercent, seconds); err != nil {
				return fmt.Errorf("record interruption for task %d: %w", t.ID, err)
			}
		}
		if _, err := tx.Exec(`UPDATE tasks SET state = ?, active_asset_key = '', activities = '[]', encoding_details_json = '' WHERE state = ?`, string(TaskPending), string(TaskRunning)); err != nil {
			return err
		}
		return tx.Commit()
	})
	return reset, err
}

// DeleteTasks removes all task rows for the given items so the scheduler
// recompiles them from the item's (possibly mutated) stage.
func (s *Store) DeleteTasks(itemIDs ...int64) error {
	if len(itemIDs) == 0 {
		return nil
	}
	return retryOnBusy(func() error {
		for _, id := range itemIDs {
			if _, err := s.db.Exec(`DELETE FROM tasks WHERE item_id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateTaskProgress persists the task's progress columns. The row is the
// single progress slot for the running handler; a write against a deleted
// row (a zombie worker after retry recompiled the tasks) affects nothing.
func (s *Store) UpdateTaskProgress(t *Task) error {
	activities, err := json.Marshal(t.Activities)
	if err != nil {
		return err
	}
	return retryOnBusy(func() error {
		_, err := s.db.Exec(`
			UPDATE tasks SET
				progress_percent = ?, progress_message = ?,
				progress_bytes_copied = ?, progress_total_bytes = ?,
				active_asset_key = ?, activities = ?, encoding_details_json = ?
			WHERE id = ? AND attempts = ?`,
			t.ProgressPercent, t.ProgressMessage,
			t.ProgressBytesCopied, t.ProgressTotalBytes,
			t.ActiveAssetKey, string(activities), t.EncodingDetailsJSON, t.ID, t.Attempts)
		if err != nil {
			return fmt.Errorf("update task %d progress: %w", t.ID, err)
		}
		return nil
	})
}

// TasksForItem returns the item's tasks in insertion (pipeline) order.
func (s *Store) TasksForItem(itemID int64) ([]*Task, error) {
	rows, err := s.db.Query(`
		SELECT `+taskColumns+`
		FROM tasks WHERE item_id = ? ORDER BY id`, itemID)
	if err != nil {
		return nil, fmt.Errorf("query item tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var tasks []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
