package contentid

import (
	"context"
	"fmt"
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
)

func TestVerifyMatchesFailureAndRejectionPaths(t *testing.T) {
	rip := writeTestSRT(t, "1\n00:00:01,000 --> 00:00:02,000\nFirst dialogue\n")
	ref := writeTestSRT(t, "1\n00:00:01,000 --> 00:00:02,000\nSecond dialogue\n")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name, response, ripPath, refPath string
		accepted                         []matchResult
		wantFailed, wantRejected         int
	}{
		{name: "missing rip", ripPath: "", refPath: ref, wantFailed: 1},
		{name: "bad rip", ripPath: filepath.Join(t.TempDir(), "absent.srt"), refPath: ref, wantFailed: 1},
		{name: "bad reference", ripPath: rip, refPath: filepath.Join(t.TempDir(), "absent.srt"), wantFailed: 1},
		{name: "LLM failure", ripPath: rip, refPath: ref, response: `{}`, wantFailed: 1},
		{name: "LLM rejects", ripPath: rip, refPath: ref, response: `{"choices":[{"message":{"content":"{\"same_episode\":false,\"explanation\":\"different scene\"}"}}]}`, wantRejected: 1},
		{name: "accepted target skipped", ripPath: rip, refPath: ref, accepted: []matchResult{{EpisodeKey: "already", TargetEpisode: 7}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.response) }))
			defer server.Close()
			client := llm.New(config.LLMConfig{APIKey: "key", BaseURL: server.URL, Model: "test", TimeoutSeconds: 2}, nil)
			candidate := matchResult{EpisodeKey: "pending", TargetEpisode: 7, Strength: 0.9}
			got, remaining, result := verifyMatches(context.Background(), client, tc.accepted, map[string][]matchResult{"pending": {candidate}}, []ripFingerprint{{EpisodeKey: "pending", Path: tc.ripPath}}, []referenceFingerprint{{EpisodeNumber: 7, CachePath: tc.refPath}}, logger)
			if result == nil || result.Failed != tc.wantFailed || result.Rejected != tc.wantRejected || len(got) != len(tc.accepted) {
				t.Fatalf("matches=%v result=%+v", got, result)
			}
			if len(remaining["pending"]) != 0 {
				t.Fatalf("candidate not removed: %+v", remaining)
			}
			if result.NeedsReview != (tc.wantFailed+tc.wantRejected > 0) {
				t.Fatalf("review=%+v", result)
			}
		})
	}
}

func TestVerificationQueueOrderingAndTranscriptErrors(t *testing.T) {
	pending := map[string][]matchResult{"b": {{Strength: 0.8, TargetEpisode: 1}, {TargetEpisode: 2}, {TargetEpisode: 3}, {TargetEpisode: 4}}, "a": {{Strength: 0.8}}, "c": nil}
	queue := verificationQueue(pending, nil)
	if len(queue) != 3 || queue[0].EpisodeKey != "a" || queue[1].EpisodeKey != "b" || queue[2].EpisodeKey != "c" || len(queue[1].Candidates) != maxVerificationCandidatesPerRip {
		t.Fatalf("queue: %+v", queue)
	}
	copy := clonePendingByRip(pending)
	copy["a"][0].Strength = 0
	if pending["a"][0].Strength != 0.8 {
		t.Fatal("clone aliases input")
	}
	if clonePendingByRip(nil) != nil || removeCandidateEpisode(nil, 1) != nil {
		t.Fatal("expected nil")
	}
	if got := removeCandidateEpisode([]matchResult{{TargetEpisode: 1}, {TargetEpisode: 2}}, 1); len(got) != 1 || got[0].TargetEpisode != 2 {
		t.Fatalf("remove: %+v", got)
	}
	if len(verificationQueue(pending, map[string]struct{}{"b": {}})) != 2 {
		t.Fatal("accepted rip not excluded")
	}
	for _, text := range []string{"", "not an SRT", "1\n00:00:01,000 --> 00:00:02,000\n   \n"} {
		path := filepath.Join(t.TempDir(), "input.srt")
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := extractMiddleTranscript(path); err == nil {
			t.Fatalf("accepted empty transcript %q", text)
		}
	}
	if _, err := extractMiddleTranscript(filepath.Join(t.TempDir(), "missing.srt")); err == nil {
		t.Fatal("missing SRT succeeded")
	}
	long := strings.Repeat("dialogue ", 1000)
	path := writeTestSRT(t, fmt.Sprintf("1\n00:00:01,000 --> 00:00:02,000\n%s\n", long))
	if text, err := extractMiddleTranscript(path); err != nil || len(text) != maxTranscriptChars {
		t.Fatalf("truncation: %d %v", len(text), err)
	}
}
