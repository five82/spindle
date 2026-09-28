package queue

import (
	"fmt"
	"time"
)

// The journal belongs to the transient queue: deleting an item removes its
// events. IDs are monotonic cursors, including across daemon restarts.
const createEventsTableSQL = `
CREATE TABLE IF NOT EXISTS item_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER NOT NULL REFERENCES queue_items(id) ON DELETE CASCADE,
    time TEXT NOT NULL,
    type TEXT NOT NULL,
    stage TEXT NOT NULL,
    task_id INTEGER NOT NULL DEFAULT 0,
    attempt INTEGER NOT NULL DEFAULT 0,
    episode_key TEXT NOT NULL DEFAULT '',
    substage TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    percent REAL NOT NULL DEFAULT 0,
    duration_seconds REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_item_events_item ON item_events(item_id, id);
`

// Event is a lifecycle or encoding-substage transition for one queue item.
type Event struct {
	ID              int64   `json:"id"`
	ItemID          int64   `json:"itemId"`
	Time            string  `json:"time"`
	Type            string  `json:"type"`
	TaskID          int64   `json:"taskId,omitempty"`
	Attempt         int     `json:"attempt,omitempty"`
	Stage           Stage   `json:"stage"`
	EpisodeKey      string  `json:"episodeKey,omitempty"`
	Substage        string  `json:"substage,omitempty"`
	Message         string  `json:"message,omitempty"`
	Percent         float64 `json:"percent,omitempty"`
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
}

// RecordEvent persists a transition before it can be observed by API clients.
func (s *Store) RecordEvent(e Event) error {
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return retryOnBusy(func() error {
		_, err := s.db.Exec(`INSERT INTO item_events
            (item_id, time, type, stage, task_id, attempt, episode_key, substage, message, percent, duration_seconds)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.ItemID, e.Time, e.Type, e.Stage, e.TaskID, e.Attempt,
			e.EpisodeKey, e.Substage, e.Message, e.Percent, e.DurationSeconds)
		if err != nil {
			return fmt.Errorf("record event for item %d: %w", e.ItemID, err)
		}
		return nil
	})
}

// Events returns up to limit transitions after cursor for a single item.
func (s *Store) Events(itemID, cursor int64, limit int) ([]Event, int64, error) {
	if limit < 1 || limit > 500 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT id, item_id, time, type, stage, task_id, attempt, episode_key, substage, message, percent, duration_seconds
        FROM item_events WHERE item_id = ? AND id > ? ORDER BY id LIMIT ?`, itemID, cursor, limit)
	if err != nil {
		return nil, cursor, fmt.Errorf("query events for item %d: %w", itemID, err)
	}
	defer func() { _ = rows.Close() }()
	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.ItemID, &e.Time, &e.Type, &e.Stage, &e.TaskID, &e.Attempt,
			&e.EpisodeKey, &e.Substage, &e.Message, &e.Percent, &e.DurationSeconds); err != nil {
			return nil, cursor, fmt.Errorf("scan item event: %w", err)
		}
		events = append(events, e)
		cursor = e.ID
	}
	if err := rows.Err(); err != nil {
		return nil, cursor, fmt.Errorf("read item events: %w", err)
	}
	return events, cursor, nil
}
