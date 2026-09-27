package util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFilePathsAndExistence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "Movie.MKV")
	if err := os.WriteFile(file, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want bool
	}{{file, true}, {filepath.Join(dir, "movie.txt"), false}, {dir, false}, {filepath.Join(dir, "missing.mkv"), false}} {
		if got := IsVideoFile(tc.path); got != tc.want {
			t.Errorf("IsVideoFile(%q) = %v", tc.path, got)
		}
	}
	if GetFilename(file) != "Movie.MKV" || GetFileStem(file) != "Movie" {
		t.Fatal("filename/stem")
	}
	if size, err := GetFileSize(file); err != nil || size != 3 {
		t.Fatalf("file size %d: %v", size, err)
	}
	if _, err := GetFileSize(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file size must fail")
	}
	nested := filepath.Join(dir, "a", "b")
	if err := EnsureDirectory(nested); err != nil {
		t.Fatal(err)
	}
	if !DirectoryExists(nested) || DirectoryExists(file) || DirectoryExists(filepath.Join(dir, "missing")) {
		t.Fatal("directory detection")
	}
	if !FileExists(file) || FileExists(dir) || FileExists(filepath.Join(dir, "missing")) {
		t.Fatal("file detection")
	}
	if got := ResolveOutputPath(file, dir, ""); got != filepath.Join(dir, "Movie.mkv") {
		t.Fatalf("default output: %q", got)
	}
	if got := ResolveOutputPath(file, dir, "custom.mkv"); got != filepath.Join(dir, "custom.mkv") {
		t.Fatalf("override: %q", got)
	}
}

func TestResolveOutputArg(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "input.mkv")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		in, out string
		want    OutputPathInfo
		invalid bool
	}{
		{file, filepath.Join(dir, "out.MKV"), OutputPathInfo{dir, "out.MKV"}, false},
		{file, filepath.Join(dir, "out.avi"), OutputPathInfo{}, true},
		{file, filepath.Join(dir, "output"), OutputPathInfo{filepath.Join(dir, "output"), ""}, false},
		{dir, filepath.Join(dir, "out.mkv"), OutputPathInfo{filepath.Join(dir, "out.mkv"), ""}, false},
	} {
		got, err := ResolveOutputArg(tc.in, tc.out)
		if tc.invalid {
			if !errors.Is(err, os.ErrInvalid) {
				t.Errorf("%q: expected invalid output, got %v", tc.out, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q: got %+v, %v want %+v", tc.out, got, err, tc.want)
		}
	}
	if _, err := ResolveOutputArg(filepath.Join(dir, "missing"), "out.mkv"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing input: %v", err)
	}
}
