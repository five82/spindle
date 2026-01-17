package drapto

import (
	"context"
	"testing"
)

func TestLibraryEncodeRequiresInput(t *testing.T) {
	lib := NewLibrary()
	if _, err := lib.Encode(context.Background(), "", "/tmp", EncodeOptions{}); err == nil {
		t.Fatal("expected error when input path is empty")
	}
}

func TestLibraryEncodeRequiresOutputDir(t *testing.T) {
	lib := NewLibrary()
	if _, err := lib.Encode(context.Background(), "/media/movie.mkv", "", EncodeOptions{}); err == nil {
		t.Fatal("expected error when output directory is empty")
	}
}

func TestLibraryEncodeWhitespaceOutputDir(t *testing.T) {
	lib := NewLibrary()
	if _, err := lib.Encode(context.Background(), "/media/movie.mkv", "   ", EncodeOptions{}); err == nil {
		t.Fatal("expected error when output directory is whitespace")
	}
}

func TestLibraryImplementsClient(t *testing.T) {
	var _ Client = (*Library)(nil)
}
