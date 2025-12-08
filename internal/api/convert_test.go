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
			"content_id_method":     "whisperx_opensubtitles",
			"episodes_synchronized": true,
			"content_id_matches": []map[string]any{{
				"episode_key":       ep2,
				"matched_episode":   2,
				"score":             0.91,
				"subtitle_language": "en",
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
	if dto.Episodes[0].Stage != "final" {
		t.Fatalf("expected first episode to be final, got %s", dto.Episodes[0].Stage)
	}
	if dto.Episodes[1].MatchedEpisode != 2 {
		t.Fatalf("expected second episode to carry matched episode, got %d", dto.Episodes[1].MatchedEpisode)
	}
	if dto.Episodes[1].Episode != 2 {
		t.Fatalf("expected fallback episode number to update, got %d", dto.Episodes[1].Episode)
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
