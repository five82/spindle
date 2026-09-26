package ripspec

import "testing"

func TestEpisodeAnalysisAndEncodeStatsCaseInsensitive(t *testing.T) {
	var missing *AudioAnalysisData
	if got := missing.EpisodeAnalysis("main"); got != nil {
		t.Fatalf("nil audio analysis returned %+v", got)
	}
	data := &AudioAnalysisData{PerEpisode: []EpisodeAudioAnalysis{{EpisodeKey: "S01E01"}}}
	if got := data.EpisodeAnalysis("s01e01"); got != &data.PerEpisode[0] {
		t.Fatalf("case-insensitive episode lookup: %+v", got)
	}
	if got := data.EpisodeAnalysis("absent"); got != nil {
		t.Fatalf("missing episode: %+v", got)
	}
	attrs := &EnvelopeAttributes{}
	attrs.SetEncodeStats(EncodeStats{EpisodeKey: "MAIN", Width: 1920})
	attrs.SetEncodeStats(EncodeStats{EpisodeKey: "main", Width: 3840})
	if len(attrs.EncodeStats) != 1 || attrs.EncodeStats[0].Width != 3840 {
		t.Fatalf("retry duplicated encode stats: %+v", attrs.EncodeStats)
	}
	attrs.SetEncodeStats(EncodeStats{EpisodeKey: "other"})
	if len(attrs.EncodeStats) != 2 {
		t.Fatalf("distinct episode lost: %+v", attrs.EncodeStats)
	}
}

func TestAssetKeysAndEpisodeLast(t *testing.T) {
	movie := &Envelope{Metadata: Metadata{MediaType: "movie"}}
	if keys := movie.AssetKeys(); len(keys) != 1 || keys[0] != "main" {
		t.Fatalf("movie keys: %v", keys)
	}
	tv := &Envelope{Metadata: Metadata{MediaType: "tv"}, Episodes: []Episode{{Key: ""}, {Key: "s01e01"}}}
	if keys := tv.AssetKeys(); len(keys) != 1 || keys[0] != "s01e01" {
		t.Fatalf("TV keys: %v", keys)
	}
	if got := (Episode{Episode: 1, EpisodeEnd: 2}).EpisodeLast(); got != 2 {
		t.Fatalf("double episode last: %d", got)
	}
	if got := (Episode{Episode: 1}).EpisodeLast(); got != 1 {
		t.Fatalf("single episode last: %d", got)
	}
}
