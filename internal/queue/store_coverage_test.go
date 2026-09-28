package queue

import (
	"strings"
	"testing"
)

func TestRefreshAndRetryWithRipSpec(t *testing.T) {
	s := openTestStore(t)
	item, err := s.NewDisc("Original", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDiscTitle(item, "Changed"); err != nil {
		t.Fatal(err)
	}
	item.DiscTitle = "stale"
	if err := s.Refresh(item); err != nil || item.DiscTitle != "Changed" {
		t.Fatalf("refresh: %+v, %v", item, err)
	}
	if err := s.Refresh(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.FailStage(item, StageIdentification, "bad disc"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureTasks(item, []TaskSpec{{Type: StageIdentification}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryWithRipSpec(item.ID, StageRipping, `{"new":true}`); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != StageRipping || got.FailedAtStage != "" || got.ErrorMessage != "" || got.RipSpecData != `{"new":true}` {
		t.Fatalf("retry: %+v", got)
	}
	tasks, err := s.TasksForItem(item.ID)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("stale tasks: %+v, %v", tasks, err)
	}
	if err := s.EnsureTasks(got, []TaskSpec{{Type: StageRipping}}); err != nil {
		t.Fatal(err)
	}
	tasks, err = s.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasks[0].EncodingDetailsJSON = `{"progress":42}`
	if err := s.UpdateTaskProgress(tasks[0]); err != nil {
		t.Fatal(err)
	}
	stored, err := s.TasksForItem(item.ID)
	if err != nil || stored[0].EncodingDetailsJSON != tasks[0].EncodingDetailsJSON {
		t.Fatalf("task telemetry: %+v %v", stored, err)
	}
}

func TestStoreClosedDatabaseErrors(t *testing.T) {
	s := openTestStore(t)
	item, err := s.NewDisc("Disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func() error{
		"insert":     func() error { _, err := s.NewDisc("Other", "fp2"); return err },
		"get":        func() error { _, err := s.GetByID(item.ID); return err },
		"retry spec": func() error { return s.RetryWithRipSpec(item.ID, StageRipping, "new") },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatalf("expected closed database error, got %v", err)
			}
		})
	}
}
