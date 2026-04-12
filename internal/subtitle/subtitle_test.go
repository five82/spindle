package subtitle

import (
	"math"
	"testing"
	"time"

	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/transcription"
)

func TestOverallSubtitlePercent(t *testing.T) {
	tests := []struct {
		name       string
		completed  int
		total      int
		currentPct float64
		want       float64
	}{
		{name: "first item half done", completed: 0, total: 4, currentPct: 50, want: 12.5},
		{name: "three complete", completed: 3, total: 4, currentPct: 0, want: 75},
		{name: "all complete", completed: 4, total: 4, currentPct: 0, want: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := overallSubtitlePercent(tt.completed, tt.total, tt.currentPct)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("overallSubtitlePercent() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestSubtitlePhasePercent(t *testing.T) {
	if got := subtitlePhasePercent(transcription.PhaseExtract, 0); got != 10 {
		t.Fatalf("extract start = %f, want 10", got)
	}
	if got := subtitlePhasePercent(transcription.PhaseExtract, time.Second); got != 25 {
		t.Fatalf("extract done = %f, want 25", got)
	}
	if got := subtitlePhasePercent(transcription.PhaseTranscribe, 0); got != 35 {
		t.Fatalf("transcribe start = %f, want 35", got)
	}
	if got := subtitlePhasePercent(transcription.PhaseTranscribe, time.Second); got != 90 {
		t.Fatalf("transcribe done = %f, want 90", got)
	}
}

func TestAssetKeys_Movie(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}
	keys := env.AssetKeys()
	if len(keys) != 1 || keys[0] != "main" {
		t.Fatalf("expected [main], got %v", keys)
	}
}

func TestAssetKeys_TV(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
	}
	keys := env.AssetKeys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	expected := []string{"s01e01", "s01e02", "s01e03"}
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("key[%d]: expected %s, got %s", i, expected[i], k)
		}
	}
}

func TestAssetKeys_TVSkipsEmptyKeys(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: ""},
			{Key: "s01e03"},
		},
	}
	keys := env.AssetKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys (skipping empty), got %d: %v", len(keys), keys)
	}
	if keys[0] != "s01e01" || keys[1] != "s01e03" {
		t.Errorf("unexpected keys: %v", keys)
	}
}

func TestAssetKeys_TVNoEpisodes(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
	}
	keys := env.AssetKeys()
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys for TV with no episodes, got %d", len(keys))
	}
}
