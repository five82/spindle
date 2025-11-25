package logging

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// EventArchive persists structured log events so API consumers can replay
// history even after the in-memory stream rolls over.
type EventArchive struct {
	path string
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

// NewEventArchive creates (or truncates) an on-disk journal for log events.
// The path argument may be empty to disable archiving.
func NewEventArchive(path string) (*EventArchive, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, nil
	}
	if err := ensureLogDir(trimmed); err != nil {
		return nil, fmt.Errorf("ensure archive dir: %w", err)
	}
	if err := truncateFile(trimmed); err != nil {
		return nil, fmt.Errorf("initialize archive %s: %w", trimmed, err)
	}
	file, err := os.OpenFile(trimmed, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open archive %s: %w", trimmed, err)
	}
	return &EventArchive{
		path: trimmed,
		file: file,
		enc:  json.NewEncoder(file),
	}, nil
}

// Append writes the provided event to the archive. Failures are logged via the
// returned error but do not panic; logging continues even if the archive is
// temporarily unavailable.
func (a *EventArchive) Append(evt LogEvent) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureWriter(); err != nil {
		return
	}
	_ = a.enc.Encode(evt)
}

// ReadSince returns events newer than the provided sequence along with the
// highest sequence observed in the archive. Limit bounds the number of events
// returned (0 means unlimited).
func (a *EventArchive) ReadSince(since uint64, limit int) ([]LogEvent, uint64, error) {
	if a == nil || strings.TrimSpace(a.path) == "" {
		return nil, since, nil
	}
	file, err := os.Open(a.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, since, nil
		}
		return nil, since, fmt.Errorf("open archive %s: %w", a.path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	capHint := limit
	if capHint <= 0 || capHint > 512 {
		capHint = 512
	}
	result := make([]LogEvent, 0, capHint)
	highest := since
	for {
		var evt LogEvent
		if err := decoder.Decode(&evt); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return result, highest, fmt.Errorf("decode archive %s: %w", a.path, err)
		}
		if evt.Sequence > highest {
			highest = evt.Sequence
		}
		if evt.Sequence <= since {
			continue
		}
		result = append(result, evt)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, highest, nil
}

// Close releases the archive file handle.
func (a *EventArchive) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var err error
	if a.file != nil {
		err = a.file.Close()
	}
	a.file = nil
	a.enc = nil
	return err
}

// Path returns the on-disk location backing the archive.
func (a *EventArchive) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

func (a *EventArchive) ensureWriter() error {
	if a.file != nil && a.enc != nil {
		return nil
	}
	file, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	a.file = file
	a.enc = json.NewEncoder(file)
	return nil
}

func truncateFile(path string) error {
	if err := ensureLogDir(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	return file.Close()
}
