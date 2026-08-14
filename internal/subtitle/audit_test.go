package subtitle

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
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
	if len(dropped) != 0 {
		t.Fatalf("expected 0 dropped, got %d", len(dropped))
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
	if len(dropped) != 0 {
		t.Fatalf("expected 0 dropped, got %d", len(dropped))
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
	if len(resolved) != 0 || len(dropped) != 1 {
		t.Fatalf("expected the ambiguous edit dropped, got resolved=%+v dropped=%d", resolved, len(dropped))
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
	if len(dropped) != 0 {
		t.Fatalf("expected 0 dropped, got %d", len(dropped))
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
	if len(dropped) != 1 {
		t.Fatalf("expected 1 dropped, got %d", len(dropped))
	}
	if resolved[0].Reason != "first" {
		t.Fatalf("expected the first edit to survive, got reason %q", resolved[0].Reason)
	}
}

func TestResolveAuditEditsConfidenceMedium(t *testing.T) {
	cues := sampleCues()
	for _, edit := range []auditEdit{
		{Index: 1, CurrentText: "Hello there.", Action: "replace", Replacement: "Hello dear.", Category: "homophone", Confidence: "medium", Reason: "maybe"},
		{Index: 3, CurrentText: "Thanks for watching.", Action: "remove", Category: "hallucination", Confidence: "medium", Reason: "maybe"},
	} {
		resolved, dropped := resolveAuditEdits(cues, []auditEdit{edit})
		if len(resolved) != 0 || len(dropped) != 1 || dropped[0].Reason != "confidence is not high" {
			t.Fatalf("expected medium-confidence edit dropped, got resolved=%+v dropped=%+v", resolved, dropped)
		}
	}
}

func TestResolveAuditEditsReplaceBlankReplacement(t *testing.T) {
	cues := sampleCues()
	edits := []auditEdit{
		{Index: 1, CurrentText: "Hello there.", Action: "replace", Replacement: "   ", Category: "homophone", Confidence: "high", Reason: "blank"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if len(resolved) != 0 || len(dropped) != 1 {
		t.Fatalf("expected blank replacement dropped, got resolved=%+v dropped=%d", resolved, len(dropped))
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
	if len(resolved) != 1 || len(dropped) != 0 {
		t.Fatalf("expected 1 resolved edit, got resolved=%+v dropped=%d", resolved, len(dropped))
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
	if len(resolved) != 0 || len(dropped) != 1 {
		t.Fatalf("expected no-op replace dropped, got resolved=%+v dropped=%d", resolved, len(dropped))
	}
}

func TestResolveAuditEditsInvalidAction(t *testing.T) {
	cues := sampleCues()
	edits := []auditEdit{
		{Index: 1, CurrentText: "Hello there.", Action: "delete", Category: "homophone", Confidence: "high", Reason: "bad action"},
	}
	resolved, dropped := resolveAuditEdits(cues, edits)
	if len(resolved) != 0 || len(dropped) != 1 {
		t.Fatalf("expected invalid-action edit dropped, got resolved=%+v dropped=%d", resolved, len(dropped))
	}
}

func TestResolveAuditEditsOutsideWindow(t *testing.T) {
	cues := sampleCues()
	edit := auditEdit{
		Index: 99, CurrentText: "Hello there.", Action: "replace", Replacement: "Hi there.",
		Category: "homophone", Confidence: "high", WindowFirst: 2, WindowLast: 3,
	}
	resolved, dropped := resolveAuditEdits(cues, []auditEdit{edit})
	if len(resolved) != 0 || len(dropped) != 1 || dropped[0].Reason != "resolved cue is outside the audited window" {
		t.Fatalf("expected outside-window edit dropped, got resolved=%+v dropped=%+v", resolved, dropped)
	}
}

func TestDeduplicateAuditEdits(t *testing.T) {
	base := auditEdit{Index: 190, CurrentText: "up in Vargo", Action: "replace", Replacement: "up in Fargo", Category: "entity", Confidence: "high"}

	kept, duplicates, dropped := deduplicateAuditEdits([]auditEdit{base, base})
	if len(kept) != 1 || duplicates != 1 || len(dropped) != 0 {
		t.Fatalf("identical overlap proposals: kept=%+v duplicates=%d dropped=%+v", kept, duplicates, dropped)
	}

	conflict := base
	conflict.Replacement = "up near Fargo"
	kept, duplicates, dropped = deduplicateAuditEdits([]auditEdit{base, conflict})
	if len(kept) != 0 || duplicates != 0 || len(dropped) != 2 {
		t.Fatalf("conflicting overlap proposals: kept=%+v duplicates=%d dropped=%+v", kept, duplicates, dropped)
	}
	for _, drop := range dropped {
		if drop.Reason != "conflicting overlap proposals" {
			t.Fatalf("unexpected conflict reason: %+v", drop)
		}
	}

	sameReplacementDifferentCategory := base
	sameReplacementDifferentCategory.Category = "garbled"
	kept, duplicates, dropped = deduplicateAuditEdits([]auditEdit{base, sameReplacementDifferentCategory})
	if len(kept) != 1 || duplicates != 1 || len(dropped) != 0 {
		t.Fatalf("same replacement with telemetry-only category difference: kept=%+v duplicates=%d dropped=%+v", kept, duplicates, dropped)
	}

	remove := auditEdit{Index: 10, CurrentText: ".", Action: "remove", Category: "broken", Confidence: "high"}
	musicRemoval := remove
	musicRemoval.Category = "music_bleed"
	kept, duplicates, dropped = deduplicateAuditEdits([]auditEdit{remove, musicRemoval})
	if len(kept) != 0 || duplicates != 0 || len(dropped) != 2 {
		t.Fatalf("removal category conflict must be preserved: kept=%+v duplicates=%d dropped=%+v", kept, duplicates, dropped)
	}
}

func TestGuardMusicBleedRemovals(t *testing.T) {
	resolved := []resolvedEdit{
		{CueIndex: 0, Action: "remove", Category: "music_bleed"},
		{CueIndex: 1, Action: "remove", Category: "music_bleed"},
		{CueIndex: 2, Action: "remove", Category: "music_bleed"},
		{CueIndex: 3, Action: "remove", Category: "music_bleed"},
		{CueIndex: 4, Action: "remove", Category: "music_bleed"},
		{CueIndex: 5, Action: "remove", Category: "music_bleed"},
		{CueIndex: 6, Action: "replace", Category: "homophone", Replacement: "Even Rocky had a montage."},
		{CueIndex: 7, Action: "remove", Category: "credits_music"},
	}
	got, preserved := guardMusicBleedRemovals(resolved)
	if len(preserved) != 6 {
		t.Fatalf("expected 6 music bleed removals preserved, got %d", len(preserved))
	}
	if len(got) != 2 || got[0].Category != "homophone" || got[1].Category != "credits_music" {
		t.Fatalf("expected unrelated edits retained, got %+v", got)
	}
}

func TestGuardMusicBleedRemovalsAllowsSmallSet(t *testing.T) {
	resolved := make([]resolvedEdit, maxMusicBleedRemovalEdits)
	for i := range resolved {
		resolved[i] = resolvedEdit{CueIndex: i, Action: "remove", Category: "music_bleed"}
	}
	got, preserved := guardMusicBleedRemovals(resolved)
	if len(got) != maxMusicBleedRemovalEdits || len(preserved) != 0 {
		t.Fatalf("expected small music bleed set allowed, got %+v, preserved=%d", got, len(preserved))
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
	// videoSeconds=1000 -> window=min(420, 100)=100 -> creditsStart=900.
	cues := manyCues(100) // cap = max(5, 100/10) = 10
	var resolved []resolvedEdit
	// Remove the last 10 cues (Start 900..990), all >= creditsStart=900.
	for i := 90; i < 100; i++ {
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

func TestCreditsRegionStart(t *testing.T) {
	tests := []struct {
		name         string
		videoSeconds float64
		want         float64
	}{
		{name: "feature film uses full window", videoSeconds: 7200, want: 7200 - 420},
		{name: "hour drama scales to 10 percent", videoSeconds: 3486.5, want: 3486.5 - 348.65},
		{name: "sitcom episode scales to 10 percent", videoSeconds: 1320, want: 1320 - 132},
		{name: "unknown duration has no credits region", videoSeconds: 0, want: math.Inf(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := creditsRegionStart(tt.videoSeconds); math.Abs(got-tt.want) > 1e-9 && got != tt.want {
				t.Fatalf("creditsRegionStart(%v) = %v, want %v", tt.videoSeconds, got, tt.want)
			}
		})
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

func verificationResponseBody(edits ...auditEdit) []byte {
	content, _ := json.Marshal(auditResponse{Edits: edits})
	return chatResponseBody(string(content))
}

func requestUserPrompt(t *testing.T, r *http.Request) string {
	t.Helper()
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Fatalf("decode LLM request: %v", err)
	}
	for _, message := range request.Messages {
		if message.Role == "user" {
			return message.Content
		}
	}
	t.Fatal("LLM request has no user prompt")
	return ""
}

func TestAuditDisplaySRTChunksFocusedWindows(t *testing.T) {
	path := writeSRTFile(t, manyCues(381))
	var prompts []string
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		prompts = append(prompts, requestUserPrompt(t, r))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatResponseBody(`{"edits": []}`))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{
		DisplayPath: path, MediaContext: `the movie "Fargo" (1996)`, EpisodeKey: "main",
	})
	if stats.Result != "clean" {
		t.Fatalf("expected clean audit, got %+v", stats)
	}
	wantWindows := []string{
		"Audit window 1/3 contains global cues 1-200 of 381",
		"Audit window 2/3 contains global cues 181-380 of 381",
		"Audit window 3/3 contains global cues 361-381 of 381",
	}
	if len(prompts) != len(wantWindows) {
		t.Fatalf("got %d requests, want %d", len(prompts), len(wantWindows))
	}
	for i, want := range wantWindows {
		if !strings.Contains(prompts[i], want) {
			t.Errorf("prompt %d missing %q", i+1, want)
		}
		if !strings.Contains(prompts[i], `the movie "Fargo" (1996)`) {
			t.Errorf("prompt %d missing media context", i+1)
		}
	}
}

func TestReferenceExcerptRetrievesMatchingTranscriptPassage(t *testing.T) {
	prefix := strings.Repeat("unrelated bakery conversation ", 80)
	matching := strings.Repeat("enterprise commander riker data android ", 30)
	suffix := strings.Repeat("unrelated courtroom testimony ", 80)
	target := []srtutil.Cue{{Index: 1, Text: "Commander Riker asked Data to return to the Enterprise."}}

	excerpt := referenceExcerpt(prefix+matching+suffix, target)
	if !strings.Contains(excerpt, "enterprise commander riker data android") {
		t.Fatalf("matching passage was not retrieved: %q", excerpt)
	}
	if strings.Contains(excerpt, "courtroom") {
		t.Fatalf("retrieved passage drifted into unrelated suffix: %q", excerpt)
	}
}

func TestAuditPromptsIncludeUntimedReferenceLookup(t *testing.T) {
	cues := sampleCues()
	params := auditParams{
		MediaContext:        "a test movie",
		ReferenceTranscript: "Hello there. How are you? Her name is Commander T'Pol.",
	}
	firstPass := buildAuditUserPrompt(cues, params, 1, 1, len(cues))
	if !strings.Contains(firstPass, "REFERENCE TRANSCRIPT LOOKUP (untimed") || !strings.Contains(firstPass, "Commander T'Pol") {
		t.Fatalf("first-pass prompt lacks reference transcript: %s", firstPass)
	}

	resolved := []resolvedEdit{{CueIndex: 0, Action: "replace", Replacement: "General Kenobi."}}
	blindVerification := buildAuditVerificationPrompt(cues, resolved, resolved, params)
	if !strings.Contains(blindVerification, "Reference transcript lookup (untimed") || !strings.Contains(blindVerification, "Commander T'Pol") {
		t.Fatalf("blind verification prompt lacks reference transcript: %s", blindVerification)
	}
	if strings.Contains(blindVerification, "General Kenobi") {
		t.Fatalf("blind verifier saw proposed replacement: %s", blindVerification)
	}

	referenceVerification := buildReferenceAuditVerificationPrompt(cues, resolved, resolved, params)
	for _, want := range []string{"one explicit verdict", "Proposed replacement: General Kenobi.", "Commander T'Pol"} {
		if !strings.Contains(referenceVerification, want) {
			t.Fatalf("reference verification prompt lacks %q: %s", want, referenceVerification)
		}
	}
}

func TestAuditDisplaySRTOverlapProposalDeduplicated(t *testing.T) {
	cues := manyCues(201)
	cues[49].Text = "This is Marge from our brainer."
	cues[189].Text = "You put me in touch up there in Vargo?"
	cues[200].Text = "They left the tags-based plank."
	path := writeSRTFile(t, cues)
	allEdits := []auditEdit{
		{Index: 50, CurrentText: cues[49].Text, Action: "replace", Replacement: "This is Marge from up Brainerd.", Category: "entity", Confidence: "high", Reason: "known location"},
		{Index: 190, CurrentText: cues[189].Text, Action: "replace", Replacement: "You put me in touch up there in Fargo?", Category: "entity", Confidence: "high", Reason: "title identity"},
		{Index: 201, CurrentText: cues[200].Text, Action: "replace", Replacement: "They left the tag space blank.", Category: "garbled", Confidence: "high", Reason: "contextually impossible phrase"},
	}
	requests := 0
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		prompt := requestUserPrompt(t, r)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(prompt, "Blind final audit") {
			_, _ = w.Write(verificationResponseBody(allEdits...))
			return
		}
		var edits []auditEdit
		for _, edit := range allEdits {
			if strings.Contains(prompt, edit.CurrentText) {
				edits = append(edits, edit)
			}
		}
		response, _ := json.Marshal(auditResponse{Edits: edits})
		_, _ = w.Write(chatResponseBody(string(response)))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{
		DisplayPath: path, MediaContext: `the movie "Fargo" (1996)`, EpisodeKey: "main",
	})
	if requests != 3 || stats.Result != "applied" || stats.Applied != 3 || stats.Dropped != 0 {
		t.Fatalf("unexpected overlap result: requests=%d stats=%+v", requests, stats)
	}
	got, err := srtutil.ParseFile(path)
	if err != nil {
		t.Fatalf("parse audited SRT: %v", err)
	}
	for index, want := range map[int]string{
		49:  "This is Marge from up Brainerd.",
		189: "You put me in touch up there in Fargo?",
		200: "They left the tag space blank.",
	} {
		if got[index].Text != want {
			t.Errorf("cue %d = %q, want %q", index+1, got[index].Text, want)
		}
	}
}

func TestAuditDisplaySRTChunkFailureLeavesFileUnchanged(t *testing.T) {
	cues := manyCues(201)
	path := writeSRTFile(t, cues)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	response, _ := json.Marshal(auditResponse{Edits: []auditEdit{{
		Index: 1, CurrentText: cues[0].Text, Action: "replace", Replacement: "changed", Category: "garbled", Confidence: "high",
	}}})
	requests := 0
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatResponseBody(string(response)))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{DisplayPath: path, MediaContext: "a test movie", EpisodeKey: "main"})
	if stats.Result != "failed" || !strings.Contains(stats.FailureReason, "chunk 2/2") {
		t.Fatalf("expected second chunk failure, got %+v", stats)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture after audit: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("chunk failure modified the SRT")
	}
}

func TestAuditDisplaySRTLogsDropReason(t *testing.T) {
	path := writeSRTFile(t, sampleCues())
	response, _ := json.Marshal(auditResponse{Edits: []auditEdit{{
		Index: 3, CurrentText: "Thanks for watching.", Action: "remove", Category: "hallucination", Confidence: "medium", Reason: "uncertain",
	}}})
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatResponseBody(string(response)))
	})
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))

	stats := auditDisplaySRT(context.Background(), client, logger, auditParams{DisplayPath: path, MediaContext: "a test movie", EpisodeKey: "main"})
	if stats.Result != "clean" || stats.Dropped != 1 {
		t.Fatalf("unexpected audit stats: %+v", stats)
	}
	logOutput := output.String()
	for _, want := range []string{`"decision_result":"dropped"`, `"decision_reason":"confidence is not high"`, `"cue_index":3`} {
		if !strings.Contains(logOutput, want) {
			t.Errorf("audit log missing %s: %s", want, logOutput)
		}
	}
}

func TestAuditDisplaySRTVerificationDropsUnconfirmedEdits(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 1, End: 2, Text: "You ask Stan Grossman."},
		{Index: 2, Start: 3, End: 4, Text: "Yes, Dan Grossman, he'll tell you the same thing."},
		{Index: 3, Start: 5, End: 6, Text: "That fuck is mine, you fucking asshole."},
	}
	path := writeSRTFile(t, cues)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	response, _ := json.Marshal(auditResponse{Edits: []auditEdit{
		{Index: 2, CurrentText: cues[1].Text, Action: "replace", Replacement: "Yes, Dan Gustafson, he'll tell you the same thing.", Category: "entity", Confidence: "high", Reason: "plot inference"},
		{Index: 3, CurrentText: cues[2].Text, Action: "replace", Replacement: "That truck is mine, you fucking asshole.", Category: "homophone", Confidence: "high", Reason: "plausible word"},
	}})
	var verificationPrompt string
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		prompt := requestUserPrompt(t, r)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(prompt, "Blind final audit") {
			verificationPrompt = prompt
			_, _ = w.Write(verificationResponseBody())
			return
		}
		_, _ = w.Write(chatResponseBody(string(response)))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{DisplayPath: path, MediaContext: `the movie "Fargo" (1996)`})
	if stats.Result != "clean" || stats.Applied != 0 || stats.Dropped != 2 {
		t.Fatalf("unexpected verification result: %+v", stats)
	}
	if !strings.Contains(verificationPrompt, "Target 1:") || !strings.Contains(verificationPrompt, "Target 2:") || !strings.Contains(verificationPrompt, "You ask Stan Grossman.") {
		t.Fatalf("verification prompt lacks global candidates or local context: %s", verificationPrompt)
	}
	for _, hidden := range []string{"Dan Gustafson", "That truck is mine"} {
		if strings.Contains(verificationPrompt, hidden) {
			t.Fatalf("blind verification prompt exposed proposed replacement %q", hidden)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture after audit: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("rejected verification candidates modified the SRT")
	}
}

func TestAuditDisplaySRTReferenceVerificationUsesExplicitVerdicts(t *testing.T) {
	cues := []srtutil.Cue{
		{Index: 1, Start: 1, End: 2, Text: "I'm Jerry Lundegarden."},
		{Index: 2, Start: 3, End: 4, Text: "Margie Olmsted?"},
	}
	path := writeSRTFile(t, cues)
	proposals := auditResponse{Edits: []auditEdit{
		{Index: 1, CurrentText: cues[0].Text, Action: "replace", Replacement: "I'm Jerry Lundegaard.", Category: "entity", Confidence: "high", Reason: "reference spelling"},
		{Index: 2, CurrentText: cues[1].Text, Action: "replace", Replacement: "Marge Olmstead?", Category: "entity", Confidence: "high", Reason: "reference name"},
	}}
	proposalJSON, _ := json.Marshal(proposals)
	requests := 0
	var verificationPrompt string
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		prompt := requestUserPrompt(t, r)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(prompt, "Reference-assisted final review") {
			verificationPrompt = prompt
			response, _ := json.Marshal(auditVerificationResponse{Verdicts: []auditVerificationVerdict{
				{Index: 1, Accept: true, Reason: "reference confirms Lundegaard"},
				{Index: 2, Accept: false, Reason: "reference says Margie, not Marge"},
			}})
			_, _ = w.Write(chatResponseBody(string(response)))
			return
		}
		_, _ = w.Write(chatResponseBody(string(proposalJSON)))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{
		DisplayPath:         path,
		MediaContext:        `the movie "Fargo" (1996)`,
		ReferenceTranscript: "I'm Jerry Lundegaard. Margie Olmstead?",
	})
	if stats.Result != "applied" || stats.Applied != 1 || stats.Dropped != 1 || requests != 2 {
		t.Fatalf("unexpected reference verification result: stats=%+v requests=%d", stats, requests)
	}
	for _, want := range []string{"Proposed replacement: I'm Jerry Lundegaard.", "Proposed replacement: Marge Olmstead?", "Return one explicit verdict"} {
		if !strings.Contains(verificationPrompt, want) {
			t.Errorf("verification prompt missing %q: %s", want, verificationPrompt)
		}
	}
	got, err := srtutil.ParseFile(path)
	if err != nil {
		t.Fatalf("parse audited SRT: %v", err)
	}
	if got[0].Text != "I'm Jerry Lundegaard." || got[1].Text != "Margie Olmsted?" {
		t.Fatalf("unexpected audited cues: %+v", got)
	}
}

func TestVerifyAuditEditsBatchesFocusedReviews(t *testing.T) {
	cues := manyCues(auditVerificationCandidates + 1)
	resolved := make([]resolvedEdit, len(cues))
	for i := range cues {
		resolved[i] = resolvedEdit{CueIndex: i, Action: "replace", Replacement: "changed"}
	}
	requests := 0
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		prompt := requestUserPrompt(t, r)
		if !strings.Contains(prompt, "ALL TARGET CUES FOR GLOBAL CONSISTENCY") {
			t.Errorf("verification request lacks global target ledger: %s", prompt)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(verificationResponseBody())
	})

	kept, dropped, err := verifyAuditEdits(context.Background(), client, cues, resolved, auditParams{MediaContext: "a test movie"})
	if err != nil || len(kept) != 0 || len(dropped) != len(resolved) || requests != 2 {
		t.Fatalf("unexpected batched verification: kept=%d dropped=%d requests=%d err=%v", len(kept), len(dropped), requests, err)
	}
}

func TestAuditDisplaySRTVerificationFailureLeavesFileUnchanged(t *testing.T) {
	cues := sampleCues()
	path := writeSRTFile(t, cues)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	response, _ := json.Marshal(auditResponse{Edits: []auditEdit{{
		Index: 1, CurrentText: cues[0].Text, Action: "replace", Replacement: "Hi there.", Category: "homophone", Confidence: "high",
	}}})
	requests := 0
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad verification"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(chatResponseBody(string(response)))
	})

	stats := auditDisplaySRT(context.Background(), client, nil, auditParams{DisplayPath: path, MediaContext: "a test movie"})
	if stats.Result != "failed" || !strings.Contains(stats.FailureReason, "verification") {
		t.Fatalf("expected atomic verification failure, got %+v", stats)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture after audit: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("verification failure modified the SRT")
	}
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

	requests := 0
	client := newTestLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(requestUserPrompt(t, r), "Blind final audit") {
			_, _ = w.Write(chatResponseBody(string(body)))
			return
		}
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
	if requests != 2 {
		t.Fatalf("short subtitle used %d requests, want 2", requests)
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
		if strings.Contains(requestUserPrompt(t, r), "Blind final audit") {
			_, _ = w.Write(chatResponseBody(string(body)))
			return
		}
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
