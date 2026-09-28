package auditgather

import (
	"testing"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/logs"
)

func TestLogAttributionDoesNotCrossQueueItems(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 7, DiscFingerprint: " ABC ", DiscTitle: "Movie"}
	for _, tc := range []struct {
		name  string
		entry map[string]any
		want  bool
	}{
		{"matching item", map[string]any{"item_id": float64(7)}, true},
		{"conflicting item is authoritative", map[string]any{"item_id": float64(8), "fingerprint": "abc", "disc_title": "Movie"}, false},
		{"fingerprint ignores case and space", map[string]any{"fingerprint": "abc"}, true},
		{"label fallback", map[string]any{"label": "movie"}, true},
		{"volume fallback", map[string]any{"volume_id": " Movie "}, true},
		{"raw title fallback", map[string]any{"raw_title": "Movie"}, true},
		{"unrelated title", map[string]any{"raw_title": "Other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := logLineMatchesItem(tc.entry, item); got != tc.want {
				t.Fatalf("match=%v, want %v", got, tc.want)
			}
		})
	}
	if logLineMatchesItem(map[string]any{"item_id": float64(7)}, nil) {
		t.Fatal("nil item matched")
	}
}

func TestDiscSourceInferenceFromEvents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry map[string]any
		want  string
	}{
		{"blu ray type", map[string]any{"disc_type": " Blu-ray "}, "bluray"},
		{"bd shorthand", map[string]any{"disc_type": "bd"}, "bluray"},
		{"dvd type", map[string]any{"disc_type": "DVD"}, "dvd"},
		{"decision bluray", map[string]any{"decision_type": logs.DecisionBDInfoAvailability, "decision_result": "bluray"}, "bluray"},
		{"decision dvd", map[string]any{"decision_type": logs.DecisionBDInfoAvailability, "decision_result": "dvd"}, "dvd"},
		{"decision unknown", map[string]any{"decision_type": logs.DecisionBDInfoAvailability, "decision_result": "unknown"}, "unknown"},
		{"structure fallback", map[string]any{"decision_reason": "VIDEO_TS directory"}, "dvd"},
		{"unrelated event", map[string]any{"msg": "unknown disc"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inferDiscSource(tc.entry); got != tc.want {
				t.Fatalf("source=%q, want %q", got, tc.want)
			}
		})
	}
}
