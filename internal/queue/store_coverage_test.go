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
	item.EncodingDetailsJSON = `{"progress":42}`
	if err := s.UpdateEncodingDetails(item); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetByID(item.ID)
	if err != nil || got.EncodingDetailsJSON != item.EncodingDetailsJSON || got.RipSpecData != `{"new":true}` {
		t.Fatalf("telemetry: %+v, %v", got, err)
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
		"refresh":         func() error { return s.Refresh(item) },
		"insert":          func() error { _, err := s.NewDisc("Other", "fp2"); return err },
		"get":             func() error { _, err := s.GetByID(item.ID); return err },
		"find":            func() error { _, err := s.FindByFingerprint("fp"); return err },
		"move":            func() error { return s.MoveToStage(item, StageRipping) },
		"complete":        func() error { return s.CompleteStage(item, StageRipping) },
		"title":           func() error { return s.UpdateDiscTitle(item, "New") },
		"work":            func() error { return s.UpdateWorkState(item) },
		"encoding":        func() error { return s.UpdateEncodingDetails(item) },
		"remove":          func() error { return s.Remove(item.ID) },
		"clear":           func() error { _, err := s.Clear(); return err },
		"clear completed": func() error { _, err := s.ClearCompleted(); return err },
		"list":            func() error { _, err := s.List(); return err },
		"disc dependent":  func() error { _, err := s.HasDiscDependentItem(); return err },
		"stats":           func() error { _, err := s.Stats(); return err },
		"retry":           func() error { _, err := s.RetryFailed(item.ID); return err },
		"retry spec":      func() error { return s.RetryWithRipSpec(item.ID, StageRipping, "new") },
		"stop":            func() error { _, err := s.StopItems(item.ID); return err },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatalf("expected closed database error, got %v", err)
			}
		})
	}
}
