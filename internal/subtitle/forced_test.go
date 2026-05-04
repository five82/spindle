package subtitle

import (
	"context"
	"testing"
)

func TestFetchForcedSubtitleSkipsWithoutTMDBID(t *testing.T) {
	got, err := FetchForcedSubtitle(context.Background(), nil, nil, nil, ForcedLookupRequest{})
	if err != nil {
		t.Fatalf("FetchForcedSubtitle() error = %v", err)
	}
	if got.Decision != "skipped:no_tmdb_id" || got.Path != "" {
		t.Fatalf("FetchForcedSubtitle() = %#v, want skipped:no_tmdb_id with no path", got)
	}
}
