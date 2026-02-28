package encoding

import (
	"context"
	"testing"

	"spindle/internal/encodingstate"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

func TestValidateEpisodeConsistency(t *testing.T) {
	makeResult := func(codec string, w, h int, audioCount int) ffprobe.Result {
		streams := []ffprobe.Stream{
			{CodecName: codec, CodecType: "video", Width: w, Height: h},
		}
		for range audioCount {
			streams = append(streams, ffprobe.Stream{CodecName: "opus", CodecType: "audio"})
		}
		return ffprobe.Result{Streams: streams}
	}

	tests := []struct {
		name         string
		episodes     []ripspec.Episode
		assets       []ripspec.Asset
		probeResults map[string]ffprobe.Result
		wantReview   bool
	}{
		{
			name: "all consistent no review",
			episodes: []ripspec.Episode{
				{Key: "s01e01"}, {Key: "s01e02"}, {Key: "s01e03"},
			},
			assets: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/a.mkv", Status: "completed"},
				{EpisodeKey: "s01e02", Path: "/b.mkv", Status: "completed"},
				{EpisodeKey: "s01e03", Path: "/c.mkv", Status: "completed"},
			},
			probeResults: map[string]ffprobe.Result{
				"/a.mkv": makeResult("av1", 1920, 1080, 1),
				"/b.mkv": makeResult("av1", 1920, 1080, 1),
				"/c.mkv": makeResult("av1", 1920, 1080, 1),
			},
			wantReview: false,
		},
		{
			name: "resolution deviation flags review",
			episodes: []ripspec.Episode{
				{Key: "s01e01"}, {Key: "s01e02"}, {Key: "s01e03"},
			},
			assets: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/a.mkv", Status: "completed"},
				{EpisodeKey: "s01e02", Path: "/b.mkv", Status: "completed"},
				{EpisodeKey: "s01e03", Path: "/c.mkv", Status: "completed"},
			},
			probeResults: map[string]ffprobe.Result{
				"/a.mkv": makeResult("av1", 1920, 1080, 1),
				"/b.mkv": makeResult("av1", 1920, 1080, 1),
				"/c.mkv": makeResult("av1", 1280, 720, 1),
			},
			wantReview: true,
		},
		{
			name: "audio stream count deviation flags review",
			episodes: []ripspec.Episode{
				{Key: "s01e01"}, {Key: "s01e02"}, {Key: "s01e03"},
			},
			assets: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/a.mkv", Status: "completed"},
				{EpisodeKey: "s01e02", Path: "/b.mkv", Status: "completed"},
				{EpisodeKey: "s01e03", Path: "/c.mkv", Status: "completed"},
			},
			probeResults: map[string]ffprobe.Result{
				"/a.mkv": makeResult("av1", 1920, 1080, 1),
				"/b.mkv": makeResult("av1", 1920, 1080, 1),
				"/c.mkv": makeResult("av1", 1920, 1080, 2),
			},
			wantReview: true,
		},
		{
			name: "fewer than 2 episodes skips check",
			episodes: []ripspec.Episode{
				{Key: "s01e01"},
			},
			assets: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/a.mkv", Status: "completed"},
			},
			probeResults: map[string]ffprobe.Result{
				"/a.mkv": makeResult("av1", 1920, 1080, 1),
			},
			wantReview: false,
		},
		{
			name: "failed assets excluded from check",
			episodes: []ripspec.Episode{
				{Key: "s01e01"}, {Key: "s01e02"}, {Key: "s01e03"},
			},
			assets: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/a.mkv", Status: "completed"},
				{EpisodeKey: "s01e02", Path: "", Status: "failed"},
				{EpisodeKey: "s01e03", Path: "/c.mkv", Status: "completed"},
			},
			probeResults: map[string]ffprobe.Result{
				"/a.mkv": makeResult("av1", 1920, 1080, 1),
				"/c.mkv": makeResult("av1", 1920, 1080, 1),
			},
			wantReview: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := SetProbeForTests(func(_ context.Context, _ string, path string) (ffprobe.Result, error) {
				if r, ok := tt.probeResults[path]; ok {
					return r, nil
				}
				return ffprobe.Result{}, nil
			})
			defer restore()

			item := &queue.Item{}
			env := &ripspec.Envelope{
				Episodes: tt.episodes,
				Assets:   ripspec.Assets{Encoded: tt.assets},
			}

			validateEpisodeConsistency(context.Background(), item, env, logging.NewNop())

			if item.NeedsReview != tt.wantReview {
				t.Errorf("NeedsReview = %v, want %v", item.NeedsReview, tt.wantReview)
			}
		})
	}
}

func TestValidateCropRatio(t *testing.T) {
	tests := []struct {
		name     string
		snapshot encodingstate.Snapshot
	}{
		{
			name: "standard ratio logged",
			snapshot: encodingstate.Snapshot{
				Crop: &encodingstate.Crop{Crop: "crop=1920:1080:0:0"},
			},
		},
		{
			name: "non-standard ratio logged",
			snapshot: encodingstate.Snapshot{
				Crop: &encodingstate.Crop{Crop: "crop=1920:800:0:140"},
			},
		},
		{
			name:     "no crop data is no-op",
			snapshot: encodingstate.Snapshot{},
		},
		{
			name: "invalid crop filter is no-op",
			snapshot: encodingstate.Snapshot{
				Crop: &encodingstate.Crop{Crop: "invalid"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := tt.snapshot.Marshal()
			if err != nil {
				t.Fatalf("failed to marshal snapshot: %v", err)
			}
			item := &queue.Item{EncodingDetailsJSON: raw}

			// validateCropRatio only logs decisions; verify it doesn't panic.
			validateCropRatio(item, logging.NewNop())

			// No NeedsReview is set by crop ratio (INFO decision only).
			if item.NeedsReview {
				t.Error("crop ratio validation should not set NeedsReview")
			}
		})
	}
}
