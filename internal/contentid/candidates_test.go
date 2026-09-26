package contentid

import (
	"reflect"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
)

func TestCandidateEpisodeScopes(t *testing.T) {
	season := &tmdb.Season{Episodes: []tmdb.Episode{{EpisodeNumber: 5}, {EpisodeNumber: 3}, {EpisodeNumber: 4}, {EpisodeNumber: 4}, {EpisodeNumber: 2}, {EpisodeNumber: 1}, {EpisodeNumber: 0}, {EpisodeNumber: 6}, {EpisodeNumber: 7}, {EpisodeNumber: 8}, {EpisodeNumber: 9}, {EpisodeNumber: 10}}}
	episodes := []ripspec.Episode{{RuntimeSeconds: 120}, {RuntimeSeconds: 120}, {RuntimeSeconds: 120}}
	cases := []struct {
		name   string
		env    *ripspec.Envelope
		disc   int
		want   []int
		reason string
	}{
		{"resolved", &ripspec.Envelope{Episodes: []ripspec.Episode{{Episode: 6}, {Episode: 5}, {Episode: 5}}}, 2, []int{5, 6, 7}, "resolved_episode_scope"},
		{"disc two", &ripspec.Envelope{Episodes: episodes}, 2, []int{2, 3, 4, 5, 6, 7, 8}, "disc_block_estimate"},
		{"unknown disc", &ripspec.Envelope{Episodes: episodes}, 0, []int{1, 2, 3, 4, 5, 6}, "season_prefix_fallback"},
		{"empty rip", &ripspec.Envelope{}, 0, []int{1}, "season_prefix_fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveCandidateEpisodes(tc.env, season, tc.disc)
			if got.InitialReason != tc.reason || !reflect.DeepEqual(got.InitialEpisodes, tc.want) || !reflect.DeepEqual(got.ExpandedEpisodes, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
				t.Fatalf("plan = %+v", got)
			}
		})
	}
	if got := deriveCandidateEpisodes(&ripspec.Envelope{}, nil, 1); len(got.InitialEpisodes) != 0 {
		t.Fatalf("nil season: %+v", got)
	}
	if got := resolvedEpisodeScope(nil, []int{1, 2}); got != nil {
		t.Fatalf("nil envelope: %v", got)
	}
	if got := resolvedEpisodeScope(&ripspec.Envelope{Episodes: []ripspec.Episode{{Episode: 0}}}, []int{1, 2}); got != nil {
		t.Fatalf("unresolved: %v", got)
	}
	if got := discBlockScope(&ripspec.Envelope{}, nil, 1); got != nil {
		t.Fatalf("empty season: %v", got)
	}
}

func TestCandidateScopeExpansionAndBlockSizing(t *testing.T) {
	cases := []struct {
		plan       candidateEpisodePlan
		resolution matchResolution
		rips       int
		expand     bool
		reason     string
	}{
		{candidateEpisodePlan{}, matchResolution{}, 1, false, ""},
		{candidateEpisodePlan{InitialEpisodes: []int{1, 2}, ExpandedEpisodes: []int{1, 2}}, matchResolution{}, 1, false, ""},
		{candidateEpisodePlan{InitialEpisodes: []int{1}, ExpandedEpisodes: []int{1, 2}}, matchResolution{}, 1, true, "initial_scope_left_unresolved_titles"},
		{candidateEpisodePlan{InitialEpisodes: []int{1}, ExpandedEpisodes: []int{1, 2}}, matchResolution{SuspectReferenceCount: 1}, 0, true, "initial_scope_contains_suspect_references"},
		{candidateEpisodePlan{InitialEpisodes: []int{1}, ExpandedEpisodes: []int{1, 2}}, matchResolution{}, 0, false, ""},
	}
	for _, tc := range cases {
		got, reason := shouldExpandCandidateScope(tc.plan, tc.resolution, tc.rips)
		if got != tc.expand || reason != tc.reason {
			t.Errorf("plan=%+v: %v %q", tc.plan, got, reason)
		}
	}
	if got := discBlockSize(nil); got != 4 {
		t.Errorf("empty block = %d", got)
	}
	normal := []ripspec.Episode{{RuntimeSeconds: 40}, {RuntimeSeconds: 40}, {RuntimeSeconds: 40}}
	if got := discBlockSize(normal); got != 3 {
		t.Errorf("normal block = %d", got)
	}
	normal[0].RuntimeSeconds = 80
	if got := discBlockSize(normal); got != 4 {
		t.Errorf("double opening block = %d", got)
	}
	if probableOpeningDoubleEpisode(normal[:2]) {
		t.Fatal("short disc classified as double")
	}
	if sameEpisodeSet([]int{1}, []int{2}) || sameEpisodeSet([]int{1}, []int{1, 2}) || !sameEpisodeSet([]int{1, 2}, []int{1, 2}) {
		t.Fatal("episode equality")
	}
	if got := intersectEpisodeRange([]int{1, 3, 5}, 2, 4); !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("intersection: %v", got)
	}
	if got := seasonEpisodeNumbers(nil); got != nil {
		t.Fatalf("nil season: %v", got)
	}
}
