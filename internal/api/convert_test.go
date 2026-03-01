package api

import (
	"testing"

	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

func TestFromQueueItemIncludesEpisodes(t *testing.T) {
	season := 5
	ep1 := ripspec.EpisodeKey(season, 1)
	ep2 := ripspec.EpisodeKey(season, 2)
	env := ripspec.Envelope{
		Titles: []ripspec.Title{
			{ID: 1, Name: "Title One", Duration: 1320},
			{ID: 2, Name: "Title Two", Duration: 1340},
		},
		Episodes: []ripspec.Episode{
			{Key: ep1, TitleID: 1, Season: season, Episode: 1, EpisodeTitle: "Episode One", OutputBasename: "Show - S05E01"},
			{Key: ep2, TitleID: 2, Season: season, Episode: 0, EpisodeTitle: "", OutputBasename: "Show - S05E02"},
		},
		Assets: ripspec.Assets{
			Ripped:  []ripspec.Asset{{EpisodeKey: ep1, Path: "/rips/ep1.mkv"}},
			Encoded: []ripspec.Asset{{EpisodeKey: ep1, Path: "/encoded/ep1.mkv"}},
			Final:   []ripspec.Asset{{EpisodeKey: ep1, Path: "/final/ep1.mkv"}},
		},
		Attributes: map[string]any{
			ripspec.AttrContentIDMethod:      "whisperx_opensubtitles",
			ripspec.AttrEpisodesSynchronized: true,
			ripspec.AttrContentIDMatches: []map[string]any{{
				"episode_key":       ep2,
				"matched_episode":   2,
				"score":             0.91,
				"subtitle_language": "en",
			}},
			"subtitle_generation_summary": map[string]any{
				"opensubtitles":          1,
				"whisperx":               1,
				"expected_opensubtitles": true,
				"fallback_used":          true,
			},
			"subtitle_generation_results": []map[string]any{{
				"episode_key": ep1,
				"source":      "opensubtitles",
			}, {
				"episode_key":            ep2,
				"source":                 "whisperx",
				"language":               "en",
				"opensubtitles_decision": "no_match",
			}},
		},
	}
	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("encode rip spec: %v", err)
	}
	item := &queue.Item{RipSpecData: encoded, MetadataJSON: `{"episode_numbers":[1,2]}`}
	dto := FromQueueItem(item)
	if len(dto.Episodes) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(dto.Episodes))
	}
	if !dto.EpisodesSynced {
		t.Fatalf("expected episodes to be marked synced")
	}
	if dto.EpisodeTotals == nil {
		t.Fatalf("expected episode totals to be populated")
	}
	if dto.EpisodeTotals.Final != 1 || dto.EpisodeTotals.Encoded != 1 || dto.EpisodeTotals.Ripped != 1 {
		t.Fatalf("unexpected totals: %+v", dto.EpisodeTotals)
	}
	if dto.EpisodeIdentifiedCount != 2 {
		t.Fatalf("expected episode identified count 2, got %d", dto.EpisodeIdentifiedCount)
	}
	if dto.Episodes[0].Stage != "final" {
		t.Fatalf("expected first episode to be final, got %s", dto.Episodes[0].Stage)
	}
	if dto.Episodes[1].MatchedEpisode != 2 {
		t.Fatalf("expected second episode to carry matched episode, got %d", dto.Episodes[1].MatchedEpisode)
	}
	if dto.Episodes[1].Episode != 2 {
		t.Fatalf("expected fallback episode number to update, got %d", dto.Episodes[1].Episode)
	}
	if dto.SubtitleGeneration == nil {
		t.Fatalf("expected subtitle generation summary to be populated")
	}
	if dto.SubtitleGeneration.OpenSubtitles != 1 || dto.SubtitleGeneration.WhisperX != 1 || !dto.SubtitleGeneration.FallbackUsed || !dto.SubtitleGeneration.ExpectedOpenSubtitles {
		t.Fatalf("unexpected subtitle generation: %+v", dto.SubtitleGeneration)
	}
	if dto.Episodes[1].GeneratedSubtitleSource != "whisperx" {
		t.Fatalf("expected generated subtitle source whisperx, got %q", dto.Episodes[1].GeneratedSubtitleSource)
	}
	if dto.Episodes[1].GeneratedSubtitleDecision != "no_match" {
		t.Fatalf("expected generated subtitle decision no_match, got %q", dto.Episodes[1].GeneratedSubtitleDecision)
	}
}

func TestEpisodeStageFallsBackToQueueStatus(t *testing.T) {
	season := 3
	epKey := ripspec.EpisodeKey(season, 7)
	env := ripspec.Envelope{
		Titles:   []ripspec.Title{{ID: 1, Name: "Title Seven", Duration: 1500}},
		Episodes: []ripspec.Episode{{Key: epKey, TitleID: 1, Season: season, Episode: 7}},
	}
	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("encode rip spec: %v", err)
	}
	item := &queue.Item{Status: queue.StatusRipping, RipSpecData: encoded}
	dto := FromQueueItem(item)
	if len(dto.Episodes) != 1 {
		t.Fatalf("expected 1 episode, got %d", len(dto.Episodes))
	}
	if dto.Episodes[0].Stage != "ripping" {
		t.Fatalf("expected stage to fall back to queue status, got %s", dto.Episodes[0].Stage)
	}
}

func TestFromQueueItem_NormalizesCompletedProgressStage(t *testing.T) {
	item := &queue.Item{
		Status:          queue.StatusCompleted,
		ProgressStage:   "Organizing",
		ProgressPercent: 42,
	}

	dto := FromQueueItem(item)
	if dto.Progress.Stage != "Completed" {
		t.Fatalf("expected completed stage, got %q", dto.Progress.Stage)
	}
	if dto.Progress.Percent != 100 {
		t.Fatalf("expected percent 100, got %v", dto.Progress.Percent)
	}
}

func TestFromQueueItem_PreservesReviewCompletionStage(t *testing.T) {
	item := &queue.Item{
		Status:          queue.StatusCompleted,
		NeedsReview:     true,
		ProgressStage:   "Manual review",
		ProgressPercent: 100,
	}

	dto := FromQueueItem(item)
	if dto.Progress.Stage != "Manual review" {
		t.Fatalf("expected manual review stage, got %q", dto.Progress.Stage)
	}
	if dto.Progress.Percent != 100 {
		t.Fatalf("expected percent 100, got %v", dto.Progress.Percent)
	}
}

func TestFromQueueItem_FillsEmptyProgressStageFromStatus(t *testing.T) {
	tests := []struct {
		name   string
		status queue.Status
		want   string
	}{
		{name: "pending", status: queue.StatusPending, want: "Pending"},
		{name: "encoding", status: queue.StatusEncoding, want: "Encoding"},
		{name: "organizing", status: queue.StatusOrganizing, want: "Organizing"},
		{name: "completed", status: queue.StatusCompleted, want: "Completed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &queue.Item{
				Status:        tt.status,
				ProgressStage: "",
			}
			dto := FromQueueItem(item)
			if dto.Progress.Stage != tt.want {
				t.Fatalf("expected stage %q, got %q", tt.want, dto.Progress.Stage)
			}
		})
	}
}

func TestCountEpisodeIdentified(t *testing.T) {
	episodes := []EpisodeStatus{
		{Key: "s01_001", Season: 1, Episode: 0},
		{Key: "s01_002", Season: 1, Episode: 2},
		{Key: "s01_003", Season: 1, Episode: 0, MatchScore: 0.91},
		{Key: "s01_004", Season: 1, Episode: 0, MatchedEpisode: 4},
	}
	if got := countEpisodeIdentified(episodes); got != 3 {
		t.Fatalf("expected identified count 3, got %d", got)
	}
}
