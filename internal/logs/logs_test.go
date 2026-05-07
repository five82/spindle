package logs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTailReturnsLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spindle.log")
	content := "one\ntwo\nthree\nfour\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	got, err := Tail(path, 2)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	want := []string{"three", "four"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tail = %#v, want %#v", got, want)
	}
}

func TestTailReturnsAllLinesWhenLimitExceedsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spindle.log")
	content := "one\ntwo\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	got, err := Tail(path, 10)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	want := []string{"one", "two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tail = %#v, want %#v", got, want)
	}
}
