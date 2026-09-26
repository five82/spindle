package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFindVideoFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"z.MP4", "B.mkv", "a.mkv", ".hidden.mkv", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested.mkv"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err := FindVideoFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "a.mkv"), filepath.Join(dir, "B.mkv"), filepath.Join(dir, "z.MP4")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindVideoFiles() = %v, want %v", got, want)
	}
}

func TestFindVideoFilesErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.mkv")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(dir, "missing"), "directory does not exist"},
		{file, "not a directory"},
		{t.TempDir(), "no video files found"},
	} {
		_, err := FindVideoFiles(tc.path)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("FindVideoFiles(%q) error = %v, want %q", tc.path, err, tc.want)
		}
	}
}
