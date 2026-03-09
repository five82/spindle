package identify

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
)

// discardLogger returns a logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testCtx returns a context with a discard logger attached.
func testCtx() context.Context {
	return context.Background()
}

func TestResolveTitle_PriorityChain(t *testing.T) {
	tests := []struct {
		name      string
		itemTitle string
		discName  string
		want      string
	}{
		{
			name:      "item title takes priority",
			itemTitle: "The Matrix",
			discName:  "MATRIX_DISC",
			want:      "The Matrix",
		},
		{
			name:      "falls back to disc name",
			itemTitle: "",
			discName:  "MATRIX_DISC",
			want:      "MATRIX_DISC",
		},
		{
			name:      "falls back to Unknown Disc",
			itemTitle: "",
			discName:  "",
			want:      "Unknown Disc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{}
			item := &queue.Item{DiscTitle: tt.itemTitle}
			discInfo := &makemkv.DiscInfo{Name: tt.discName}
			got := h.resolveTitle(item, discInfo)
			if got != tt.want {
				t.Errorf("resolveTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveTitle_NilDiscInfo(t *testing.T) {
	h := &Handler{}
	item := &queue.Item{DiscTitle: ""}
	got := h.resolveTitle(item, nil)
	if got != "Unknown Disc" {
		t.Errorf("resolveTitle(nil discInfo) = %q, want %q", got, "Unknown Disc")
	}
}

func TestDetectEdition_Regex(t *testing.T) {
	tests := []struct {
		name      string
		discTitle string
		discName  string
		wantEmpty bool
		wantMatch string
	}{
		{
			name:      "Extended Edition",
			discTitle: "THE_HOBBIT_EXTENDED_EDITION",
			discName:  "",
			wantEmpty: false,
			wantMatch: "EXTENDED EDITION",
		},
		{
			name:      "Director's Cut",
			discTitle: "BLADE_RUNNER_DIRECTOR'S_CUT",
			discName:  "",
			wantEmpty: false,
			wantMatch: "DIRECTOR'S CUT",
		},
		{
			name:      "Directors Cut no apostrophe",
			discTitle: "BLADE_RUNNER_DIRECTORS_CUT",
			discName:  "",
			wantEmpty: false,
			wantMatch: "DIRECTORS CUT",
		},
		{
			name:      "UNRATED",
			discTitle: "ANCHORMAN_UNRATED",
			discName:  "",
			wantEmpty: false,
			wantMatch: "UNRATED",
		},
		{
			name:      "Theatrical",
			discTitle: "ALIEN_THEATRICAL",
			discName:  "",
			wantEmpty: false,
			wantMatch: "THEATRICAL",
		},
		{
			name:      "Special Edition",
			discTitle: "STAR_WARS_SPECIAL_EDITION",
			discName:  "",
			wantEmpty: false,
			wantMatch: "SPECIAL EDITION",
		},
		{
			name:      "Criterion",
			discTitle: "SEVEN_SAMURAI_CRITERION",
			discName:  "",
			wantEmpty: false,
			wantMatch: "CRITERION",
		},
		{
			name:      "IMAX",
			discTitle: "DUNE_IMAX",
			discName:  "",
			wantEmpty: false,
			wantMatch: "IMAX",
		},
		{
			name:      "Extended Cut",
			discTitle: "LOTR_EXTENDED_CUT",
			discName:  "",
			wantEmpty: false,
			wantMatch: "EXTENDED CUT",
		},
		{
			name:      "case insensitive match",
			discTitle: "hobbit_extended_edition",
			discName:  "",
			wantEmpty: false,
			wantMatch: "extended edition",
		},
		{
			name:      "match in disc name",
			discTitle: "THE_HOBBIT",
			discName:  "Extended Edition",
			wantEmpty: false,
			wantMatch: "Extended Edition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{} // no LLM client, so LLM path won't run
			got := h.detectEdition(testCtx(), discardLogger(), tt.discTitle, tt.discName)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("detectEdition() = %q, want empty", got)
				}
				return
			}
			if got == "" {
				t.Errorf("detectEdition() returned empty, want match containing %q", tt.wantMatch)
			}
		})
	}
}

func TestDetectEdition_NoMatch(t *testing.T) {
	tests := []struct {
		name      string
		discTitle string
		discName  string
	}{
		{
			name:      "plain movie title",
			discTitle: "THE_MATRIX",
			discName:  "",
		},
		{
			name:      "TV show",
			discTitle: "BREAKING_BAD_S01",
			discName:  "Breaking Bad",
		},
		{
			name:      "empty titles",
			discTitle: "",
			discName:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{} // no LLM client
			got := h.detectEdition(testCtx(), discardLogger(), tt.discTitle, tt.discName)
			if got != "" {
				t.Errorf("detectEdition() = %q, want empty", got)
			}
		})
	}
}

func TestBuildEnvelope_Valid(t *testing.T) {
	h := &Handler{}
	item := &queue.Item{
		DiscFingerprint: "abc123",
	}
	discInfo := &makemkv.DiscInfo{
		Name: "Test Disc",
		Titles: []makemkv.TitleInfo{
			{
				ID:       0,
				Name:     "Main Feature",
				Duration: 2 * time.Hour,
				Chapters: 20,
			},
			{
				ID:       1,
				Name:     "Bonus",
				Duration: 30 * time.Minute,
				Chapters: 5,
			},
		},
	}
	best := &tmdb.SearchResult{
		ID:          12345,
		Title:       "Test Movie",
		Overview:    "A test movie.",
		ReleaseDate: "2024-01-15",
		VoteAverage: 7.5,
		VoteCount:   1000,
	}

	env := h.buildEnvelope(item, discInfo, best, "movie", "Extended Edition", 0.85)

	if env.Version != ripspec.CurrentVersion {
		t.Errorf("Version = %d, want %d", env.Version, ripspec.CurrentVersion)
	}
	if env.Fingerprint != "abc123" {
		t.Errorf("Fingerprint = %q, want %q", env.Fingerprint, "abc123")
	}
	if env.Metadata.ID != 12345 {
		t.Errorf("Metadata.ID = %d, want %d", env.Metadata.ID, 12345)
	}
	if env.Metadata.Title != "Test Movie" {
		t.Errorf("Metadata.Title = %q, want %q", env.Metadata.Title, "Test Movie")
	}
	if env.Metadata.MediaType != "movie" {
		t.Errorf("Metadata.MediaType = %q, want %q", env.Metadata.MediaType, "movie")
	}
	if !env.Metadata.Movie {
		t.Error("Metadata.Movie = false, want true")
	}
	if env.Metadata.Edition != "Extended Edition" {
		t.Errorf("Metadata.Edition = %q, want %q", env.Metadata.Edition, "Extended Edition")
	}
	if env.Metadata.Year != "2024" {
		t.Errorf("Metadata.Year = %q, want %q", env.Metadata.Year, "2024")
	}
	if len(env.Titles) != 2 {
		t.Fatalf("len(Titles) = %d, want 2", len(env.Titles))
	}
	if env.Titles[0].Duration != 7200 {
		t.Errorf("Titles[0].Duration = %d, want 7200", env.Titles[0].Duration)
	}
	if env.Titles[1].Duration != 1800 {
		t.Errorf("Titles[1].Duration = %d, want 1800", env.Titles[1].Duration)
	}

	// Verify the envelope can be serialized and parsed.
	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("Encode() error: %v", err)
	}
	parsed, err := ripspec.Parse(encoded)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if parsed.Metadata.Title != "Test Movie" {
		t.Errorf("round-trip Title = %q, want %q", parsed.Metadata.Title, "Test Movie")
	}
}

func TestBuildEnvelope_TV(t *testing.T) {
	h := &Handler{}
	item := &queue.Item{DiscFingerprint: "tv123"}
	discInfo := &makemkv.DiscInfo{Name: "Show Disc"}
	best := &tmdb.SearchResult{
		ID:           999,
		Name:         "Test Show",
		FirstAirDate: "2023-06-01",
		VoteAverage:  8.0,
		VoteCount:    500,
	}

	env := h.buildEnvelope(item, discInfo, best, "tv", "", 0.90)

	if env.Metadata.MediaType != "tv" {
		t.Errorf("MediaType = %q, want %q", env.Metadata.MediaType, "tv")
	}
	if env.Metadata.Movie {
		t.Error("Movie = true, want false for TV")
	}
	if env.Metadata.ShowTitle != "Test Show" {
		t.Errorf("ShowTitle = %q, want %q", env.Metadata.ShowTitle, "Test Show")
	}
	if env.Metadata.FirstAirDate != "2023-06-01" {
		t.Errorf("FirstAirDate = %q, want %q", env.Metadata.FirstAirDate, "2023-06-01")
	}
}

func TestBuildEnvelopeFromCache(t *testing.T) {
	h := &Handler{}
	item := &queue.Item{DiscFingerprint: "cached-fp"}
	entry := &discidcache.Entry{
		TMDBID:    42,
		MediaType: "movie",
		Title:     "Cached Movie",
		Year:      "2022",
	}

	env := h.buildEnvelopeFromCache(item, entry)

	if env.Version != ripspec.CurrentVersion {
		t.Errorf("Version = %d, want %d", env.Version, ripspec.CurrentVersion)
	}
	if env.Metadata.ID != 42 {
		t.Errorf("ID = %d, want 42", env.Metadata.ID)
	}
	if env.Metadata.Title != "Cached Movie" {
		t.Errorf("Title = %q, want %q", env.Metadata.Title, "Cached Movie")
	}
	if !env.Metadata.Cached {
		t.Error("Cached = false, want true")
	}
	if !env.Metadata.Movie {
		t.Error("Movie = false, want true")
	}
}

func TestBuildFallbackEnvelope(t *testing.T) {
	h := &Handler{}

	t.Run("uses item title", func(t *testing.T) {
		item := &queue.Item{DiscTitle: "My Disc", DiscFingerprint: "fp1"}
		env := h.buildFallbackEnvelope(item, nil)
		if env.Metadata.Title != "My Disc" {
			t.Errorf("Title = %q, want %q", env.Metadata.Title, "My Disc")
		}
		if env.Metadata.MediaType != "unknown" {
			t.Errorf("MediaType = %q, want %q", env.Metadata.MediaType, "unknown")
		}
	})

	t.Run("uses disc name when item title empty", func(t *testing.T) {
		item := &queue.Item{DiscTitle: ""}
		discInfo := &makemkv.DiscInfo{Name: "Disc Name"}
		env := h.buildFallbackEnvelope(item, discInfo)
		if env.Metadata.Title != "Disc Name" {
			t.Errorf("Title = %q, want %q", env.Metadata.Title, "Disc Name")
		}
	})

	t.Run("uses Unknown Disc when both empty", func(t *testing.T) {
		item := &queue.Item{}
		discInfo := &makemkv.DiscInfo{}
		env := h.buildFallbackEnvelope(item, discInfo)
		if env.Metadata.Title != "Unknown Disc" {
			t.Errorf("Title = %q, want %q", env.Metadata.Title, "Unknown Disc")
		}
	})

	t.Run("includes titles from discInfo", func(t *testing.T) {
		item := &queue.Item{DiscTitle: "Test"}
		discInfo := &makemkv.DiscInfo{
			Titles: []makemkv.TitleInfo{
				{ID: 0, Name: "Title 1", Duration: time.Hour},
			},
		}
		env := h.buildFallbackEnvelope(item, discInfo)
		if len(env.Titles) != 1 {
			t.Fatalf("len(Titles) = %d, want 1", len(env.Titles))
		}
		if env.Titles[0].Duration != 3600 {
			t.Errorf("Titles[0].Duration = %d, want 3600", env.Titles[0].Duration)
		}
	})
}
