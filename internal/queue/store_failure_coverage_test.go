package queue

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsIncompatibleTransientTables(t *testing.T) {
	for _, tc := range []struct{ name, ddl, want string }{
		{"items", `CREATE VIEW queue_items AS SELECT 1 AS id`, "create queue table"},
		{"tasks", `CREATE VIEW tasks AS SELECT 1 AS id`, "create tasks table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "queue.db")
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec("DROP TABLE " + map[string]string{"items": "queue_items", "tasks": "tasks"}[tc.name]); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(tc.ddl); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = Open(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Open: %v, want %s", err, tc.want)
			}
		})
	}
}

func TestBrokenQueueSchemaSurfacesErrorsWithoutClaimingSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, table string
		run         func(*Store, int64) error
	}{
		{"clear after items dropped", "queue_items", func(s *Store, _ int64) error { _, err := s.Clear(); return err }},
		{"clear completed after items dropped", "queue_items", func(s *Store, _ int64) error { _, err := s.ClearCompleted(); return err }},
		{"retry after items dropped", "queue_items", func(s *Store, id int64) error { _, err := s.RetryFailed(id); return err }},
		{"stop after items dropped", "queue_items", func(s *Store, id int64) error { _, err := s.StopItems(id); return err }},
		{"remove after tasks dropped", "tasks", func(s *Store, id int64) error { return s.Remove(id) }},
		{"retry spec after tasks dropped", "tasks", func(s *Store, id int64) error { return s.RetryWithRipSpec(id, StageRipping, "spec") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "queue.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			item, err := s.NewDisc("Disc", "fp")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("DROP TABLE " + tc.table); err != nil {
				t.Fatal(err)
			}
			if err := tc.run(s, item.ID); err == nil {
				t.Fatal("corrupt queue schema accepted")
			}
		})
	}
}

func TestClosedStoreReportsReadWriteAndRetryFailures(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.NewDisc("Disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"new", func() error { _, err := store.NewDisc("Disc", "new"); return err }},
		{"get", func() error { _, err := store.GetByID(item.ID); return err }},
		{"fingerprint", func() error { _, err := store.FindByFingerprint("fp"); return err }},
		{"complete", func() error { return store.CompleteStage(item, StageRipping) }},
		{"fail", func() error { return store.FailStage(item, StageRipping, "error") }},
		{"work state", func() error { return store.UpdateWorkState(item) }},
		{"encoding", func() error { return store.UpdateEncodingDetails(item) }},
		{"title", func() error { return store.UpdateDiscTitle(item, "New title") }},
		{"remove", func() error { return store.Remove(item.ID) }},
		{"clear", func() error { _, err := store.Clear(); return err }},
		{"clear completed", func() error { _, err := store.ClearCompleted(); return err }},
		{"list", func() error { _, err := store.List(); return err }},
		{"stats", func() error { _, err := store.Stats(); return err }},
		{"disc busy", func() error { _, err := store.HasDiscDependentItem(); return err }},
		{"retry all", func() error { _, err := store.RetryFailed(); return err }},
		{"retry one", func() error { _, err := store.RetryFailed(item.ID); return err }},
		{"retry with spec", func() error { return store.RetryWithRipSpec(item.ID, StageEncoding, "spec") }},
		{"stop", func() error { _, err := store.StopItems(item.ID); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("operation on closed store succeeded")
			}
		})
	}
}
