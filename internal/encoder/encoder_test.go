package encoder

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/five82/reel"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestPlanJobs_MovieProducesOneJob(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{
				{EpisodeKey: "main", Path: "/tmp/ripped/title00.mkv", Status: ripspec.AssetStatusCompleted},
			},
		},
		// AssetKeys() for movies is ["main"] regardless of Episodes.
	}

	jobs, _ := stage.PendingKeyedAssetJobs(env, ripspec.AssetKindRipped, ripspec.AssetKindEncoded)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job for movie, got %d", len(jobs))
	}
	if jobs[0].Key != "main" {
		t.Errorf("expected episode key 'main', got %q", jobs[0].Key)
	}
	if jobs[0].Input.Path != "/tmp/ripped/title00.mkv" {
		t.Errorf("expected input path '/tmp/ripped/title00.mkv', got %q", jobs[0].Input.Path)
	}
}

func TestPlanJobs_TVProducesNJobs(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/tmp/ripped/title00.mkv", Status: ripspec.AssetStatusCompleted},
				{EpisodeKey: "s01e02", Path: "/tmp/ripped/title01.mkv", Status: ripspec.AssetStatusCompleted},
				{EpisodeKey: "s01e03", Path: "/tmp/ripped/title02.mkv", Status: ripspec.AssetStatusCompleted},
			},
		},
	}

	jobs, _ := stage.PendingKeyedAssetJobs(env, ripspec.AssetKindRipped, ripspec.AssetKindEncoded)
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs for TV, got %d", len(jobs))
	}

	expectedKeys := []string{"s01e01", "s01e02", "s01e03"}
	for i, want := range expectedKeys {
		if jobs[i].Key != want {
			t.Errorf("job[%d]: expected episode key %q, got %q", i, want, jobs[i].Key)
		}
	}
}

func TestPlanJobs_SkipsFailedAssets(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"}, {Key: "s01e02"}, {Key: "s01e03"},
		},
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/tmp/ripped/title00.mkv", Status: ripspec.AssetStatusCompleted},
				{EpisodeKey: "s01e02", Path: "", Status: ripspec.AssetStatusFailed, ErrorMsg: "rip error"},
				{EpisodeKey: "s01e03", Path: "/tmp/ripped/title02.mkv", Status: ripspec.AssetStatusCompleted},
			},
		},
	}

	jobs, _ := stage.PendingKeyedAssetJobs(env, ripspec.AssetKindRipped, ripspec.AssetKindEncoded)
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs (skipping failed), got %d", len(jobs))
	}
	if jobs[0].Key != "s01e01" {
		t.Errorf("job[0]: expected 's01e01', got %q", jobs[0].Key)
	}
	if jobs[1].Key != "s01e03" {
		t.Errorf("job[1]: expected 's01e03', got %q", jobs[1].Key)
	}
}

func TestPlanJobs_EmptyRippedAssets(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	jobs, _ := stage.PendingKeyedAssetJobs(env, ripspec.AssetKindRipped, ripspec.AssetKindEncoded)
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs for empty assets, got %d", len(jobs))
	}
}

func TestProgressThrottle_SuppressesWithinInterval(t *testing.T) {
	item := &queue.Item{ID: 1}
	reporter := &spindleReporter{
		item: item,
		now:  time.Now,
	}

	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	callCount := 0
	reporter.now = func() time.Time {
		callCount++
		// First call at T+0, second at T+1s (within throttle), third at T+3s (past throttle).
		switch callCount {
		case 1:
			return baseTime
		case 2:
			return baseTime.Add(1 * time.Second)
		case 3:
			return baseTime.Add(3 * time.Second)
		default:
			return baseTime.Add(time.Duration(callCount) * time.Second)
		}
	}

	progressCalls := 0

	// Test the throttle logic directly by checking lastPush updates.
	// First call: should proceed (lastPush is zero).
	reporter.lastPush = time.Time{} // zero
	now1 := reporter.now()
	if now1.Sub(reporter.lastPush) < throttleInterval {
		t.Error("first call should not be throttled")
	}
	reporter.lastPush = now1
	progressCalls++

	// Second call: T+1s, should be throttled (only 1s since last push).
	now2 := reporter.now()
	if now2.Sub(reporter.lastPush) >= throttleInterval {
		t.Error("second call at T+1s should be throttled")
	}

	// Third call: T+3s, should proceed (3s since last push at T+0).
	now3 := reporter.now()
	if now3.Sub(reporter.lastPush) < throttleInterval {
		t.Error("third call at T+3s should not be throttled")
	}
	reporter.lastPush = now3
	progressCalls++

	if progressCalls != 2 {
		t.Errorf("expected 2 non-throttled calls, got %d", progressCalls)
	}
}

func TestProgressThrottle_FirstCallAlwaysProceeds(t *testing.T) {
	reporter := &spindleReporter{
		item: &queue.Item{ID: 1},
		now:  func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) },
	}

	// lastPush is zero value, so any time should exceed the throttle.
	now := reporter.now()
	if now.Sub(reporter.lastPush) < throttleInterval {
		t.Error("first call should always proceed regardless of throttle interval")
	}
}

// The grain gate's verdict is only observable downstream if the mapping into
// the envelope copies it; a field dropped here is invisible in metrics.jsonl
// and in the item audit.
func TestEncodeStatsFromResult_CarriesGrainTreatment(t *testing.T) {
	mean, min := 9.90, 9.83
	stats := &reel.EncodeStats{}
	stats.Width = 3840
	stats.GrainTreatment = &reel.GrainTreatmentStats{
		Mode: "auto", Treated: true, Tier: "med", ResolutionClass: "2160p",
		Denoise: "fftdnoiz", GrainTable: "grain-med.tbl",
		GateCRF: 22, SampleChunks: []int{4, 9}, SampleBPP: []float64{0.13, 0.14},
		MedianBPP: 0.131, LightBPPCutoff: 0.0703, MedBPPCutoff: 0.1205,
		GateSeconds: 200, CeilingSeconds: 62,
		DenoiseCeilingJODMean: &mean, DenoiseCeilingJODMin: &min,
	}

	rec := encodeStatsFromResult("main", &reel.Result{Stats: stats})
	if rec == nil || rec.GrainTreatment == nil {
		t.Fatalf("expected a grain treatment on the record, got %+v", rec)
	}
	got := *rec.GrainTreatment
	want := ripspec.GrainTreatment{
		Mode: "auto", Treated: true, Tier: "med", ResolutionClass: "2160p",
		Denoise: "fftdnoiz", GrainTable: "grain-med.tbl",
		GateCRF: 22, SampleChunks: []int{4, 9}, SampleBPP: []float64{0.13, 0.14},
		MedianBPP: 0.131, LightBPPCutoff: 0.0703, MedBPPCutoff: 0.1205,
		GateSeconds: 200, CeilingSeconds: 62,
		DenoiseCeilingJODMean: &mean, DenoiseCeilingJODMin: &min,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("grain treatment mapping lost or changed a field:\n got %+v\nwant %+v", got, want)
	}
}

// An encode from a Reel build that ran no gate must record no verdict rather
// than an empty one that reads as "measured and untreated".
func TestEncodeStatsFromResult_NoGrainTreatment(t *testing.T) {
	rec := encodeStatsFromResult("main", &reel.Result{Stats: &reel.EncodeStats{}})
	if rec == nil || rec.GrainTreatment != nil {
		t.Errorf("expected no grain treatment, got %+v", rec)
	}
}

func TestReporterImplementsInterface(t *testing.T) {
	// Compile-time check that spindleReporter implements reel.Reporter.
	var _ reel.Reporter = (*spindleReporter)(nil)
}

func TestRippingActiveStates(t *testing.T) {
	store, err := queue.Open(":memory:")
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()
	item, _ := store.NewDisc("A", "fp1")
	specs := []queue.TaskSpec{
		{Type: queue.StageIdentification},
		{Type: queue.StageRipping, DependsOn: []queue.Stage{queue.StageIdentification}},
		{Type: queue.StageEncoding, DependsOn: []queue.Stage{queue.StageIdentification}},
	}
	if err := store.EnsureTasks(item, specs); err != nil {
		t.Fatalf("ensure tasks: %v", err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	h := New(nil)

	active, err := h.rippingActive(sess)
	if err != nil || !active {
		t.Fatalf("pending ripping: active=%v err=%v, want true", active, err)
	}

	tasks, _ := store.TasksForItem(item.ID)
	var ripTask *queue.Task
	for _, task := range tasks {
		if task.Type == queue.StageRipping {
			ripTask = task
		}
	}
	if err := store.StartTask(ripTask); err != nil {
		t.Fatalf("start: %v", err)
	}
	if active, _ = h.rippingActive(sess); !active {
		t.Fatal("running ripping should be active")
	}
	if err := store.FinishTask(ripTask, queue.TaskDone, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if active, _ = h.rippingActive(sess); active {
		t.Fatal("done ripping should be inactive")
	}
	if err := store.FinishTask(ripTask, queue.TaskFailed, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if active, _ = h.rippingActive(sess); active {
		t.Fatal("failed ripping should be inactive (no more assets coming)")
	}

	// Absent ripping task (recompilation window) must read inactive.
	if err := store.DeleteTasks(item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if active, _ = h.rippingActive(sess); active {
		t.Fatal("absent ripping task should be inactive")
	}
}
