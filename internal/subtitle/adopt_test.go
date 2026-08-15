package subtitle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/fileutil"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/transcription"
)

// stubAdoptEnvironment stubs ffprobe and ffsubsync: the probe reports a fixed
// duration and "sync" copies the cleaned input to the output unchanged.
func stubAdoptEnvironment(t *testing.T, videoSeconds string) {
	t.Helper()
	origInspect := inspectSubtitleMedia
	origSync := runFFSubsync
	t.Cleanup(func() {
		inspectSubtitleMedia = origInspect
		runFFSubsync = origSync
	})
	inspectSubtitleMedia = func(context.Context, string, string) (*ffprobe.Result, error) {
		return &ffprobe.Result{Format: ffprobe.Format{Duration: videoSeconds}}, nil
	}
	runFFSubsync = func(_ context.Context, args []string) ([]byte, error) {
		// args: [ffsubsync, reference, -i, input, -o, output]
		if err := fileutil.CopyFile(args[3], args[5]); err != nil {
			return nil, err
		}
		return []byte("ok"), nil
	}
}

// newAdoptSession builds a TV session with a completed ripped asset, a
// transcript artifact containing referenceCues, and an on-disk contentid
// reference file containing candidateSRT.
func newAdoptSession(t *testing.T, h *Handler, referenceCues []srtutil.Cue, candidateSRT []byte) (*stage.Session, stage.AssetJob) {
	t.Helper()
	sess := newSubtitleTestSession(t, &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv", ID: 1396},
		Episodes: []ripspec.Episode{{Key: "s01e01", Season: 1, Episode: 1}},
	})
	root, err := sess.Item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		t.Fatal(err)
	}

	transcriptDir := filepath.Join(root, "transcripts", "s01e01")
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcriptSRT := filepath.Join(transcriptDir, "audio.srt")
	if err := os.WriteFile(transcriptSRT, []byte(srtutil.Format(referenceCues)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transcriptDir, "audio.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	refDir := filepath.Join(root, "contentid", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "s01e01-77.srt"), candidateSRT, 0o644); err != nil {
		t.Fatal(err)
	}

	ripped := ripspec.Asset{EpisodeKey: "s01e01", Path: filepath.Join(root, "ripped.mkv"), Status: ripspec.AssetStatusCompleted}
	sess.Env.Assets.Ripped = []ripspec.Asset{ripped}
	sess.Env.Assets.Transcript = []ripspec.Asset{{EpisodeKey: "s01e01", Path: transcriptSRT, Status: ripspec.AssetStatusCompleted}}
	return sess, stage.AssetJob{Key: "s01e01", Input: ripped, ProgressTotal: 1}
}

func emptySearchClient(t *testing.T) *opensubtitles.Client {
	t.Helper()
	server := newCandidateSearchServer(t, `{"data":[]}`)
	t.Cleanup(server.Close)
	return opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, discardLogger())
}

func TestProcessSubtitleJobAdoptsVerifiedDownload(t *testing.T) {
	stubAdoptEnvironment(t, "140.0")
	reference := dialogueCues(12, 10, 10)

	// The candidate is the reference dialogue plus a leading spam cue that
	// cleanup must remove before adoption.
	candidateSRT := []byte("1\n00:00:01,000 --> 00:00:03,000\nDownloaded from www.OpenSubtitles.org\n\n" + srtutil.Format(reference))

	h := &Handler{
		cfg:      &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}, Subtitles: config.SubtitlesConfig{Enabled: true}},
		osClient: emptySearchClient(t),
	}
	sess, job := newAdoptSession(t, h, reference, candidateSRT)

	outcome, err := h.processSubtitleJob(context.Background(), sess, job)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != subtitleOutcomeAdopted {
		t.Fatalf("outcome = %q", outcome)
	}

	records := sess.Env.Attributes.SubtitleGenerationResults
	if len(records) != 1 {
		t.Fatalf("records = %+v", records)
	}
	record := records[0]
	if record.Source != "opensubtitles" || record.ValidationResult != "passed" || record.Segments != len(reference) {
		t.Fatalf("record = %+v", record)
	}
	cues, err := srtutil.ParseFile(record.SubtitlePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != len(reference) {
		t.Fatalf("adopted cue count = %d, want %d", len(cues), len(reference))
	}
	for _, cue := range cues {
		if strings.Contains(cue.Text, "OpenSubtitles") {
			t.Fatalf("spam cue survived adoption: %q", cue.Text)
		}
	}
}

// writeAlignedWordsJSON writes an audio.json whose word stream spreads each
// cue's words across its interval, as the WhisperX wrapper does.
func writeAlignedWordsJSON(t *testing.T, path string, cues []srtutil.Cue) {
	t.Helper()
	type jsonWord struct {
		Word  string  `json:"word"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	}
	var words []jsonWord
	for _, w := range wordsForCues(cues) {
		words = append(words, jsonWord{Word: w.Text, Start: w.Start, End: w.End})
	}
	payload, err := json.Marshal(map[string]any{"segments": []map[string]any{{"words": words}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProcessSubtitleJobSnapsAdoptedCuesToWordOnsets(t *testing.T) {
	stubAdoptEnvironment(t, "140.0")
	reference := dialogueCues(12, 10, 10)
	// The candidate's text matches but every cue leads speech by 0.4s, the
	// per-cue author lead that ffsubsync's global correction cannot remove.
	candidate := shiftedCues(reference, 0.4)

	h := &Handler{
		cfg:      &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}, Subtitles: config.SubtitlesConfig{Enabled: true}},
		osClient: emptySearchClient(t),
	}
	sess, job := newAdoptSession(t, h, reference, []byte(srtutil.Format(candidate)))
	root, err := sess.Item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		t.Fatal(err)
	}
	writeAlignedWordsJSON(t, filepath.Join(root, "transcripts", "s01e01", "audio.json"), reference)

	outcome, err := h.processSubtitleJob(context.Background(), sess, job)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != subtitleOutcomeAdopted {
		t.Fatalf("outcome = %q", outcome)
	}
	records := sess.Env.Attributes.SubtitleGenerationResults
	if len(records) != 1 {
		t.Fatalf("records = %+v", records)
	}
	cues, err := srtutil.ParseFile(records[0].SubtitlePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != len(reference) {
		t.Fatalf("adopted cue count = %d, want %d", len(cues), len(reference))
	}
	for i, cue := range cues {
		if !approxEqual(cue.Start, reference[i].Start) {
			t.Fatalf("cue %d start = %.3f, want word onset %.3f", i, cue.Start, reference[i].Start)
		}
	}
}

func TestProcessSubtitleJobSkipsWhenUnconfigured(t *testing.T) {
	h := &Handler{cfg: &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}}
	sess := newSubtitleTestSession(t, &ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie", ID: 9}})
	job := stage.AssetJob{Key: "main", Input: ripspec.Asset{EpisodeKey: "main", Path: "/staging/main.mkv"}, ProgressTotal: 1}

	outcome, err := h.processSubtitleJob(context.Background(), sess, job)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != subtitleOutcomeSkipped {
		t.Fatalf("outcome = %q", outcome)
	}
	records := sess.Env.Attributes.SubtitleGenerationResults
	if len(records) != 1 || records[0].Source != "none" || records[0].ValidationResult != "skipped" {
		t.Fatalf("records = %+v", records)
	}
	if records[0].SubtitlePath != "" {
		t.Fatalf("skip record has subtitle path: %+v", records[0])
	}
}

func TestProcessSubtitleJobSkipsWhenVerificationFails(t *testing.T) {
	stubAdoptEnvironment(t, "140.0")
	reference := dialogueCues(12, 10, 10)

	// Candidate is a completely different episode's dialogue.
	wrong := dialogueCues(12, 10, 10)
	for i := range wrong {
		wrong[i].Text = strings.ToUpper(wrong[i].Text[:4]) + " unrelated conversation about other things entirely"
	}

	h := &Handler{
		cfg:      &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}, Subtitles: config.SubtitlesConfig{Enabled: true}},
		osClient: emptySearchClient(t),
	}
	sess, job := newAdoptSession(t, h, reference, []byte(srtutil.Format(wrong)))

	outcome, err := h.processSubtitleJob(context.Background(), sess, job)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != subtitleOutcomeSkipped {
		t.Fatalf("outcome = %q", outcome)
	}
	records := sess.Env.Attributes.SubtitleGenerationResults
	if len(records) != 1 || records[0].Source != "none" {
		t.Fatalf("records = %+v", records)
	}
}

func TestProcessSubtitleJobRejectsShortSpanBeforeSync(t *testing.T) {
	stubAdoptEnvironment(t, "140.0")
	reference := dialogueCues(12, 10, 10)

	// A wrong-length subtitle (e.g. a mislabeled 44-minute episode against a
	// 90-minute video) must be rejected before ffsubsync can stretch it to fit.
	short := dialogueCues(6, 10, 10) // spans 10s-63s of the 140s video

	h := &Handler{
		cfg:      &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}, Subtitles: config.SubtitlesConfig{Enabled: true}},
		osClient: emptySearchClient(t),
	}
	sess, job := newAdoptSession(t, h, reference, []byte(srtutil.Format(short)))
	var logBuf strings.Builder
	sess.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))

	outcome, err := h.processSubtitleJob(context.Background(), sess, job)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != subtitleOutcomeSkipped {
		t.Fatalf("outcome = %q", outcome)
	}
	if !strings.Contains(logBuf.String(), "before sync") {
		t.Fatalf("expected pre-sync span rejection, logs:\n%s", logBuf.String())
	}
}

func TestPlanSubtitleJobsSkipsCompletedAndSkippedRecords(t *testing.T) {
	sess := newSubtitleTestSession(t, &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv", ID: 1},
		Episodes: []ripspec.Episode{{Key: "s01e01", Season: 1, Episode: 1}, {Key: "s01e02", Season: 1, Episode: 2}},
	})
	sess.Env.Assets.Ripped = []ripspec.Asset{
		{EpisodeKey: "s01e01", Path: "/a.mkv", Status: ripspec.AssetStatusCompleted},
		{EpisodeKey: "s01e02", Path: "/b.mkv", Status: ripspec.AssetStatusCompleted},
	}
	sess.Env.Attributes.SubtitleGenerationResults = []ripspec.SubtitleGenRecord{
		{EpisodeKey: "s01e01", Source: "none", ValidationResult: "skipped"},
	}

	h := &Handler{cfg: &config.Config{}}
	jobs, skipped := h.planSubtitleJobs(sess)
	if len(jobs) != 1 || jobs[0].Key != "s01e02" {
		t.Fatalf("jobs = %+v", jobs)
	}
	if len(skipped) != 1 || skipped[0] != "s01e01" {
		t.Fatalf("skipped = %+v", skipped)
	}
}

func TestAdoptForFileAdoptsAndReportsRejections(t *testing.T) {
	stubAdoptEnvironment(t, "140.0")
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	reference := dialogueCues(12, 10, 10)
	transcriptDir := t.TempDir()
	transcriptSRT := filepath.Join(transcriptDir, "audio.srt")
	if err := os.WriteFile(transcriptSRT, []byte(srtutil.Format(reference)), 0o644); err != nil {
		t.Fatal(err)
	}

	server := newCandidateSearchServer(t, `{"data":[
		{"id":"a","attributes":{"language":"en","download_count":900,"files":[{"file_id":77}]}},
		{"id":"b","attributes":{"language":"en","download_count":500,"files":[{"file_id":88}]}}
	]}`)
	defer server.Close()

	cfg := &config.Config{}
	// Candidate 77 is wrong content (rejected); candidate 88 matches.
	wrong := dialogueCues(12, 10, 10)
	for i := range wrong {
		wrong[i].Text = fmt.Sprintf("Totally different conversation number %d about nothing here", i+50)
	}
	cacheDir := cfg.OpenSubtitlesCacheDir()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "77.srt"), []byte(srtutil.Format(wrong)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "88.srt"), []byte(srtutil.Format(reference)), 0o644); err != nil {
		t.Fatal(err)
	}

	h := New(cfg, nil, opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, discardLogger()))
	result, err := h.AdoptForFile(context.Background(), AdoptFileRequest{
		VideoPath:  "/staging/Movie.mkv",
		WorkDir:    t.TempDir(),
		TMDBID:     42,
		Transcript: &transcription.TranscribeResult{SRTPath: transcriptSRT, Duration: 123},
		Logger:     discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Segments != len(reference) || result.Language != "en" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Candidate, "file_id=88") || len(result.RejectedCandidates) != 1 {
		t.Fatalf("candidate accounting = %+v", result)
	}
	if _, err := os.Stat(result.SubtitlePath); err != nil {
		t.Fatalf("adopted subtitle missing: %v", err)
	}
	if !strings.HasSuffix(result.SubtitlePath, "Movie.en.srt") {
		t.Fatalf("subtitle path = %q", result.SubtitlePath)
	}
}

func TestAdoptForFileRequiresIdentity(t *testing.T) {
	h := New(&config.Config{}, nil, opensubtitles.New(opensubtitles.Params{APIKey: "key"}, discardLogger()))
	if _, err := h.AdoptForFile(context.Background(), AdoptFileRequest{VideoPath: "/a.mkv", WorkDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "TMDB identity") {
		t.Fatalf("err = %v", err)
	}
	h = New(&config.Config{}, nil, nil)
	if _, err := h.AdoptForFile(context.Background(), AdoptFileRequest{VideoPath: "/a.mkv", WorkDir: t.TempDir(), TMDBID: 5}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v", err)
	}
}
