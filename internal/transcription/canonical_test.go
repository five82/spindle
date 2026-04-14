package transcription

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildCanonicalSegments_GroupsByPauseAndPunctuation(t *testing.T) {
	segments := buildCanonicalSegments("", []workerTimeStamp{
		{Text: "Hello", StartTime: 0.0, EndTime: 0.3},
		{Text: "world.", StartTime: 0.31, EndTime: 0.7},
		{Text: "Next", StartTime: 1.5, EndTime: 1.8},
		{Text: "line", StartTime: 1.81, EndTime: 2.1},
	})
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}
	if segments[0].Text != "Hello world." {
		t.Fatalf("first segment text = %q", segments[0].Text)
	}
	if segments[1].Text != "Next line" {
		t.Fatalf("second segment text = %q", segments[1].Text)
	}
}

func TestBuildCanonicalSegments_FallsBackToSyntheticSegment(t *testing.T) {
	segments := buildCanonicalSegments("Hello there general Kenobi", nil)
	if len(segments) != 1 {
		t.Fatalf("expected 1 synthetic segment, got %d", len(segments))
	}
	if segments[0].Text != "Hello there general Kenobi" {
		t.Fatalf("unexpected synthetic text %q", segments[0].Text)
	}
	if segments[0].End <= segments[0].Start {
		t.Fatalf("expected positive synthetic duration, got start=%.2f end=%.2f", segments[0].Start, segments[0].End)
	}
}

func TestBuildTranscriptArtifacts_WritesCanonicalFiles(t *testing.T) {
	dir := t.TempDir()
	artifacts, err := buildTranscriptArtifacts(dir, "en", &workerTranscribeResponse{
		Language: "English",
		Text:     "Hello world.",
		TimeStamps: []workerTimeStamp{
			{Text: "Hello", StartTime: 0.0, EndTime: 0.4},
			{Text: "world.", StartTime: 0.41, EndTime: 0.9},
		},
	})
	if err != nil {
		t.Fatalf("buildTranscriptArtifacts() error = %v", err)
	}
	if artifacts.SRTPath == "" || artifacts.JSONPath == "" {
		t.Fatal("expected artifact paths")
	}
	if artifacts.Segments != 1 {
		t.Fatalf("expected 1 segment, got %d", artifacts.Segments)
	}
	jsonData, err := os.ReadFile(filepath.Join(dir, "audio.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload canonicalPayload
	if err := json.Unmarshal(jsonData, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != canonicalSchemaVersion {
		t.Fatalf("schema_version = %q", payload.SchemaVersion)
	}
	if payload.Language != "en" {
		t.Fatalf("language = %q", payload.Language)
	}
	if len(payload.Segments) != 1 || payload.Segments[0].Text != "Hello world." {
		t.Fatalf("unexpected payload segments: %+v", payload.Segments)
	}
}

func TestSupportsAlignedLanguage(t *testing.T) {
	if !supportsAlignedLanguage("en") {
		t.Fatal("expected en to be supported")
	}
	if supportsAlignedLanguage("sv") {
		t.Fatal("expected sv to be unsupported")
	}
}

func TestJoinWords_InsertsSpacesBetweenBareTokens(t *testing.T) {
	text := joinWords([]canonicalWord{{Word: "Captain"}, {Word: "Locke"}, {Word: "started"}})
	if text != "Captain Locke started" {
		t.Fatalf("joinWords() = %q", text)
	}
}

func TestBuildCanonicalSegments_ProjectsPunctuationFromRawText(t *testing.T) {
	segments := buildCanonicalSegments("Captain's log, Stardate four one three eight six point four.", []workerTimeStamp{
		{Text: "Captain's", StartTime: 0.0, EndTime: 0.2},
		{Text: "log", StartTime: 0.2, EndTime: 0.4},
		{Text: "Stardate", StartTime: 0.8, EndTime: 1.1},
		{Text: "four", StartTime: 1.1, EndTime: 1.3},
		{Text: "one", StartTime: 1.3, EndTime: 1.5},
		{Text: "three", StartTime: 1.5, EndTime: 1.7},
		{Text: "eight", StartTime: 1.7, EndTime: 1.9},
		{Text: "six", StartTime: 1.9, EndTime: 2.1},
		{Text: "point", StartTime: 2.1, EndTime: 2.3},
		{Text: "four", StartTime: 2.3, EndTime: 2.5},
	})
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	if segments[0].Text != "Captain's log, Stardate four one three eight six point four." {
		t.Fatalf("segment text = %q", segments[0].Text)
	}
}
