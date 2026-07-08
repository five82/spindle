package queue

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
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
