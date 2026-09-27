package audioanalysis

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestClassifyTrackResultsAndMissingTranscript(t *testing.T) {
	response := `{"answers":{"decision":{"type":"choice","choice":"commentary","confidence":0.1,"probabilities":{"commentary":0.9,"not_commentary":0.1}}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	h := New(cfg, llm.New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, logger), nil)
	stream := ffprobe.Stream{Index: 1, Tags: map[string]string{"title": "Commentary"}}
	ref := h.classifyTrack(context.Background(), logger, 1, stream, "main", "The director describes a scene", true)
	if ref == nil || ref.Index != 1 || ref.Confidence != .9 {
		t.Fatalf("commentary: %+v", ref)
	}
	response = `{"answers":{"decision":{"type":"choice","choice":"not_commentary","confidence":0.9,"probabilities":{"commentary":0.01,"not_commentary":0.99}}}}`
	if ref := h.classifyTrack(context.Background(), logger, 1, stream, "main", "ordinary dialogue", true); ref != nil {
		t.Fatalf("program audio: %+v", ref)
	}
	ref = h.classifyTrack(context.Background(), logger, 1, stream, "main", "", false)
	if ref == nil || !strings.Contains(ref.Reason, "transcription failed") {
		t.Fatalf("conservative fallback: %+v", ref)
	}
}

func TestDetectCommentaryPreservesUntranscribedEnglish(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{}"}}]}`)
	}))
	defer server.Close()
	h := New(&config.Config{}, llm.New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, logger), nil)
	sess := &stage.Session{Item: &queue.Item{}, Env: &ripspec.Envelope{}, Logger: logger}
	result := &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video"}, {CodecType: "audio", Channels: 6, Tags: map[string]string{"language": "eng"}}, {CodecType: "audio", Channels: 2, Tags: map[string]string{"language": "eng"}}}}
	comms, excluded := h.detectCommentary(context.Background(), sess, result, "/missing.mkv", "fp", "main")
	if len(comms) != 1 || comms[0].Index != 1 || len(excluded) != 0 {
		t.Fatalf("comms=%+v excluded=%+v", comms, excluded)
	}
}

func TestPrimaryFingerprintReusesTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.srt")
	if err := os.WriteFile(path, []byte("1\n00:00:01,000 --> 00:00:02,000\nOriginal dialogue here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := &stage.Session{Item: &queue.Item{}, Env: &ripspec.Envelope{Assets: ripspec.Assets{Transcript: []ripspec.Asset{{EpisodeKey: "main", Path: path, Status: ripspec.AssetStatusCompleted}}}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if fp := New(&config.Config{}, nil, nil).primaryFingerprint(context.Background(), sess, "unused", 0, "main"); fp == nil {
		t.Fatal("transcript not reused")
	}
	if fp := New(&config.Config{}, nil, nil).primaryFingerprint(context.Background(), sess, "unused", 0, "missing"); fp != nil {
		t.Fatal("missing transcript fabricated")
	}
}
