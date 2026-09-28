package queue

import "testing"

func TestTaskTelemetryIsAttemptOwnedAndResetOnRecovery(t *testing.T) {
	store := openTestStore(t)
	item, err := store.NewDisc("disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	specs := []TaskSpec{{Type: StageEncoding}, {Type: StageAnalysis}}
	if err = store.EnsureTasks(item, specs); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if err = store.StartTask(task); err != nil {
			t.Fatal(err)
		}
	}
	enc := tasks[0]
	enc.ActiveAssetKey = "title03"
	enc.EncodingDetailsJSON = `{"asset_key":"title03","current_frame":10}`
	enc.Activities = []Activity{{ID: "video", Operation: "encoding", State: "running", AssetKey: "title03", Completed: 10, Total: 100, Unit: "frames"}}
	if err = store.UpdateTaskProgress(enc); err != nil {
		t.Fatal(err)
	}
	tasks[1].ProgressMessage = "Analyzing audio"
	if err = store.UpdateTaskProgress(tasks[1]); err != nil {
		t.Fatal(err)
	}
	got, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].EncodingDetailsJSON != enc.EncodingDetailsJSON || got[1].EncodingDetailsJSON != "" {
		t.Fatalf("cross-branch telemetry: %+v", got)
	}
	old := *enc
	if err = store.FinishTask(enc, TaskPending, ""); err != nil {
		t.Fatal(err)
	}
	if err = store.StartTask(enc); err != nil {
		t.Fatal(err)
	}
	if enc.Attempts != 2 || enc.EncodingDetailsJSON != "" || len(enc.Activities) > 0 {
		t.Fatalf("retry retained telemetry: %+v", enc)
	}
	if err = store.UpdateTaskProgress(&old); err != nil {
		t.Fatal(err)
	}
	got, err = store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].EncodingDetailsJSON != "" || len(got[0].Activities) > 0 || got[0].ActiveAssetKey != "" {
		t.Fatal("late prior attempt overwrote retry")
	}
	enc.EncodingDetailsJSON = `{"current_frame":20}`
	enc.Activities = old.Activities
	if err = store.UpdateTaskProgress(enc); err != nil {
		t.Fatal(err)
	}
	if err = store.ResetRunningTasks(); err != nil {
		t.Fatal(err)
	}
	got, err = store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].State != TaskPending || got[0].EncodingDetailsJSON != "" || len(got[0].Activities) > 0 {
		t.Fatalf("recovery leaked worker activity: %+v", got[0])
	}
	if err = store.DeleteTasks(item.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureTasks(item, specs); err != nil {
		t.Fatal(err)
	}
	got, err = store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID == old.ID {
		t.Fatal("recompilation reused run identity")
	}
	if err = store.UpdateTaskProgress(&old); err != nil {
		t.Fatal(err)
	}
	got, err = store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].EncodingDetailsJSON != "" {
		t.Fatal("deleted run overwrote new generation")
	}
}
