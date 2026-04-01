package identify

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
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

func TestResolveTitle_PriorityChain(t *testing.T) {
	tests := []struct {
		name      string
		itemTitle string
		discName  string
		want      string
	}{
		{
			name:      "MakeMKV disc name takes priority over item title",
			itemTitle: "The Matrix",
			discName:  "MATRIX_DISC",
			want:      "MATRIX_DISC",
		},
		{
			name:      "falls back to item title when no disc name",
			itemTitle: "The Matrix",
			discName:  "",
			want:      "The Matrix",
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
			got, _ := h.resolveTitle(item, discInfo, nil)
			if got != tt.want {
				t.Errorf("resolveTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveTitle_NilDiscInfo(t *testing.T) {
	h := &Handler{}
	item := &queue.Item{DiscTitle: ""}
	got, _ := h.resolveTitle(item, nil, nil)
	if got != "Unknown Disc" {
		t.Errorf("resolveTitle(nil discInfo) = %q, want %q", got, "Unknown Disc")
	}
}

func TestCleanQueryTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strips season and disc",
			input: "Batman TV Series - Season 2: Disc 6",
			want:  "Batman",
		},
		{
			name:  "strips disc only",
			input: "The Matrix: Disc 1",
			want:  "The Matrix",
		},
		{
			name:  "strips season only",
			input: "Breaking Bad - Season 3",
			want:  "Breaking Bad",
		},
		{
			name:  "strips TV Series only",
			input: "Seinfeld TV Series",
			want:  "Seinfeld",
		},
		{
			name:  "strips volume",
			input: "Kill Bill Volume 1",
			want:  "Kill Bill",
		},
		{
			name:  "no metadata to strip",
			input: "Inception",
			want:  "Inception",
		},
		{
			name:  "case insensitive",
			input: "BATMAN tv series - SEASON 2: DISC 6",
			want:  "BATMAN",
		},
		{
			name:  "strips Ultra HD Blu-ray with trademark",
			input: "Jackie Brown - Ultra HD Blu-ray\u2122",
			want:  "Jackie Brown",
		},
		{
			name:  "strips Blu-ray",
			input: "Jackie Brown - Blu-ray",
			want:  "Jackie Brown",
		},
		{
			name:  "strips 4K Ultra HD",
			input: "The Matrix 4K Ultra HD",
			want:  "The Matrix",
		},
		{
			name:  "strips UHD",
			input: "Inception - UHD",
			want:  "Inception",
		},
		{
			name:  "strips DVD",
			input: "Alien DVD",
			want:  "Alien",
		},
		{
			name:  "combined disc metadata and format branding",
			input: "Batman TV Series - Season 2: Disc 6 Blu-ray",
			want:  "Batman",
		},
		{
			name:  "no false positive on BD within words",
			input: "Abduction",
			want:  "Abduction",
		},
		{
			name:  "falls back to original if result would be empty",
			input: "Season 1",
			want:  "Season 1",
		},
		{
			name:  "falls back to original for format-only title",
			input: "Blu-ray",
			want:  "Blu-ray",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanQueryTitle(tt.input)
			if got != tt.want {
				t.Errorf("CleanQueryTitle(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSplitTitleYear(t *testing.T) {
	tests := []struct {
		input     string
		wantTitle string
		wantYear  int
	}{
		{"Munich (2005)", "Munich", 2005},
		{"Munich 2005", "Munich", 2005},
		{"Munich", "Munich", 0},
		{"Blade Runner (1982)", "Blade Runner", 1982},
		{"2001 A Space Odyssey", "2001 A Space Odyssey", 0},
		{"Munich (1700)", "Munich (1700)", 0},
		{"", "", 0},
		{"2005", "2005", 0},
		{"  Munich (2005)  ", "Munich", 2005},
	}
	for _, tt := range tests {
		gotTitle, gotYear := splitTitleYear(tt.input)
		if gotTitle != tt.wantTitle || gotYear != tt.wantYear {
			t.Errorf("splitTitleYear(%q) = (%q, %d), want (%q, %d)",
				tt.input, gotTitle, gotYear, tt.wantTitle, tt.wantYear)
		}
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

	env := h.buildEnvelope(discardLogger(), item, discInfo, best, "movie", "bluray")

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
	h := &Handler{cfg: &config.Config{}}
	item := &queue.Item{DiscFingerprint: "tv123"}
	discInfo := &makemkv.DiscInfo{Name: "Show Disc"}
	best := &tmdb.SearchResult{
		ID:           999,
		Name:         "Test Show",
		FirstAirDate: "2023-06-01",
		VoteAverage:  8.0,
		VoteCount:    500,
	}

	env := h.buildEnvelope(discardLogger(), item, discInfo, best, "tv", "bluray")

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
	t.Run("movie with scan results", func(t *testing.T) {
		h := &Handler{cfg: &config.Config{}}
		item := &queue.Item{DiscFingerprint: "cached-fp"}
		entry := &discidcache.Entry{
			TMDBID:    42,
			MediaType: "movie",
			Title:     "Cached Movie",
			Year:      "2022",
		}
		discInfo := &makemkv.DiscInfo{
			Titles: []makemkv.TitleInfo{
				{ID: 0, Name: "Feature", Duration: 2 * time.Hour, Chapters: 20},
				{ID: 1, Name: "Extras", Duration: 10 * time.Minute, Chapters: 1},
			},
		}

		env := h.buildEnvelopeFromCache(discardLogger(), item, entry, discInfo, "bluray")

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
		if len(env.Titles) != 2 {
			t.Fatalf("len(Titles) = %d, want 2", len(env.Titles))
		}
		if env.Titles[0].Duration != 7200 {
			t.Errorf("Titles[0].Duration = %d, want 7200", env.Titles[0].Duration)
		}
	})

	t.Run("tv creates episode placeholders", func(t *testing.T) {
		h := &Handler{cfg: &config.Config{}}
		h.cfg.MakeMKV.MinTitleLength = 120
		item := &queue.Item{DiscFingerprint: "cached-fp", DiscTitle: "Show S01"}
		entry := &discidcache.Entry{
			TMDBID:    99,
			MediaType: "tv",
			Title:     "Cached Show",
			Year:      "2023",
		}
		discInfo := &makemkv.DiscInfo{
			Titles: []makemkv.TitleInfo{
				{ID: 0, Name: "Ep1", Duration: 30 * time.Minute},
				{ID: 1, Name: "Ep2", Duration: 30 * time.Minute},
			},
		}

		env := h.buildEnvelopeFromCache(discardLogger(), item, entry, discInfo, "bluray")

		if env.Metadata.MediaType != "tv" {
			t.Errorf("MediaType = %q, want %q", env.Metadata.MediaType, "tv")
		}
		if env.Metadata.ShowTitle != "Cached Show" {
			t.Errorf("ShowTitle = %q, want %q", env.Metadata.ShowTitle, "Cached Show")
		}
		if len(env.Titles) != 2 {
			t.Fatalf("len(Titles) = %d, want 2", len(env.Titles))
		}
		if len(env.Episodes) != 2 {
			t.Fatalf("len(Episodes) = %d, want 2", len(env.Episodes))
		}
		if env.Episodes[0].Season != 1 {
			t.Errorf("Episodes[0].Season = %d, want 1", env.Episodes[0].Season)
		}
	})

	t.Run("nil discInfo produces empty titles", func(t *testing.T) {
		h := &Handler{cfg: &config.Config{}}
		item := &queue.Item{DiscFingerprint: "cached-fp"}
		entry := &discidcache.Entry{
			TMDBID:    42,
			MediaType: "movie",
			Title:     "Cached Movie",
			Year:      "2022",
		}

		env := h.buildEnvelopeFromCache(discardLogger(), item, entry, nil, "bluray")

		if len(env.Titles) != 0 {
			t.Errorf("len(Titles) = %d, want 0 for nil discInfo", len(env.Titles))
		}
	})
}

func TestBuildFallbackEnvelope(t *testing.T) {
	h := &Handler{}

	t.Run("uses item title", func(t *testing.T) {
		item := &queue.Item{DiscTitle: "My Disc", DiscFingerprint: "fp1"}
		env := h.buildFallbackEnvelope(discardLogger(), item, nil)
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
		env := h.buildFallbackEnvelope(discardLogger(), item, discInfo)
		if env.Metadata.Title != "Disc Name" {
			t.Errorf("Title = %q, want %q", env.Metadata.Title, "Disc Name")
		}
	})

	t.Run("uses Unknown Disc when both empty", func(t *testing.T) {
		item := &queue.Item{}
		discInfo := &makemkv.DiscInfo{}
		env := h.buildFallbackEnvelope(discardLogger(), item, discInfo)
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
		env := h.buildFallbackEnvelope(discardLogger(), item, discInfo)
		if len(env.Titles) != 1 {
			t.Fatalf("len(Titles) = %d, want 1", len(env.Titles))
		}
		if env.Titles[0].Duration != 3600 {
			t.Errorf("Titles[0].Duration = %d, want 3600", env.Titles[0].Duration)
		}
	})

	t.Run("extracts season number and creates episodes", func(t *testing.T) {
		h := &Handler{cfg: &config.Config{}}
		h.cfg.MakeMKV.MinTitleLength = 120
		item := &queue.Item{DiscTitle: "Breaking Bad Season 2"}
		discInfo := &makemkv.DiscInfo{
			Titles: []makemkv.TitleInfo{
				{ID: 0, Name: "Title 1", Duration: 45 * time.Minute},
				{ID: 1, Name: "Title 2", Duration: 30 * time.Second}, // too short
				{ID: 2, Name: "Title 3", Duration: 50 * time.Minute},
			},
		}
		env := h.buildFallbackEnvelope(discardLogger(), item, discInfo)
		if env.Metadata.SeasonNumber != 2 {
			t.Errorf("SeasonNumber = %d, want 2", env.Metadata.SeasonNumber)
		}
		if len(env.Episodes) != 2 {
			t.Fatalf("len(Episodes) = %d, want 2", len(env.Episodes))
		}
		if env.Episodes[0].Key != "s02_001" {
			t.Errorf("Episodes[0].Key = %q, want %q", env.Episodes[0].Key, "s02_001")
		}
		if env.Episodes[0].TitleID != 0 {
			t.Errorf("Episodes[0].TitleID = %d, want 0", env.Episodes[0].TitleID)
		}
		if env.Episodes[1].Key != "s02_002" {
			t.Errorf("Episodes[1].Key = %q, want %q", env.Episodes[1].Key, "s02_002")
		}
		if env.Episodes[1].TitleID != 2 {
			t.Errorf("Episodes[1].TitleID = %d, want 2", env.Episodes[1].TitleID)
		}
	})
}

func TestDetectMediaTypeHint(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"TV Series keyword", "Batman TV Series - Season 3: Disc 1", "tv"},
		{"season pattern", "Breaking Bad - Season 2", "tv"},
		{"S prefix", "BATMAN_S03_DISC_1", "tv"},
		{"case insensitive TV Series", "seinfeld tv series", "tv"},
		{"no hint for movie", "Inception", ""},
		{"no hint for disc only", "The Matrix: Disc 1", ""},
		{"no false positive on words containing s+digits", "S1mone", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectMediaTypeHint(tt.input)
			if got != tt.want {
				t.Errorf("detectMediaTypeHint(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractSeasonNumber(t *testing.T) {
	tests := []struct {
		name    string
		sources []string
		want    int
	}{
		{"S01", []string{"BREAKING_BAD_S01"}, 1},
		{"Season 2", []string{"Breaking Bad Season 2"}, 2},
		{"SEASON_3", []string{"SHOW_SEASON_3"}, 3},
		{"case insensitive", []string{"show_s04_disc_1"}, 4},
		{"first source wins", []string{"S01", "S02"}, 1},
		{"second source if first empty", []string{"NO_MATCH", "Season 5"}, 5},
		{"no match", []string{"THE_MATRIX"}, 0},
		{"empty", []string{""}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSeasonNumber(tt.sources...)
			if got != tt.want {
				t.Errorf("extractSeasonNumber(%v) = %d, want %d", tt.sources, got, tt.want)
			}
		})
	}
}

func TestExtractDiscNumber(t *testing.T) {
	tests := []struct {
		name    string
		sources []string
		want    int
	}{
		{"Disc 1", []string{"Batman Disc 1"}, 1},
		{"DISC_2", []string{"SHOW_DISC_2"}, 2},
		{"Volume 3", []string{"Kill Bill Volume 3"}, 3},
		{"Part 1", []string{"Harry Potter Part 1"}, 1},
		{"no match", []string{"THE_MATRIX"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDiscNumber(tt.sources...)
			if got != tt.want {
				t.Errorf("extractDiscNumber(%v) = %d, want %d", tt.sources, got, tt.want)
			}
		})
	}
}

func TestMapDiscSource(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Blu-ray", "bluray"},
		{"DVD", "dvd"},
		{"Unknown", "unknown"},
		{"", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapDiscSource(tt.input)
			if got != tt.want {
				t.Errorf("mapDiscSource(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildEnvelope_TVCreatesEpisodes(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	h.cfg.MakeMKV.MinTitleLength = 120
	item := &queue.Item{
		DiscFingerprint: "tv123",
		DiscTitle:       "Show S02 Disc 1",
	}
	discInfo := &makemkv.DiscInfo{
		Name: "Show Disc",
		Titles: []makemkv.TitleInfo{
			{ID: 0, Name: "Ep 1", Duration: 45 * time.Minute},
			{ID: 1, Name: "Short", Duration: 10 * time.Second},
			{ID: 2, Name: "Ep 2", Duration: 42 * time.Minute},
			{ID: 3, Name: "Ep 3", Duration: 48 * time.Minute},
		},
	}
	best := &tmdb.SearchResult{
		ID:           999,
		Name:         "Test Show",
		FirstAirDate: "2023-06-01",
		VoteAverage:  8.0,
		VoteCount:    500,
	}

	env := h.buildEnvelope(discardLogger(), item, discInfo, best, "tv", "bluray")

	if env.Metadata.SeasonNumber != 2 {
		t.Errorf("SeasonNumber = %d, want 2", env.Metadata.SeasonNumber)
	}
	if env.Metadata.DiscNumber != 1 {
		t.Errorf("DiscNumber = %d, want 1", env.Metadata.DiscNumber)
	}
	if env.Metadata.DiscSource != "bluray" {
		t.Errorf("DiscSource = %q, want %q", env.Metadata.DiscSource, "bluray")
	}
	if len(env.Episodes) != 3 {
		t.Fatalf("len(Episodes) = %d, want 3", len(env.Episodes))
	}
	// Episodes should reference title IDs 0, 2, 3 (skipping short title 1).
	wantTitleIDs := []int{0, 2, 3}
	wantKeys := []string{"s02_001", "s02_002", "s02_003"}
	for i, ep := range env.Episodes {
		if ep.TitleID != wantTitleIDs[i] {
			t.Errorf("Episodes[%d].TitleID = %d, want %d", i, ep.TitleID, wantTitleIDs[i])
		}
		if ep.Key != wantKeys[i] {
			t.Errorf("Episodes[%d].Key = %q, want %q", i, ep.Key, wantKeys[i])
		}
		if ep.Season != 2 {
			t.Errorf("Episodes[%d].Season = %d, want 2", i, ep.Season)
		}
	}
}

func TestCreateEpisodePlaceholders_DeduplicatesBySegmentMap(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	h.cfg.MakeMKV.MinTitleLength = 120
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{SeasonNumber: 1},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 1320, SegmentMap: "00001.m2ts"},
			{ID: 1, Duration: 1380, SegmentMap: "00002.m2ts"},
			{ID: 2, Duration: 1320, SegmentMap: "00001.m2ts"}, // duplicate of title 0
		},
	}
	h.createEpisodePlaceholders(discardLogger(), env)

	if len(env.Episodes) != 2 {
		t.Fatalf("len(Episodes) = %d, want 2 (title 2 should be deduped)", len(env.Episodes))
	}
	if env.Episodes[0].TitleID != 0 {
		t.Errorf("Episodes[0].TitleID = %d, want 0", env.Episodes[0].TitleID)
	}
	if env.Episodes[1].TitleID != 1 {
		t.Errorf("Episodes[1].TitleID = %d, want 1", env.Episodes[1].TitleID)
	}
}

func TestCreateEpisodePlaceholders_DifferentSegmentMapSameDuration(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	h.cfg.MakeMKV.MinTitleLength = 120
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{SeasonNumber: 1},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 1320, SegmentMap: "00001.m2ts"},
			{ID: 1, Duration: 1320, SegmentMap: "00002.m2ts"},
		},
	}
	h.createEpisodePlaceholders(discardLogger(), env)

	if len(env.Episodes) != 2 {
		t.Fatalf("len(Episodes) = %d, want 2 (different SegmentMap = different content)", len(env.Episodes))
	}
}

func TestCreateEpisodePlaceholders_FallsBackToTitleHash(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	h.cfg.MakeMKV.MinTitleLength = 120
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{SeasonNumber: 1},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 1320, TitleHash: "abc123"},
			{ID: 1, Duration: 1380, TitleHash: "def456"},
			{ID: 2, Duration: 1320, TitleHash: "abc123"}, // duplicate by hash
		},
	}
	h.createEpisodePlaceholders(discardLogger(), env)

	if len(env.Episodes) != 2 {
		t.Fatalf("len(Episodes) = %d, want 2 (TitleHash fallback dedup)", len(env.Episodes))
	}
	if env.Episodes[0].TitleID != 0 {
		t.Errorf("Episodes[0].TitleID = %d, want 0", env.Episodes[0].TitleID)
	}
	if env.Episodes[1].TitleID != 1 {
		t.Errorf("Episodes[1].TitleID = %d, want 1", env.Episodes[1].TitleID)
	}
}

func TestCreateEpisodePlaceholders_NoDedupWhenBothKeysEmpty(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	h.cfg.MakeMKV.MinTitleLength = 120
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{SeasonNumber: 1},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 1320},
			{ID: 1, Duration: 1320},
		},
	}
	h.createEpisodePlaceholders(discardLogger(), env)

	if len(env.Episodes) != 2 {
		t.Fatalf("len(Episodes) = %d, want 2 (no dedup when keys empty)", len(env.Episodes))
	}
}

func TestCreateEpisodePlaceholders_SegmentMapTakesPriorityOverTitleHash(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	h.cfg.MakeMKV.MinTitleLength = 120
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{SeasonNumber: 1},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 1320, SegmentMap: "00010.m2ts", TitleHash: "aaa"},
			{ID: 1, Duration: 1320, SegmentMap: "00010.m2ts", TitleHash: "bbb"},
		},
	}
	h.createEpisodePlaceholders(discardLogger(), env)

	if len(env.Episodes) != 1 {
		t.Fatalf("len(Episodes) = %d, want 1 (same SegmentMap dedupes despite different TitleHash)", len(env.Episodes))
	}
}
