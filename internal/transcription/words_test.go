package transcription

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAlignedWords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio.json")
	payload := `{"language":"en","segments":[
		{"start":1,"end":3,"text":"Hello, world","words":[
			{"word":"Hello,","start":1.0,"end":1.4,"score":0.91},
			{"word":"world","start":1.5,"end":1.9,"score":0.88}
		]},
		{"start":4,"end":6,"text":"42 again","words":[
			{"word":"42"},
			{"word":"again","start":4.2,"end":4.6,"score":0.95}
		]}
	]}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	words, err := ReadAlignedWords(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Word{
		{Text: "Hello,", Start: 1.0, End: 1.4},
		{Text: "world", Start: 1.5, End: 1.9},
		{Text: "again", Start: 4.2, End: 4.6},
	}
	if len(words) != len(want) {
		t.Fatalf("words = %+v, want %d entries", words, len(want))
	}
	for i, w := range want {
		if words[i] != w {
			t.Fatalf("words[%d] = %+v, want %+v", i, words[i], w)
		}
	}
}

func TestReadAlignedWordsErrors(t *testing.T) {
	if _, err := ReadAlignedWords(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
	path := filepath.Join(t.TempDir(), "audio.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAlignedWords(path); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestReadAlignedWordsEmptyPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	words, err := ReadAlignedWords(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 0 {
		t.Fatalf("words = %+v, want none", words)
	}
}
