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
		{"retry after items dropped", "queue_items", func(s *Store, id int64) error { _, err := s.RetryFailed(id); return err }},
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
