package queue

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Task states. Readiness (all deps done) is derived at query time, never
// stored. A failed item's remaining pending tasks are gated by the item
// filter in ReadyTasks rather than being marked failed themselves; when
// sibling subtrees can continue independently (Phase 4 of the task-graph
// plan), transitive dependent failure moves here.
const (
	TaskPending = "pending"
	TaskRunning = "running"
	TaskDone    = "done"
	TaskFailed  = "failed"
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
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP,
    finished_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tasks_item ON tasks(item_id);
CREATE INDEX IF NOT EXISTS idx_tasks_state ON tasks(state);
`

// Task is one schedulable unit of work for an item. At the current
// (Phase 3) granularity there is exactly one task per pipeline stage and
// deps form a linear chain, so an item's task rows are a pure projection of
// (template, item.Stage): they are compiled lazily and simply deleted and
// recompiled whenever the item's position is mutated externally (retry,
// move). Do not repair task rows in place -- delete and let the scheduler
// recompile.
type Task struct {
	ID         int64
	ItemID     int64
	Type       Stage
	AssetKey   string
	State      string
	Attempts   int
	ErrorMsg   string
	Deps       []int64
	StartedAt  time.Time
	FinishedAt time.Time
}

// TaskSpec describes one task type in pipeline order for compilation.
type TaskSpec struct {
	Type Stage
}

// EnsureTasks compiles task rows for the item if none exist. Tasks for
// stages the item has already passed are inserted as done; the rest are
// pending with a dependency on the previous task. Items whose stage is not
// in specs (failed, completed) compile with all tasks done or are skipped
// by callers via eligibility filters.
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

		var prevID int64
		for i, spec := range specs {
			state := TaskPending
			if i < position {
				state = TaskDone
			}
			deps := "[]"
			if i > 0 {
				b, _ := json.Marshal([]int64{prevID})
				deps = string(b)
			}
			res, err := tx.Exec(
				`INSERT INTO tasks (item_id, type, state, deps) VALUES (?, ?, ?, ?)`,
				item.ID, string(spec.Type), state, deps,
			)
			if err != nil {
				return fmt.Errorf("insert task %s: %w", spec.Type, err)
			}
			prevID, err = res.LastInsertId()
			if err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

// ReadyTasks returns pending tasks whose dependencies are all done, for
// items that are eligible to run (not failed/completed, not user-stopped,
// not already in progress), ordered oldest item first.
func (s *Store) ReadyTasks() ([]*Task, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.item_id, t.type, t.asset_key, t.state, t.attempts, t.error_message, t.deps
		FROM tasks t
		JOIN queue_items i ON i.id = t.item_id
		WHERE t.state = ?
		  AND i.user_stopped = 0
		  AND i.in_progress = 0
		  AND i.stage NOT IN (?, ?)
		ORDER BY i.created_at, t.id`,
		TaskPending, string(StageFailed), string(StageCompleted))
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

func (s *Store) taskStates() (map[int64]string, error) {
	rows, err := s.db.Query(`SELECT id, state FROM tasks`)
	if err != nil {
		return nil, fmt.Errorf("query task states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	states := make(map[int64]string)
	for rows.Next() {
		var id int64
		var state string
		if err := rows.Scan(&id, &state); err != nil {
			return nil, err
		}
		states[id] = state
	}
	return states, rows.Err()
}

func scanTask(rows *sql.Rows) (*Task, error) {
	t := &Task{}
	var typ, deps string
	if err := rows.Scan(&t.ID, &t.ItemID, &typ, &t.AssetKey, &t.State, &t.Attempts, &t.ErrorMsg, &deps); err != nil {
		return nil, fmt.Errorf("scan task: %w", err)
	}
	t.Type = Stage(typ)
	if err := json.Unmarshal([]byte(deps), &t.Deps); err != nil {
		return nil, fmt.Errorf("parse task deps: %w", err)
	}
	return t, nil
}

// StartTask marks a task running and bumps its attempt counter.
func (s *Store) StartTask(t *Task) error {
	err := retryOnBusy(func() error {
		_, err := s.db.Exec(
			`UPDATE tasks SET state = ?, attempts = attempts + 1, started_at = CURRENT_TIMESTAMP WHERE id = ?`,
			TaskRunning, t.ID)
		return err
	})
	if err != nil {
		return fmt.Errorf("start task %d: %w", t.ID, err)
	}
	t.State = TaskRunning
	t.Attempts++
	return nil
}

// FinishTask records a terminal (or reverted) state for a task.
func (s *Store) FinishTask(t *Task, state, errMsg string) error {
	err := retryOnBusy(func() error {
		_, err := s.db.Exec(
			`UPDATE tasks SET state = ?, error_message = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ?`,
			state, errMsg, t.ID)
		return err
	})
	if err != nil {
		return fmt.Errorf("finish task %d: %w", t.ID, err)
	}
	t.State = state
	t.ErrorMsg = errMsg
	return nil
}

// ResetRunningTasks reverts running tasks to pending. Called on daemon
// startup and shutdown, mirroring ResetInProgress for items.
func (s *Store) ResetRunningTasks() error {
	return retryOnBusy(func() error {
		_, err := s.db.Exec(`UPDATE tasks SET state = ? WHERE state = ?`, TaskPending, TaskRunning)
		return err
	})
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

// TasksForItem returns the item's tasks in insertion (pipeline) order.
func (s *Store) TasksForItem(itemID int64) ([]*Task, error) {
	rows, err := s.db.Query(`
		SELECT id, item_id, type, asset_key, state, attempts, error_message, deps
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
