package contentid

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/logging"
)

// mockLLMVerifier is a test double that returns canned JSON responses.
type mockLLMVerifier struct {
	responses []string // returned in order; wraps around
	calls     int
	err       error // if set, all calls return this error
}

func (m *mockLLMVerifier) CompleteJSON(_ context.Context, _, _ string) (string, error) {
	if m.err != nil {
		m.calls++
		return "", m.err
	}
	idx := m.calls % len(m.responses)
	m.calls++
	return m.responses[idx], nil
}

// writeSRT creates a temp SRT file and returns its path.
func writeSRT(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// testSRTContent is a minimal SRT with dialogue spanning 30 minutes.
const testSRTContent = `1
00:01:00,000 --> 00:01:05,000
Opening dialogue.

2
00:10:00,000 --> 00:10:05,000
Middle section line A.

3
00:15:00,000 --> 00:15:05,000
Middle section line B.

4
00:20:00,000 --> 00:20:05,000
More middle content.

5
00:29:00,000 --> 00:29:05,000
Closing dialogue.
`

func makeTestFixtures(t *testing.T) ([]ripFingerprint, []referenceFingerprint) {
	t.Helper()
	dir := t.TempDir()
	ripPath1 := writeSRT(t, dir, "rip_s01e01.srt", testSRTContent)
	ripPath2 := writeSRT(t, dir, "rip_s01e02.srt", testSRTContent)
	ripPath3 := writeSRT(t, dir, "rip_s01e03.srt", testSRTContent)
	refPath1 := writeSRT(t, dir, "ref_ep1.srt", testSRTContent)
	refPath2 := writeSRT(t, dir, "ref_ep2.srt", testSRTContent)
	refPath3 := writeSRT(t, dir, "ref_ep3.srt", testSRTContent)

	rips := []ripFingerprint{
		{EpisodeKey: "s01e01", TitleID: 1, Path: ripPath1},
		{EpisodeKey: "s01e02", TitleID: 2, Path: ripPath2},
		{EpisodeKey: "s01e03", TitleID: 3, Path: ripPath3},
	}
	refs := []referenceFingerprint{
		{EpisodeNumber: 1, CachePath: refPath1, FileID: 100, Language: "en"},
		{EpisodeNumber: 2, CachePath: refPath2, FileID: 200, Language: "en"},
		{EpisodeNumber: 3, CachePath: refPath3, FileID: 300, Language: "en"},
	}
	return rips, refs
}

func TestVerifyMatches_HighConfidenceSkipsLLM(t *testing.T) {
	mock := &mockLLMVerifier{responses: []string{`{"same_episode":true,"confidence":0.95,"explanation":"match"}`}}
	logger := logging.NewNop()

	matches := []matchResult{
		{EpisodeKey: "s01e01", TargetEpisode: 1, Score: 0.90},
		{EpisodeKey: "s01e02", TargetEpisode: 2, Score: 0.92},
	}
	rips, refs := makeTestFixtures(t)

	result, vr := verifyMatches(context.Background(), mock, matches, rips, refs, logger)

	if mock.calls != 0 {
		t.Fatalf("expected 0 LLM calls for high-confidence matches, got %d", mock.calls)
	}
	if vr != nil {
		t.Fatalf("expected nil verifyResult, got %+v", vr)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 matches returned, got %d", len(result))
	}
}

func TestVerifyMatches_NilClient(t *testing.T) {
	matches := []matchResult{
		{EpisodeKey: "s01e01", TargetEpisode: 1, Score: 0.75},
	}

	result, vr := verifyMatches(context.Background(), nil, matches, nil, nil, logging.NewNop())
	if vr != nil {
		t.Fatal("expected nil verifyResult for nil client")
	}
	if len(result) != 1 {
		t.Fatal("expected original matches returned")
	}
}

func TestVerifyMatches_SingleRejectionNeedsReview(t *testing.T) {
	mock := &mockLLMVerifier{responses: []string{
		`{"same_episode":false,"confidence":0.3,"explanation":"different scenes"}`,
	}}
	rips, refs := makeTestFixtures(t)

	matches := []matchResult{
		{EpisodeKey: "s01e01", TargetEpisode: 1, Score: 0.90}, // above threshold
		{EpisodeKey: "s01e02", TargetEpisode: 2, Score: 0.80}, // below threshold
		{EpisodeKey: "s01e03", TargetEpisode: 3, Score: 0.90}, // above threshold
	}

	result, vr := verifyMatches(context.Background(), mock, matches, rips, refs, logging.NewNop())

	if mock.calls != 1 {
		t.Fatalf("expected 1 LLM call, got %d", mock.calls)
	}
	if vr == nil {
		t.Fatal("expected non-nil verifyResult")
	}
	if !vr.NeedsReview {
		t.Fatal("expected NeedsReview for single rejection")
	}
	if vr.Rejected != 1 {
		t.Fatalf("expected 1 rejection, got %d", vr.Rejected)
	}
	// Original matches should be preserved (not rematched with only 1 rejection).
	if result[1].TargetEpisode != 2 {
		t.Fatalf("expected original match preserved, got target %d", result[1].TargetEpisode)
	}
}

func TestVerifyMatches_TwoRejectionsAttemptRematch(t *testing.T) {
	// First two calls reject, then cross-match calls accept swapped assignments.
	mock := &mockLLMVerifier{responses: []string{
		// Initial verifications: both rejected
		`{"same_episode":false,"confidence":0.3,"explanation":"wrong episode"}`,
		`{"same_episode":false,"confidence":0.2,"explanation":"wrong episode"}`,
		// Cross-match: s01e01 vs ep2 -> yes, s01e01 vs ep1 -> no,
		// s01e02 vs ep2 -> no, s01e02 vs ep1 -> yes
		// Order: (cand0,ref0), (cand0,ref1), (cand1,ref0), (cand1,ref1)
		// We want s01e01->ep2 and s01e02->ep1
		`{"same_episode":false,"confidence":0.2,"explanation":"no"}`,
		`{"same_episode":true,"confidence":0.9,"explanation":"yes swapped"}`,
		`{"same_episode":true,"confidence":0.85,"explanation":"yes swapped"}`,
		`{"same_episode":false,"confidence":0.2,"explanation":"no"}`,
	}}
	rips, refs := makeTestFixtures(t)

	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 1, Score: 0.80},
		{EpisodeKey: "s01e02", TitleID: 2, TargetEpisode: 2, Score: 0.78},
		{EpisodeKey: "s01e03", TitleID: 3, TargetEpisode: 3, Score: 0.90}, // above threshold
	}

	result, vr := verifyMatches(context.Background(), mock, matches, rips, refs, logging.NewNop())

	if vr == nil {
		t.Fatal("expected non-nil verifyResult")
	}
	if vr.Rejected != 2 {
		t.Fatalf("expected 2 rejections, got %d", vr.Rejected)
	}
	if vr.Rematched < 1 {
		t.Fatalf("expected at least 1 rematch, got %d", vr.Rematched)
	}
	// High-confidence match should be untouched.
	if result[2].TargetEpisode != 3 {
		t.Fatalf("expected high-confidence match preserved, got target %d", result[2].TargetEpisode)
	}
}

func TestVerifyMatches_AllRejectedRematchNeedsReview(t *testing.T) {
	// All initial verifications rejected, and all cross-match combinations also rejected.
	mock := &mockLLMVerifier{responses: []string{
		`{"same_episode":false,"confidence":0.1,"explanation":"no match"}`,
	}}
	rips, refs := makeTestFixtures(t)

	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 1, Score: 0.80},
		{EpisodeKey: "s01e02", TitleID: 2, TargetEpisode: 2, Score: 0.78},
	}

	_, vr := verifyMatches(context.Background(), mock, matches, rips, refs, logging.NewNop())

	if vr == nil {
		t.Fatal("expected non-nil verifyResult")
	}
	if !vr.NeedsReview {
		t.Fatal("expected NeedsReview when all combinations rejected")
	}
	if vr.ReviewReason == "" {
		t.Fatal("expected non-empty ReviewReason")
	}
}

func TestVerifyMatches_LLMErrorGracefulDegradation(t *testing.T) {
	mock := &mockLLMVerifier{err: fmt.Errorf("API timeout")}
	rips, refs := makeTestFixtures(t)

	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 1, Score: 0.80},
	}

	result, vr := verifyMatches(context.Background(), mock, matches, rips, refs, logging.NewNop())

	if vr == nil {
		t.Fatal("expected non-nil verifyResult")
	}
	if !vr.NeedsReview {
		t.Fatal("expected NeedsReview on LLM error")
	}
	// Original match should be preserved.
	if result[0].TargetEpisode != 1 {
		t.Fatalf("expected original match preserved, got target %d", result[0].TargetEpisode)
	}
}

func TestVerifyMatches_VerifiedMatchesConfirmed(t *testing.T) {
	mock := &mockLLMVerifier{responses: []string{
		`{"same_episode":true,"confidence":0.95,"explanation":"same scenes throughout"}`,
	}}
	rips, refs := makeTestFixtures(t)

	matches := []matchResult{
		{EpisodeKey: "s01e01", TitleID: 1, TargetEpisode: 1, Score: 0.82},
	}

	result, vr := verifyMatches(context.Background(), mock, matches, rips, refs, logging.NewNop())

	if vr == nil {
		t.Fatal("expected non-nil verifyResult")
	}
	if vr.Verified != 1 {
		t.Fatalf("expected 1 verified, got %d", vr.Verified)
	}
	if vr.NeedsReview {
		t.Fatal("should not need review when LLM confirms")
	}
	if result[0].TargetEpisode != 1 {
		t.Fatalf("expected match unchanged, got target %d", result[0].TargetEpisode)
	}
}

func TestExtractMiddleTranscript(t *testing.T) {
	dir := t.TempDir()
	path := writeSRT(t, dir, "test.srt", testSRTContent)

	text, err := extractMiddleTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if text == "" {
		t.Fatal("expected non-empty transcript")
	}
}

func TestBuildVerificationPrompt(t *testing.T) {
	prompt := buildVerificationPrompt("hello world", "hello world ref", "s01e05", 5)
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	// Should contain both transcript markers.
	if !strings.Contains(prompt, "TRANSCRIPT A") || !strings.Contains(prompt, "TRANSCRIPT B") {
		t.Error("expected transcript section headers")
	}
	if !strings.Contains(prompt, "s01e05") {
		t.Error("expected episode key in prompt")
	}
}
