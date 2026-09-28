package ripper

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestEpisodeMappingMissingDirectoryAndUnknownTitle(t *testing.T) {
	env := &ripspec.Envelope{Episodes: []ripspec.Episode{{Key: "unknown", TitleID: -1}, {Key: "one", TitleID: 1}}}
	missingDir := filepath.Join(t.TempDir(), "missing")
	if files, missing := cacheHasAllEpisodeFiles(env, missingDir); files != nil || !reflect.DeepEqual(missing, []string{"unknown", "one"}) {
		t.Fatalf("missing directory: %v %v", files, missing)
	}
	if result := assignEpisodeAssets(env, missingDir, nil, nil); result.Assigned != 0 || len(env.Assets.Ripped) != 0 {
		t.Fatalf("scan error: %+v, %+v", result, env.Assets)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Show_t01.MKV"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "Show_t02.mkv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Show_t03.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unmatched.mkv"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	files, missing := cacheHasAllEpisodeFiles(env, dir)
	if !reflect.DeepEqual(missing, []string{"unknown"}) || len(files) != 1 {
		t.Fatalf("cache: %v %v", files, missing)
	}
	result := assignEpisodeAssets(env, dir, files, nil)
	if result.Assigned != 1 || !reflect.DeepEqual(result.Missing, []string{"unknown"}) || env.Assets.Ripped[0].Path != files[1] {
		t.Fatalf("mapping: %+v, %+v", result, env.Assets)
	}
	if err := assignMovieAssets(&ripspec.Envelope{}, missingDir); err == nil {
		t.Fatal("missing movie directory should fail")
	}
	if files, missing := cacheHasAllEpisodeFiles(nil, dir); files != nil || missing != nil {
		t.Fatalf("nil envelope: %v %v", files, missing)
	}
}
