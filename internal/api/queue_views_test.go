package api

import "testing"

func TestEpisodeDisplayLabel(t *testing.T) {
	if got := EpisodeDisplayLabel(EpisodeStatus{Season: 2, Episode: 3}); got != "S02E03" {
		t.Fatalf("EpisodeDisplayLabel season/episode = %q, want S02E03", got)
	}
	if got := EpisodeDisplayLabel(EpisodeStatus{Key: "s01e09"}); got != "S01E09" {
		t.Fatalf("EpisodeDisplayLabel key = %q, want S01E09", got)
	}
	if got := EpisodeDisplayLabel(EpisodeStatus{}); got != "EP" {
		t.Fatalf("EpisodeDisplayLabel empty = %q, want EP", got)
	}
}

func TestPrimaryEpisodePath(t *testing.T) {
	ep := EpisodeStatus{
		RippedPath:  "/ripped.mkv",
		EncodedPath: "/encoded.mkv",
		FinalPath:   "/final.mkv",
	}
	if got := PrimaryEpisodePath(ep); got != "/final.mkv" {
		t.Fatalf("PrimaryEpisodePath with final = %q, want /final.mkv", got)
	}

	ep.FinalPath = ""
	if got := PrimaryEpisodePath(ep); got != "/encoded.mkv" {
		t.Fatalf("PrimaryEpisodePath with encoded = %q, want /encoded.mkv", got)
	}

	ep.EncodedPath = ""
	if got := PrimaryEpisodePath(ep); got != "/ripped.mkv" {
		t.Fatalf("PrimaryEpisodePath with ripped = %q, want /ripped.mkv", got)
	}
}

func TestEpisodeSubtitleSummary(t *testing.T) {
	ep := EpisodeStatus{
		SubtitleLanguage: "en",
		SubtitleSource:   "opensubtitles",
		MatchScore:       0.91234,
	}
	if got := EpisodeSubtitleSummary(ep); got != "EN · opensubtitles · score 0.91" {
		t.Fatalf("EpisodeSubtitleSummary = %q", got)
	}

	if got := EpisodeSubtitleSummary(EpisodeStatus{}); got != "" {
		t.Fatalf("EpisodeSubtitleSummary empty = %q, want empty", got)
	}
}
