package main

import (
	"strings"
	"testing"
)

func TestResolveSubtitleIdentityRejectsAmbiguousAndMultiEpisodePaths(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/library/TV/Show [tmdbid-9]/Season 01/Show - S01E01-E02.mkv", "multi-episode"},
		{"/library/Movies/Film [tmdbid-1]/Film [tmdbid-2].mkv", "conflicting"},
	} {
		_, err := resolveSubtitleIdentity(tc.path, 0, 0, 0)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.path, err, tc.want)
		}
	}
	id, err := resolveSubtitleIdentity("/library/TV/Show [tmdbid-9]/Season 02/Show - S02E07.mkv", 0, 0, 0)
	if err != nil || id.TMDBID != 9 || id.Season != 2 || id.Episode != 7 {
		t.Fatalf("path identity = %+v, %v", id, err)
	}
}
