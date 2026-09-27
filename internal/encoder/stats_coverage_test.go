package encoder

import (
	"encoding/json"
	"testing"

	"github.com/five82/reel"
)

func TestEncodeStatsAbsentAndRepeatedPhases(t *testing.T) {
	if got := encodeStatsFromResult("main", &reel.Result{}); got != nil {
		t.Fatalf("absent reel stats: %+v", got)
	}
	stats := &reel.EncodeStats{}
	stats.Width = 1920
	if err := json.Unmarshal([]byte(`{"phases":[{"name":"search","duration_seconds":3},{"name":"search","duration_seconds":2},{"name":"encode","duration_seconds":10}]}`), stats); err != nil {
		t.Fatal(err)
	}
	rec := encodeStatsFromResult("s01e01", &reel.Result{Stats: stats})
	if rec.EpisodeKey != "s01e01" || rec.ResolutionClass != "1080p" || rec.PhaseSeconds["search"] != 5 || rec.PhaseSeconds["encode"] != 10 {
		t.Fatalf("phase aggregation: %+v", rec)
	}
	for _, tc := range []struct {
		width int
		want  string
	}{{720, "sd"}, {1600, "1080p"}, {3000, "2160p"}} {
		if got := resolutionClass(tc.width); got != tc.want {
			t.Errorf("width %d: %s", tc.width, got)
		}
	}
}
