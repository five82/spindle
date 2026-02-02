package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMapLanguageCode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"en", "eng"},
		{"EN", "eng"},
		{"es", "spa"},
		{"fr", "fre"},
		{"de", "deu"},
		{"it", "ita"},
		{"pt", "por"},
		{"ja", "jpn"},
		{"ko", "kor"},
		{"zh", "zho"},
		{"ru", "rus"},
		{"ar", "ara"},
		{"hi", "hin"},
		{"nl", "nld"},
		{"pl", "pol"},
		{"sv", "swe"},
		{"da", "dan"},
		{"no", "nor"},
		{"fi", "fin"},
		{"eng", "eng"}, // already 3-letter
		{"spa", "spa"},
		{"xyz", "xyz"}, // unknown 3-letter passes through
		{"xy", "und"},  // unknown 2-letter becomes undefined
		{"", "und"},    // empty
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := mapLanguageCode(tt.input)
			if result != tt.expected {
				t.Errorf("mapLanguageCode(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestLanguageDisplayName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"en", "English"},
		{"eng", "English"},
		{"es", "Spanish"},
		{"spa", "Spanish"},
		{"fr", "French"},
		{"de", "German"},
		{"ja", "Japanese"},
		{"ko", "Korean"},
		{"zh", "Chinese"},
		{"", "Unknown"},
		{"xyz", "XYZ"}, // unknown returns uppercase
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := languageDisplayName(tt.input)
			if result != tt.expected {
				t.Errorf("languageDisplayName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestBuildTrackName(t *testing.T) {
	tests := []struct {
		lang     string
		forced   bool
		expected string
	}{
		{"en", false, "English"},
		{"en", true, "English (Forced)"},
		{"es", false, "Spanish"},
		{"es", true, "Spanish (Forced)"},
		{"", false, "Unknown"},
		{"", true, "Unknown (Forced)"},
	}

	for _, tt := range tests {
		name := "lang=" + tt.lang
		if tt.forced {
			name += "_forced"
		}
		t.Run(name, func(t *testing.T) {
			result := buildTrackName(tt.lang, tt.forced)
			if result != tt.expected {
				t.Errorf("buildTrackName(%q, %v) = %q, want %q", tt.lang, tt.forced, result, tt.expected)
			}
		})
	}
}

func TestIsForcesSRT(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"/path/to/video.en.srt", false},
		{"/path/to/video.en.forced.srt", true},
		{"/path/to/video.forced.en.srt", true},
		{"/path/to/VIDEO.EN.FORCED.SRT", true},
		{"/path/to/video.srt", false},
		{"video.forced.srt", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isForcesSRT(tt.path)
			if result != tt.expected {
				t.Errorf("isForcesSRT(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestMuxer_BuildMkvmergeArgs(t *testing.T) {
	muxer := NewMuxer(nil)

	t.Run("single subtitle", func(t *testing.T) {
		req := MuxRequest{
			MKVPath:           "/path/to/video.mkv",
			SubtitlePaths:     []string{"/path/to/video.en.srt"},
			Language:          "en",
			StripExistingSubs: true,
		}
		args := muxer.buildMkvmergeArgs(req, "/tmp/output.mkv")

		// Verify output flag
		if args[0] != "-o" || args[1] != "/tmp/output.mkv" {
			t.Errorf("expected output flag, got %v", args[:2])
		}

		// Verify strip flag
		if args[2] != "-S" {
			t.Errorf("expected -S flag, got %s", args[2])
		}

		// Verify source MKV
		if args[3] != "/path/to/video.mkv" {
			t.Errorf("expected source MKV, got %s", args[3])
		}

		// Verify language flag
		found := false
		for i, arg := range args {
			if arg == "--language" && i+1 < len(args) && args[i+1] == "0:eng" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected --language 0:eng in args: %v", args)
		}

		// Verify default-track flag for non-forced
		found = false
		for i, arg := range args {
			if arg == "--default-track" && i+1 < len(args) && args[i+1] == "0:yes" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected --default-track 0:yes in args: %v", args)
		}
	})

	t.Run("with forced subtitle", func(t *testing.T) {
		req := MuxRequest{
			MKVPath:           "/path/to/video.mkv",
			SubtitlePaths:     []string{"/path/to/video.en.srt", "/path/to/video.en.forced.srt"},
			Language:          "en",
			StripExistingSubs: false,
		}
		args := muxer.buildMkvmergeArgs(req, "/tmp/output.mkv")

		// Verify no -S flag
		for _, arg := range args {
			if arg == "-S" {
				t.Errorf("unexpected -S flag when StripExistingSubs is false")
			}
		}

		// Verify forced-track flag
		found := false
		for i, arg := range args {
			if arg == "--forced-track" && i+1 < len(args) && args[i+1] == "0:yes" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected --forced-track 0:yes in args: %v", args)
		}
	})

	t.Run("no strip flag", func(t *testing.T) {
		req := MuxRequest{
			MKVPath:           "/path/to/video.mkv",
			SubtitlePaths:     []string{"/path/to/video.en.srt"},
			Language:          "en",
			StripExistingSubs: false,
		}
		args := muxer.buildMkvmergeArgs(req, "/tmp/output.mkv")

		for _, arg := range args {
			if arg == "-S" {
				t.Errorf("unexpected -S flag when StripExistingSubs is false")
			}
		}
	})
}

func TestMuxer_MuxSubtitles_Validation(t *testing.T) {
	muxer := NewMuxer(nil)

	t.Run("nil muxer", func(t *testing.T) {
		var nilMuxer *Muxer
		_, err := nilMuxer.MuxSubtitles(context.Background(), MuxRequest{})
		if err == nil {
			t.Error("expected error for nil muxer")
		}
	})

	t.Run("empty MKV path", func(t *testing.T) {
		_, err := muxer.MuxSubtitles(context.Background(), MuxRequest{
			SubtitlePaths: []string{"/path/to/sub.srt"},
		})
		if err == nil {
			t.Error("expected error for empty MKV path")
		}
	})

	t.Run("empty subtitle paths", func(t *testing.T) {
		_, err := muxer.MuxSubtitles(context.Background(), MuxRequest{
			MKVPath: "/path/to/video.mkv",
		})
		if err == nil {
			t.Error("expected error for empty subtitle paths")
		}
	})

	t.Run("non-existent MKV", func(t *testing.T) {
		_, err := muxer.MuxSubtitles(context.Background(), MuxRequest{
			MKVPath:       "/nonexistent/video.mkv",
			SubtitlePaths: []string{"/path/to/sub.srt"},
		})
		if err == nil {
			t.Error("expected error for non-existent MKV")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected 'not found' in error, got: %v", err)
		}
	})
}

func TestMuxer_MuxSubtitles_WithMockRunner(t *testing.T) {
	// Create temp files for testing
	tmpDir := t.TempDir()
	mkvPath := filepath.Join(tmpDir, "video.mkv")
	srtPath := filepath.Join(tmpDir, "video.en.srt")

	// Create dummy MKV and SRT files
	if err := os.WriteFile(mkvPath, []byte("dummy mkv content"), 0644); err != nil {
		t.Fatalf("failed to create test MKV: %v", err)
	}
	if err := os.WriteFile(srtPath, []byte("1\n00:00:01,000 --> 00:00:02,000\nTest\n"), 0644); err != nil {
		t.Fatalf("failed to create test SRT: %v", err)
	}

	t.Run("successful mux", func(t *testing.T) {
		muxer := NewMuxer(nil)
		called := false
		muxer.WithCommandRunner(func(ctx context.Context, name string, args ...string) error {
			called = true
			// Verify mkvmerge command
			if name != "mkvmerge" {
				t.Errorf("expected mkvmerge command, got %s", name)
			}
			// Verify output file
			if len(args) < 2 || args[0] != "-o" {
				t.Error("expected -o flag")
			}
			// Create the output file to simulate mkvmerge success
			if len(args) >= 2 {
				if err := os.WriteFile(args[1], []byte("muxed content"), 0644); err != nil {
					return err
				}
			}
			return nil
		})

		result, err := muxer.MuxSubtitles(context.Background(), MuxRequest{
			MKVPath:           mkvPath,
			SubtitlePaths:     []string{srtPath},
			Language:          "en",
			StripExistingSubs: true,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !called {
			t.Error("mkvmerge command was not called")
		}
		if result.OutputPath != mkvPath {
			t.Errorf("expected output path %s, got %s", mkvPath, result.OutputPath)
		}
		if len(result.MuxedSubtitles) != 1 {
			t.Errorf("expected 1 muxed subtitle, got %d", len(result.MuxedSubtitles))
		}
		// SRT should be removed after successful mux
		if _, err := os.Stat(srtPath); err == nil {
			t.Error("SRT file should have been removed after muxing")
		}
	})
}
