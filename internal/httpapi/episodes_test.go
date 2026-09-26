package httpapi

import (
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestBuildEpisodesTracksAssetProgressAndFailures(t *testing.T) {
	if got := buildEpisodes(&ripspec.Envelope{}, nil); got != nil {
		t.Fatalf("empty episodes = %+v", got)
	}
	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{{Key: "S01E01", TitleID: 1, Episode: 1}, {Key: "s01e02", TitleID: 2, Episode: 2, EpisodeTitle: "Custom", RuntimeSeconds: 99}, {Key: "s01e03", TitleID: 3}, {Key: "s01e04"}},
		Titles:   []ripspec.Title{{ID: 1, Name: "Fallback", Duration: 44}, {ID: 2, Name: "Disc title", EpisodeTitle: "Ignored", Duration: 40}, {ID: 3, Name: "Raw title"}},
		Assets: ripspec.Assets{
			Ripped:    []ripspec.Asset{{EpisodeKey: "S01E01", Status: ripspec.AssetStatusCompleted, Path: "rip.mkv"}},
			Encoded:   []ripspec.Asset{{EpisodeKey: "S01E01", Status: ripspec.AssetStatusCompleted, Path: "encode.mkv"}, {EpisodeKey: "s01e02", Status: ripspec.AssetStatusFailed, ErrorMsg: "encode broke"}},
			Subtitled: []ripspec.Asset{{EpisodeKey: "S01E01", Status: ripspec.AssetStatusCompleted, Path: "subs.srt"}, {EpisodeKey: "s01e02", Status: ripspec.AssetStatusFailed, ErrorMsg: "subtitle broke"}},
			Final:     []ripspec.Asset{{EpisodeKey: "S01E01", Status: ripspec.AssetStatusCompleted, Path: "final.mkv"}, {EpisodeKey: "s01e02", Status: ripspec.AssetStatusFailed, ErrorMsg: "final broke"}},
		},
	}
	env.Attributes.SubtitleGenerationResults = []ripspec.SubtitleGenRecord{{EpisodeKey: "s01e01", Source: "opensubtitles", Language: "en", ValidationResult: "passed", ReviewIssues: []string{"timing"}}}
	env.Attributes.AudioAnalysis = &ripspec.AudioAnalysisData{PerEpisode: []ripspec.EpisodeAudioAnalysis{{EpisodeKey: "s01e01", CommentaryTracks: []ripspec.CommentaryTrackRef{{Index: 2}}, ExcludedTracks: []ripspec.ExcludedTrackRef{{Index: 3}}}}}
	got := buildEpisodes(env, map[string]bool{"s01e01": true})
	if len(got) != 4 {
		t.Fatalf("episodes = %+v", got)
	}
	first := got[0]
	if first.Stage != "final" || first.FinalPath != "final.mkv" || first.RippedPath != "rip.mkv" || first.SubtitledPath != "subs.srt" || !first.Active || first.Title != "Fallback" || first.RuntimeSeconds != 44 || first.MatchedEpisode != 1 || first.SubtitleSource != "opensubtitles" || first.CommentaryTracks != 1 || first.ExcludedTracks != 1 {
		t.Fatalf("completed episode = %+v", first)
	}
	if got[1].Status != "failed" || got[1].ErrorMessage != "final broke" || got[1].Stage != "planned" || got[1].Title != "Custom" || got[1].RuntimeSeconds != 99 {
		t.Fatalf("failed episode = %+v", got[1])
	}
	if got[2].Title != "Raw title" || got[2].MatchedEpisode != 0 || got[3].Stage != "planned" {
		t.Fatalf("unmatched episodes = %+v", got[2:])
	}
}
