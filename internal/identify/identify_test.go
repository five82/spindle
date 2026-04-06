package identify

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/keydb"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/tmdb"
)

// discardLogger returns a logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestUpdateProgress(t *testing.T) {
	h := &Handler{}
	item := &queue.Item{}
	h.updateProgress(item, 42, "Phase 2/3 - Scanning disc and resolving metadata")
	if item.ProgressPercent != 42 {
		t.Fatalf("ProgressPercent = %f, want 42", item.ProgressPercent)
	}
	if item.ProgressMessage != "Phase 2/3 - Scanning disc and resolving metadata" {
		t.Fatalf("ProgressMessage = %q", item.ProgressMessage)
	}
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

func TestResolveTitle_UsesKeyDBDiscID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "KEYDB.cfg")
	discID := "DCB2FF29F40C9CD4702BC163A3F4511A492E54A4"
	if err := os.WriteFile(path, []byte(discID+" | Star Trek: The Next Generation | extra\n"), 0o644); err != nil {
		t.Fatalf("write keydb: %v", err)
	}
	cat, _, err := keydb.LoadFromFile(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	h := &Handler{keydbCat: cat}
	item := &queue.Item{DiscTitle: "STAR TREK TNG S1 D1", DiscFingerprint: "not-the-disc-id"}
	bdInfo := &BDInfoResult{DiscID: discID, DiscName: "BDINFO NAME"}
	got, source := h.resolveTitle(item, &makemkv.DiscInfo{Name: "MAKEMKV NAME"}, bdInfo)
	if got != "Star Trek: The Next Generation" {
		t.Fatalf("resolveTitle() = %q, want %q", got, "Star Trek: The Next Generation")
	}
	if source != "keydb" {
		t.Fatalf("source = %q, want keydb", source)
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
			name:  "strips shorthand season and disc markers",
			input: "STAR TREK TNG S1 D1",
			want:  "STAR TREK TNG",
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
				Duration: 7200,
				Chapters: 20,
			},
			{
				ID:       1,
				Name:     "Bonus",
				Duration: 1800,
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
				{ID: 0, Name: "Feature", Duration: 7200, Chapters: 20},
				{ID: 1, Name: "Extras", Duration: 600, Chapters: 1},
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
				{ID: 0, Name: "Ep1", Duration: 1800},
				{ID: 1, Name: "Ep2", Duration: 1800},
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
				{ID: 0, Name: "Title 1", Duration: 3600},
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
				{ID: 0, Name: "Title 1", Duration: 2700},
				{ID: 1, Name: "Title 2", Duration: 30}, // too short
				{ID: 2, Name: "Title 3", Duration: 3000},
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
		{"D1 shorthand", []string{"STAR TREK TNG S1 D1"}, 1},
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

func TestNoTMDBMatchIsFatal(t *testing.T) {
	tests := []struct {
		name      string
		mediaHint string
		want      bool
	}{
		{name: "tv hint is fatal", mediaHint: "tv", want: true},
		{name: "movie hint is not fatal", mediaHint: "movie", want: false},
		{name: "unknown hint is not fatal", mediaHint: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := noTMDBMatchIsFatal(tt.mediaHint); got != tt.want {
				t.Fatalf("noTMDBMatchIsFatal(%q) = %v, want %v", tt.mediaHint, got, tt.want)
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
			{ID: 0, Name: "Ep 1", Duration: 2700},
			{ID: 1, Name: "Short", Duration: 10},
			{ID: 2, Name: "Ep 2", Duration: 2520},
			{ID: 3, Name: "Ep 3", Duration: 2880},
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

func TestSelectTVEpisodeTitles(t *testing.T) {
	tests := []struct {
		name           string
		minTitleLength int
		titles         []ripspec.Title
		wantIDs        []int
		wantAmbiguous  bool
		wantDoubleLong int
		wantDuplicates int
		wantExtras     int
		wantReasonByID map[int]string
	}{
		{
			name:           "half hour episodes with extras",
			minTitleLength: 120,
			titles: []ripspec.Title{
				{ID: 0, Duration: 22 * 60},
				{ID: 1, Duration: 23 * 60},
				{ID: 2, Duration: 22 * 60},
				{ID: 3, Duration: 3 * 60},
				{ID: 4, Duration: 4 * 60},
			},
			wantIDs:        []int{0, 1, 2},
			wantExtras:     2,
			wantReasonByID: map[int]string{3: "runtime_cluster_extra", 4: "runtime_cluster_extra"},
		},
		{
			name:           "45 minute episodes with extras",
			minTitleLength: 120,
			titles: []ripspec.Title{
				{ID: 0, Duration: 45 * 60},
				{ID: 1, Duration: 46 * 60},
				{ID: 2, Duration: 44 * 60},
				{ID: 3, Duration: 5 * 60},
				{ID: 4, Duration: 3 * 60},
			},
			wantIDs:    []int{0, 1, 2},
			wantExtras: 2,
		},
		{
			name:           "double length pilot plus normal episodes and extras",
			minTitleLength: 120,
			titles: []ripspec.Title{
				{ID: 0, Duration: 91 * 60},
				{ID: 1, Duration: 45 * 60},
				{ID: 2, Duration: 45 * 60},
				{ID: 3, Duration: 5 * 60},
				{ID: 4, Duration: 4 * 60},
				{ID: 5, Duration: 2 * 60},
			},
			wantIDs:        []int{0, 1, 2},
			wantDoubleLong: 1,
			wantExtras:     3,
			wantReasonByID: map[int]string{0: "probable_double_episode_candidate"},
		},
		{
			name:           "combined playlist replaces split pilot halves when segment union matches",
			minTitleLength: 120,
			titles: []ripspec.Title{
				{ID: 0, Duration: 5464, SegmentMap: "1,2,64"},
				{ID: 1, Duration: 2730, SegmentMap: "2,64"},
				{ID: 2, Duration: 2734, SegmentMap: "1,64"},
				{ID: 3, Duration: 140, SegmentMap: "29,30"},
			},
			wantIDs:        []int{0},
			wantAmbiguous:  false,
			wantDoubleLong: 1,
			wantExtras:     3,
			wantReasonByID: map[int]string{0: "combined_double_episode_candidate", 1: "runtime_cluster_extra", 2: "runtime_cluster_extra", 3: "runtime_cluster_extra"},
		},
		{
			// TNG S1 D1 real disc layout: two viable double candidates
			// (multi-segment composite 00027.mpls and single-segment
			// precomposed 00040.mpls), both ~91min. The single-segment
			// playlist must win because seamless-branch composites have
			// been observed to silently fail during rip.
			name:           "prefers single-segment playlist when two doubles qualify",
			minTitleLength: 120,
			titles: []ripspec.Title{
				{ID: 0, Duration: 5464, SegmentCount: 3, SegmentMap: "1,2,64", Playlist: "00027.mpls"},
				{ID: 1, Duration: 2730, SegmentCount: 2, SegmentMap: "2,64", Playlist: "00042.mpls"},
				{ID: 2, Duration: 2734, SegmentCount: 2, SegmentMap: "1,64", Playlist: "00041.mpls"},
				{ID: 3, Duration: 5481, SegmentCount: 1, SegmentMap: "0", Playlist: "00040.mpls"},
				{ID: 4, Duration: 140, SegmentCount: 2, SegmentMap: "29,30", Playlist: "00013.mpls"},
			},
			wantIDs:        []int{3},
			wantAmbiguous:  false,
			wantDoubleLong: 2,
			wantExtras:     4,
			wantReasonByID: map[int]string{
				3: "combined_double_episode_candidate",
				0: "runtime_cluster_extra",
				1: "runtime_cluster_extra",
				2: "runtime_cluster_extra",
				4: "runtime_cluster_extra",
			},
		},
		{
			name:           "duplicate playlists dedupe by segment map",
			minTitleLength: 120,
			titles: []ripspec.Title{
				{ID: 0, Duration: 45 * 60, SegmentMap: "00001.m2ts"},
				{ID: 1, Duration: 45 * 60, SegmentMap: "00001.m2ts"},
				{ID: 2, Duration: 46 * 60, SegmentMap: "00002.m2ts"},
			},
			wantIDs:        []int{0, 2},
			wantDuplicates: 1,
			wantReasonByID: map[int]string{1: "duplicate_title"},
		},
		{
			name:           "mixed long form disc still prefers dominant runtime cluster",
			minTitleLength: 120,
			titles: []ripspec.Title{
				{ID: 0, Duration: 24 * 60},
				{ID: 1, Duration: 48 * 60},
				{ID: 2, Duration: 50 * 60},
				{ID: 3, Duration: 52 * 60},
			},
			wantIDs:    []int{1, 2, 3},
			wantExtras: 1,
		},
		{
			name:           "single long feature on tv hinted disc stays ambiguous",
			minTitleLength: 120,
			titles: []ripspec.Title{
				{ID: 0, Duration: 90 * 60},
				{ID: 1, Duration: 5 * 60},
				{ID: 2, Duration: 4 * 60},
			},
			wantIDs:       []int{0},
			wantAmbiguous: true,
			wantExtras:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectTVEpisodeTitles(tt.titles, tt.minTitleLength)
			if got.Ambiguous != tt.wantAmbiguous {
				t.Fatalf("Ambiguous = %v, want %v (reasons=%v)", got.Ambiguous, tt.wantAmbiguous, got.AmbiguityReasons)
			}
			if got.AmbiguousLongCount != tt.wantDoubleLong {
				t.Fatalf("AmbiguousLongCount = %d, want %d", got.AmbiguousLongCount, tt.wantDoubleLong)
			}
			if got.DuplicateCount != tt.wantDuplicates {
				t.Fatalf("DuplicateCount = %d, want %d", got.DuplicateCount, tt.wantDuplicates)
			}
			if got.ExtraCount != tt.wantExtras {
				t.Fatalf("ExtraCount = %d, want %d", got.ExtraCount, tt.wantExtras)
			}
			var gotIDs []int
			for _, title := range got.SelectedTitles {
				gotIDs = append(gotIDs, title.ID)
			}
			if !reflect.DeepEqual(gotIDs, tt.wantIDs) {
				t.Fatalf("SelectedTitles IDs = %v, want %v", gotIDs, tt.wantIDs)
			}
			for id, wantReason := range tt.wantReasonByID {
				found := false
				for _, decision := range got.Decisions {
					if decision.Title.ID == id {
						found = true
						if decision.Reason != wantReason {
							t.Fatalf("title %d reason = %q, want %q", id, decision.Reason, wantReason)
						}
					}
				}
				if !found {
					t.Fatalf("decision for title %d not found", id)
				}
			}
		})
	}
}

func TestCreateEpisodePlaceholders_TNGLikeSelection(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	h.cfg.MakeMKV.MinTitleLength = 120
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{SeasonNumber: 1},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 91*60 + 4, SegmentMap: "01000.m2ts"},
			{ID: 1, Duration: 45*60 + 30},
			{ID: 2, Duration: 45*60 + 34},
			{ID: 3, Duration: 91*60 + 21, SegmentMap: "01000.m2ts"},
			{ID: 4, Duration: 2*60 + 20},
			{ID: 5, Duration: 2*60 + 39},
			{ID: 6, Duration: 2*60 + 39},
			{ID: 7, Duration: 2*60 + 20},
			{ID: 8, Duration: 2*60 + 20},
			{ID: 9, Duration: 2*60 + 20},
			{ID: 10, Duration: 2*60 + 20},
			{ID: 11, Duration: 2*60 + 20},
			{ID: 12, Duration: 4*60 + 7},
			{ID: 13, Duration: 2*60 + 44},
			{ID: 14, Duration: 23*60 + 46},
			{ID: 15, Duration: 5 * 60},
		},
	}
	h.createEpisodePlaceholders(discardLogger(), env)

	if len(env.Episodes) != 3 {
		t.Fatalf("len(Episodes) = %d, want 3", len(env.Episodes))
	}
	wantTitleIDs := []int{0, 1, 2}
	for i, ep := range env.Episodes {
		if ep.TitleID != wantTitleIDs[i] {
			t.Fatalf("Episodes[%d].TitleID = %d, want %d", i, ep.TitleID, wantTitleIDs[i])
		}
	}
}
