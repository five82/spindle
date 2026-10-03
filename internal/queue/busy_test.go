package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIsBusyErrorRealBusy provokes a real SQLITE_BUSY through two
// connections contending on the same database file and checks that
// isBusyError recognizes the driver error, including when wrapped.
func TestIsBusyErrorRealBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")

	open := func() *sql.DB {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		db.SetMaxOpenConns(1)
		return db
	}

	db1 := open()
	db2 := open()

	if _, err := db1.Exec(`CREATE TABLE t (x INTEGER)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	tx, err := db1.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO t VALUES (1)`); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}

	_, busyErr := db2.Exec(`INSERT INTO t VALUES (2)`)
	if busyErr == nil {
		t.Fatal("expected SQLITE_BUSY from second connection, got nil")
	}
	if !isBusyError(busyErr) {
		t.Fatalf("isBusyError(%v) = false, want true", busyErr)
	}
	if !isBusyError(fmt.Errorf("insert item: %w", busyErr)) {
		t.Fatalf("isBusyError(wrapped %v) = false, want true", busyErr)
	}
	attempts := 0
	if err := retryOnBusy(func() error {
		attempts++
		if attempts < 3 {
			return busyErr
		}
		return nil
	}); err != nil || attempts != 3 {
		t.Fatalf("retry until success: %d attempts, %v", attempts, err)
	}
	attempts = 0
	if err := retryOnBusy(func() error { attempts++; return busyErr }); err == nil || !strings.Contains(err.Error(), "database busy after 5 attempts over ") || attempts != 5 {
		t.Fatalf("exhausted retries: %d attempts, %v", attempts, err)
	}
}

// The daemon runs concurrent stage workers through one sql.DB. A PRAGMA
// executed once in Open does not configure connections opened by the pool later.
func TestOpenConfiguresEveryConnection(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	first, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for i, conn := range []*sql.Conn{first, second} {
		var timeout, foreignKeys int
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if timeout != 5000 || foreignKeys != 1 {
			t.Fatalf("connection %d: timeout=%d, foreign_keys=%d; want 5000, 1", i+1, timeout, foreignKeys)
		}
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	res, err := first.ExecContext(ctx, "INSERT INTO queue_items (stage) VALUES ('ripping')")
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE queue_items SET disc_title = 'locked' WHERE id = ?", id); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	// Longer than retryOnBusy's 150ms total backoff: the second connection
	// must wait for SQLite's busy timeout rather than exhaust those retries.
	done := make(chan error, 1)
	go func() {
		time.Sleep(350 * time.Millisecond)
		done <- tx.Commit()
	}()
	err = store.RecordEvent(Event{ItemID: id, Type: "test", Stage: StageRipping})
	commitErr := <-done
	if commitErr != nil {
		t.Fatal(commitErr)
	}
	if err != nil {
		t.Fatalf("write under brief contention: %v", err)
	}
}

func TestIsBusyErrorNonBusy(t *testing.T) {
	cases := []error{
		nil,
		errors.New("open /media/disc (5)/title.mkv: no such file"),
		errors.New("database is locked"), // message match alone is not enough
		fmt.Errorf("wrapped: %w", errors.New("SQLITE_BUSY lookalike")),
	}
	for _, err := range cases {
		if isBusyError(err) {
			t.Errorf("isBusyError(%v) = true, want false", err)
		}
	}
}
