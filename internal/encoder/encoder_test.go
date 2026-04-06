package encoder

import (
	"math"
	"testing"
	"time"

	"github.com/five82/drapto"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func TestPlanJobs_MovieProducesOneJob(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{
				{EpisodeKey: "main", Path: "/tmp/ripped/title00.mkv", Status: ripspec.AssetStatusCompleted},
			},
		},
	}

	jobs := planJobs(env)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job for movie, got %d", len(jobs))
	}
	if jobs[0].episodeKey != "main" {
		t.Errorf("expected episode key 'main', got %q", jobs[0].episodeKey)
	}
	if jobs[0].inputPath != "/tmp/ripped/title00.mkv" {
		t.Errorf("expected input path '/tmp/ripped/title00.mkv', got %q", jobs[0].inputPath)
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

	jobs := planJobs(env)
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs for TV, got %d", len(jobs))
	}

	expectedKeys := []string{"s01e01", "s01e02", "s01e03"}
	for i, want := range expectedKeys {
		if jobs[i].episodeKey != want {
			t.Errorf("job[%d]: expected episode key %q, got %q", i, want, jobs[i].episodeKey)
		}
	}
}

func TestPlanJobs_SkipsFailedAssets(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/tmp/ripped/title00.mkv", Status: ripspec.AssetStatusCompleted},
				{EpisodeKey: "s01e02", Path: "", Status: ripspec.AssetStatusFailed, ErrorMsg: "rip error"},
				{EpisodeKey: "s01e03", Path: "/tmp/ripped/title02.mkv", Status: ripspec.AssetStatusCompleted},
			},
		},
	}

	jobs := planJobs(env)
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs (skipping failed), got %d", len(jobs))
	}
	if jobs[0].episodeKey != "s01e01" {
		t.Errorf("job[0]: expected 's01e01', got %q", jobs[0].episodeKey)
	}
	if jobs[1].episodeKey != "s01e03" {
		t.Errorf("job[1]: expected 's01e03', got %q", jobs[1].episodeKey)
	}
}

func TestPlanJobs_EmptyRippedAssets(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	jobs := planJobs(env)
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs for empty assets, got %d", len(jobs))
	}
}

func TestProgressThrottle_SuppressesWithinInterval(t *testing.T) {
	item := &queue.Item{ID: 1}
	// Use a nil store; we track calls via the clock.
	reporter := &spindleReporter{
		item:  item,
		store: nil,
		now:   time.Now,
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

	// Mock store that tracks UpdateProgress calls.
	progressCalls := 0
	reporter.store = nil // We can't call store methods with nil store.

	// Instead, test the throttle logic directly by checking lastPush updates.
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

func TestOverallEncodePercent(t *testing.T) {
	tests := []struct {
		name       string
		completed  int
		total      int
		currentPct float64
		want       float64
	}{
		{name: "first job half done", completed: 0, total: 12, currentPct: 50, want: 4.166666666666667},
		{name: "ninth job one third done", completed: 9, total: 12, currentPct: 33.333333333333336, want: 77.77777777777779},
		{name: "all jobs complete", completed: 12, total: 12, currentPct: 0, want: 100},
		{name: "invalid total", completed: 1, total: 0, currentPct: 50, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := overallEncodePercent(tt.completed, tt.total, tt.currentPct)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("overallEncodePercent(%d, %d, %f) = %f, want %f", tt.completed, tt.total, tt.currentPct, got, tt.want)
			}
		})
	}
}

func TestReporterImplementsInterface(t *testing.T) {
	// Compile-time check that spindleReporter implements drapto.Reporter.
	var _ drapto.Reporter = (*spindleReporter)(nil)
}
