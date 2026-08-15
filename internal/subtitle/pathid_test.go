package subtitle

import (
	"strings"
	"testing"
)

func TestParseLibraryPathIdentityMovie(t *testing.T) {
	identity, found, err := ParseLibraryPathIdentity("/library/Movies/Inception (2010) [tmdbid-27205]/Inception (2010) [tmdbid-27205].mkv")
	if err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
	if identity.TMDBID != 27205 || identity.Season != 0 || identity.Episode != 0 {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestParseLibraryPathIdentityTVEpisode(t *testing.T) {
	identity, found, err := ParseLibraryPathIdentity("/library/TV/Breaking Bad (2008) [tmdbid-1396]/Season 02/Breaking Bad - S02E07.mkv")
	if err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
	if identity.TMDBID != 1396 || identity.Season != 2 || identity.Episode != 7 || identity.EpisodeEnd != 7 {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestParseLibraryPathIdentityDoubleEpisode(t *testing.T) {
	identity, found, err := ParseLibraryPathIdentity("/library/TV/Show [tmdbid-9]/Season 01/Show - S01E01-E02.mkv")
	if err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
	if identity.Episode != 1 || identity.EpisodeEnd != 2 {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestParseLibraryPathIdentityNoMarker(t *testing.T) {
	_, found, err := ParseLibraryPathIdentity("/library/Movies/Unknown/Unknown.mkv")
	if found || err != nil {
		t.Fatalf("found=%t err=%v", found, err)
	}
}

func TestParseLibraryPathIdentityConflictingIDs(t *testing.T) {
	_, found, err := ParseLibraryPathIdentity("/library/Movies/Film [tmdbid-1]/Film [tmdbid-2].mkv")
	if !found || err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("found=%t err=%v", found, err)
	}
}

func TestParseLibraryPathIdentitySeasonMismatch(t *testing.T) {
	_, found, err := ParseLibraryPathIdentity("/library/TV/Show [tmdbid-1]/Season 02/Show - S03E04.mkv")
	if !found || err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("found=%t err=%v", found, err)
	}
}

func TestParseLibraryPathIdentityMissingEpisodeMarker(t *testing.T) {
	_, found, err := ParseLibraryPathIdentity("/library/TV/Show [tmdbid-1]/Season 02/Show - Episode Seven.mkv")
	if !found || err == nil || !strings.Contains(err.Error(), "no episode marker") {
		t.Fatalf("found=%t err=%v", found, err)
	}
}
