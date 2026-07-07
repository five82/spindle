package subtitle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/srtutil"
)

func sampleCues() []srtutil.Cue {
	return []srtutil.Cue{
		{Index: 1, Start: 1, End: 2, Text: "Hello there."},
		{Index: 2, Start: 3, End: 4, Text: "How are you."},
		{Index: 3, Start: 5, End: 6, Text: "Thanks for watching."},
	}
}

func TestResolveAuditEditsExactIndexMatch(t *testing.T) {
	cues := sampleCues()
	edits := []auditEdit{
		{Index: 3, CurrentText: "Thanks for watching.", Action: "remove", Category: "hallucination", Confidence: "high", Reason: "isolated hallucination"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if dropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", dropped)
	}
	if len(resolved) != 1 || resolved[0].CueIndex != 2 {
		t.Fatalf("expected resolve to cue position 2, got %+v", resolved)
	}
}

func TestResolveAuditEditsIndexDrift(t *testing.T) {
	// The edit's index no longer matches the cue that now holds this exact
	// text, but the text is globally unique, so it should remap.
	cues := sampleCues()
	edits := []auditEdit{
		{Index: 99, CurrentText: "Thanks for watching.", Action: "remove", Category: "hallucination", Confidence: "high", Reason: "isolated hallucination"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if dropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", dropped)
	}
	if len(resolved) != 1 || resolved[0].CueIndex != 2 {
		t.Fatalf("expected remap to cue position 2, got %+v", resolved)
	}
}

func TestResolveAuditEditsAmbiguousIndexNotAmongMatches(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 1, End: 2, Text: "Thank you."},
		{Index: 2, Start: 3, End: 4, Text: "Something else."},
		{Index: 3, Start: 5, End: 6, Text: "Thank you."},
	}
	edits := []auditEdit{
		{Index: 7, CurrentText: "Thank you.", Action: "remove", Category: "hallucination", Confidence: "high", Reason: "dup"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if len(resolved) != 0 || dropped != 1 {
		t.Fatalf("expected the ambiguous edit dropped, got resolved=%+v dropped=%d", resolved, dropped)
	}
}

func TestResolveAuditEditsAmbiguousIndexAmongMatches(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 1, End: 2, Text: "Thank you."},
		{Index: 2, Start: 3, End: 4, Text: "Something else."},
		{Index: 3, Start: 5, End: 6, Text: "Thank you."},
	}
	edits := []auditEdit{
		{Index: 3, CurrentText: "Thank you.", Action: "remove", Category: "hallucination", Confidence: "high", Reason: "dup"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if dropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", dropped)
	}
	if len(resolved) != 1 || resolved[0].CueIndex != 2 {
		t.Fatalf("expected resolve to cue position 2 (Index 3), got %+v", resolved)
	}
}

func TestResolveAuditEditsDedupeSameCue(t *testing.T) {
	cues := sampleCues()
	edits := []auditEdit{
		{Index: 3, CurrentText: "Thanks for watching.", Action: "remove", Category: "hallucination", Confidence: "high", Reason: "first"},
		{Index: 3, CurrentText: "Thanks for watching.", Action: "remove", Category: "hallucination", Confidence: "high", Reason: "second"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if len(resolved) != 1 {
		t.Fatalf("expected exactly 1 resolved edit, got %d", len(resolved))
	}
	if dropped != 1 {
		t.Fatalf("expected 1 dropped, got %d", dropped)
	}
	if resolved[0].Reason != "first" {
		t.Fatalf("expected the first edit to survive, got reason %q", resolved[0].Reason)
	}
}

func TestResolveAuditEditsReplaceConfidenceMedium(t *testing.T) {
	cues := sampleCues()
	edits := []auditEdit{
		{Index: 1, CurrentText: "Hello there.", Action: "replace", Replacement: "Hello dear.", Category: "homophone", Confidence: "medium", Reason: "maybe"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if len(resolved) != 0 || dropped != 1 {
		t.Fatalf("expected medium-confidence replace dropped, got resolved=%+v dropped=%d", resolved, dropped)
	}
}

func TestResolveAuditEditsReplaceBlankReplacement(t *testing.T) {
	cues := sampleCues()
	edits := []auditEdit{
		{Index: 1, CurrentText: "Hello there.", Action: "replace", Replacement: "   ", Category: "homophone", Confidence: "high", Reason: "blank"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if len(resolved) != 0 || dropped != 1 {
		t.Fatalf("expected blank replacement dropped, got resolved=%+v dropped=%d", resolved, dropped)
	}
}

func TestResolveAuditEditsReplaceUnescapesLiteralNewline(t *testing.T) {
	// Models echo the prompt's literal \n cue serialization back inside
	// replacement text; it must never reach the SRT.
	cues := []srtutil.Cue{
		{Index: 1, Start: 1, End: 2, Text: "You would not believe\nyou screening Chad's car."},
	}
	edits := []auditEdit{
		{Index: 1, CurrentText: `You would not believe\nyou screening Chad's car.`, Action: "replace", Replacement: `You would not believe\nyou're cleaning Chad's car.`, Category: "homophone", Confidence: "high", Reason: "context"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if len(resolved) != 1 || dropped != 0 {
		t.Fatalf("expected 1 resolved edit, got resolved=%+v dropped=%d", resolved, dropped)
	}
	if strings.Contains(resolved[0].Replacement, `\n`) {
		t.Fatalf("replacement contains literal backslash-n: %q", resolved[0].Replacement)
	}
}

func TestResolveAuditEditsReplaceNoOpDropped(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 1, End: 2, Text: "The worst that you\ncan do is shut me out."},
	}
	edits := []auditEdit{
		{Index: 1, CurrentText: `The worst that you\ncan do is shut me out.`, Action: "replace", Replacement: `The worst that you\ncan do is shut me out.`, Category: "credits_music", Confidence: "high", Reason: "no-op"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if len(resolved) != 0 || dropped != 1 {
		t.Fatalf("expected no-op replace dropped, got resolved=%+v dropped=%d", resolved, dropped)
	}
}

func TestResolveAuditEditsInvalidAction(t *testing.T) {
	cues := sampleCues()
	edits := []auditEdit{
		{Index: 1, CurrentText: "Hello there.", Action: "delete", Category: "homophone", Confidence: "high", Reason: "bad action"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if len(resolved) != 0 || dropped != 1 {
		t.Fatalf("expected invalid-action edit dropped, got resolved=%+v dropped=%d", resolved, dropped)
	}
}

func TestApplyResolvedEditsReindexes(t *testing.T) {
	cues := sampleCues()
	resolved := []resolvedEdit{
		{CueIndex: 1, Action: "remove"},
	}
	out := applyResolvedEdits(cues, resolved)
	if len(out) != 2 {
		t.Fatalf("expected 2 cues remaining, got %d", len(out))
	}
	for i, cue := range out {
		if cue.Index != i+1 {
			t.Fatalf("expected sequential index %d, got %d", i+1, cue.Index)
		}
	}
	if out[0].Text != "Hello there." || out[1].Text != "Thanks for watching." {
		t.Fatalf("unexpected surviving cue texts: %+v", out)
	}
}

func TestApplyResolvedEditsReplace(t *testing.T) {
	cues := sampleCues()
	resolved := []resolvedEdit{
		{CueIndex: 0, Action: "replace", Replacement: "Hi there."},
	}
	out := applyResolvedEdits(cues, resolved)
	if len(out) != 3 {
		t.Fatalf("expected 3 cues, got %d", len(out))
	}
	if out[0].Text != "Hi there." {
		t.Fatalf("expected replaced text, got %q", out[0].Text)
	}
}

func manyCues(n int) []srtutil.Cue {
	cues := make([]srtutil.Cue, n)
	for i := range cues {
		cues[i] = srtutil.Cue{
			Index: i + 1,
			Start: float64(i * 10),
			End:   float64(i*10 + 5),
			Text:  "line text",
		}
	}
	return cues
}

func TestAuditRemovalCapExceeded(t *testing.T) {
	cues := manyCues(10) // cap = max(5, 10/10) = 5
	var resolved []resolvedEdit
	for i := 0; i < 6; i++ {
		resolved = append(resolved, resolvedEdit{CueIndex: i, Action: "remove"})
	}
	exceeded, nonCredits, cap := auditRemovalCap(cues, resolved, 0)
	if !exceeded {
		t.Fatalf("expected cap exceeded: nonCredits=%d cap=%d", nonCredits, cap)
	}
	if nonCredits != 6 {
		t.Fatalf("expected 6 non-credits removals, got %d", nonCredits)
	}
	if cap != 5 {
		t.Fatalf("expected cap 5, got %d", cap)
	}
}

func TestAuditRemovalCapCreditsRegionExcluded(t *testing.T) {
	// videoSeconds=1000 -> creditsStart=580. Cues 90..990 in steps of 10 for
	// n=100 puts the last several cues in the credits region.
	cues := manyCues(100) // cap = max(5, 100/10) = 10
	var resolved []resolvedEdit
	// Remove the last 12 cues (Start 890..990), all >= creditsStart=580.
	for i := 88; i < 100; i++ {
		resolved = append(resolved, resolvedEdit{CueIndex: i, Action: "remove"})
	}
	exceeded, nonCredits, cap := auditRemovalCap(cues, resolved, 1000)
	if exceeded {
		t.Fatalf("expected credits-region removals not to trip the cap: nonCredits=%d cap=%d", nonCredits, cap)
	}
	if nonCredits != 0 {
		t.Fatalf("expected 0 non-credits removals, got %d", nonCredits)
	}
	if cap != 10 {
		t.Fatalf("expected cap 10, got %d", cap)
	}
}

func writeSRTFile(t *testing.T, cues []srtutil.Cue) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "display.en.srt")
	if err := os.WriteFile(path, []byte(srtutil.Format(cues)), 0o644); err != nil {
		t.Fatalf("write srt fixture: %v", err)
	}
	return path
}

func newTestLLMClient(t *testing.T, handler http.HandlerFunc) *llm.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return llm.New(config.LLMConfig{APIKey: "test", BaseURL: srv.URL, TimeoutSeconds: 5}, nil)
}

func chatResponseBody(content string) []byte {
	resp := map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"content": content}},
		},
	}
	body, _ := json.Marshal(resp)
	return body
}

func TestAuditDisplaySRTAppliesEdits(t *testing.T) {
	cues := sampleCues()
	path := writeSRTFile(t, cues)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	edits := auditResponse{Edits: []auditEdit{
		{Index: 3, CurrentText: "Thanks for watching.", Action: "remove", Category: "hallucination", Confidence: "high", Reason: "isolated"},
		{Index: 1, CurrentText: "Hello there.", Action: "replace", Replacement: "Hi there.", Category: "homophone", Confidence: "high", Reason: "clearer"},
	}}
	body, err := json.Marshal(edits)
	if err != nil {
		t.Fatalf("marshal edits: %v", err)
	}

	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatResponseBody(string(body)))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{
		DisplayPath:  path,
		VideoSeconds: 3600,
		MediaContext: `the movie "Air" (2023)`,
		EpisodeKey:   "movie",
	})

	if stats.Result != "applied" {
		t.Fatalf("expected Result=applied, got %+v", stats)
	}
	if stats.Applied != 2 || stats.Dropped != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten file: %v", err)
	}
	if string(rewritten) == string(original) {
		t.Fatalf("expected file to be rewritten")
	}
	newCues := srtutil.Parse(string(rewritten))
	if len(newCues) != 2 {
		t.Fatalf("expected 2 surviving cues, got %d: %+v", len(newCues), newCues)
	}
	if newCues[0].Text != "Hi there." {
		t.Fatalf("expected replaced text, got %q", newCues[0].Text)
	}
	if newCues[0].Index != 1 || newCues[1].Index != 2 {
		t.Fatalf("expected sequential reindex, got %+v", newCues)
	}
}

func TestAuditDisplaySRTLLMFailure(t *testing.T) {
	cues := sampleCues()
	path := writeSRTFile(t, cues)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// HTTP 400 is treated as non-retryable by the llm client, so this fails
	// fast without the retry/backoff loop.
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{
		DisplayPath:  path,
		VideoSeconds: 3600,
		MediaContext: "a test movie",
		EpisodeKey:   "movie",
	})

	if stats.Result != "failed" {
		t.Fatalf("expected Result=failed, got %+v", stats)
	}
	if stats.FailureReason == "" {
		t.Fatalf("expected a failure reason")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file after failure: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("expected file to be untouched on LLM failure")
	}
}

func TestAuditDisplaySRTNilClient(t *testing.T) {
	cues := sampleCues()
	path := writeSRTFile(t, cues)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	stats := auditDisplaySRT(context.Background(), nil, nil, auditParams{
		DisplayPath:  path,
		VideoSeconds: 3600,
		MediaContext: "a test movie",
		EpisodeKey:   "movie",
	})

	if stats.Result != "skipped" {
		t.Fatalf("expected Result=skipped, got %+v", stats)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file after nil client: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("expected file to be untouched with nil client")
	}
}

func TestAuditDisplaySRTCapRejectionLeavesFileUnchanged(t *testing.T) {
	cues := manyCues(10) // cap = max(5, 10/10) = 5
	path := writeSRTFile(t, cues)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var edits []auditEdit
	for i := 0; i < 6; i++ {
		edits = append(edits, auditEdit{
			Index:       i + 1,
			CurrentText: "line text",
			Action:      "remove",
			Category:    "hallucination",
			Confidence:  "high",
			Reason:      "test",
		})
	}
	body, err := json.Marshal(auditResponse{Edits: edits})
	if err != nil {
		t.Fatalf("marshal edits: %v", err)
	}

	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatResponseBody(string(body)))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{
		DisplayPath:  path,
		VideoSeconds: 0, // unknown duration: every cue counts as non-credits
		MediaContext: "a test movie",
		EpisodeKey:   "movie",
	})

	if stats.Result != "rejected" {
		t.Fatalf("expected Result=rejected for cap rejection, got %+v", stats)
	}
	if stats.FailureReason != "non-credits removal cap exceeded" {
		t.Fatalf("unexpected failure reason: %q", stats.FailureReason)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file after cap rejection: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("expected file to be untouched after cap rejection")
	}
}

func TestAuditDisplaySRTClean(t *testing.T) {
	cues := sampleCues()
	path := writeSRTFile(t, cues)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatResponseBody(`{"edits": []}`))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{
		DisplayPath:  path,
		VideoSeconds: 3600,
		MediaContext: "a test movie",
		EpisodeKey:   "movie",
	})

	if stats.Result != "clean" {
		t.Fatalf("expected Result=clean, got %+v", stats)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file after clean run: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("expected file to be untouched on a clean run")
	}
}
