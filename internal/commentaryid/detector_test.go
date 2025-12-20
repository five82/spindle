package commentaryid

import (
	"context"
	"testing"

	"spindle/internal/config"
	"spindle/internal/media/ffprobe"
	"spindle/internal/subtitles"
)

type stubTranscriber struct {
	calls int
}

func (s *stubTranscriber) TranscribeSnippetPlainText(ctx context.Context, req subtitles.SnippetRequest) (subtitles.SnippetResult, error) {
	s.calls++
	return subtitles.SnippetResult{PlainText: "hello world"}, nil
}

func TestSelectWindowsShortClip(t *testing.T) {
	windows := selectWindows(60)
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].duration != 60 {
		t.Fatalf("expected duration 60, got %v", windows[0].duration)
	}
}

func TestSelectWindowsStandardClip(t *testing.T) {
	windows := selectWindows(600)
	if len(windows) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(windows))
	}
	for _, w := range windows {
		if w.duration != 75 {
			t.Fatalf("expected duration 75, got %v", w.duration)
		}
	}
}

func TestEnglishStereoCandidatesFilters(t *testing.T) {
	streams := []ffprobe.Stream{
		{Index: 0, CodecType: "audio", Channels: 6, Tags: map[string]string{"language": "en"}},
		{Index: 1, CodecType: "audio", Channels: 2, Tags: map[string]string{"language": "en", "title": "Commentary"}},
		{Index: 2, CodecType: "audio", Channels: 2, Tags: map[string]string{"language": "fr"}},
		{Index: 3, CodecType: "audio", Channels: 2, Tags: map[string]string{"language": "en"}, Disposition: map[string]int{"dub": 1}},
		{Index: 4, CodecType: "video"},
	}
	cands := englishStereoCandidates(streams, 0)
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}
	if cands[0].stream.Index != 1 || cands[0].title != "Commentary" {
		t.Fatalf("unexpected candidate %+v", cands[0])
	}
}

func TestKeepListDedupes(t *testing.T) {
	list := keepList(1, []int{2, 1, 3, 2})
	expect := []int{1, 2, 3}
	if len(list) != len(expect) {
		t.Fatalf("expected %v, got %v", expect, list)
	}
	for i := range expect {
		if list[i] != expect[i] {
			t.Fatalf("expected %v, got %v", expect, list)
		}
	}
}

func TestNormalizeLanguageAndTitle(t *testing.T) {
	lang := normalizeLanguage(map[string]string{"LANGUAGE": "EN-us"})
	if lang != "en-us" {
		t.Fatalf("unexpected lang %q", lang)
	}
	title := normalizeTitle(map[string]string{"handler_name": "Director Commentary"})
	if title != "Director Commentary" {
		t.Fatalf("unexpected title %q", title)
	}
}

func TestTruncateAndClamp(t *testing.T) {
	if clamp01(-1) != 0 || clamp01(2) != 1 {
		t.Fatalf("clamp01 bounds failed")
	}
	text := "  hello world  "
	if truncate(text, 5) != "hello" {
		t.Fatalf("unexpected truncate result %q", truncate(text, 5))
	}
}

func TestRefineSkipsWhenSingleAudioStream(t *testing.T) {
	cfg := config.Default()
	cfg.CommentaryDetection.Enabled = true
	stub := &stubTranscriber{}
	detector := &Detector{
		cfg:        &cfg,
		probe:      func(ctx context.Context, binary, path string) (ffprobe.Result, error) { return singleAudioProbe(), nil },
		transcribe: stub,
	}

	out, err := detector.Refine(context.Background(), "/tmp/source.mkv", "")
	if err != nil {
		t.Fatalf("Refine failed: %v", err)
	}
	if out.PrimaryIndex != -1 {
		t.Fatalf("expected PrimaryIndex -1, got %d", out.PrimaryIndex)
	}
	if stub.calls != 0 {
		t.Fatalf("expected no transcription calls, got %d", stub.calls)
	}
}

func TestRefineKeepsOnlyPrimaryWhenNoCandidates(t *testing.T) {
	cfg := config.Default()
	cfg.CommentaryDetection.Enabled = true
	stub := &stubTranscriber{}
	detector := &Detector{
		cfg:        &cfg,
		probe:      func(ctx context.Context, binary, path string) (ffprobe.Result, error) { return noCandidateProbe(), nil },
		transcribe: stub,
	}

	out, err := detector.Refine(context.Background(), "/tmp/source.mkv", "")
	if err != nil {
		t.Fatalf("Refine failed: %v", err)
	}
	if out.PrimaryIndex < 0 {
		t.Fatalf("expected PrimaryIndex >= 0, got %d", out.PrimaryIndex)
	}
	if len(out.KeepIndices) != 1 || out.KeepIndices[0] != out.PrimaryIndex {
		t.Fatalf("expected keep list with primary index, got %+v", out.KeepIndices)
	}
	if stub.calls != 0 {
		t.Fatalf("expected no transcription calls, got %d", stub.calls)
	}
}

func singleAudioProbe() ffprobe.Result {
	return ffprobe.Result{
		Streams: []ffprobe.Stream{
			{Index: 0, CodecType: "audio", Channels: 2, Tags: map[string]string{"language": "en"}},
		},
		Format: ffprobe.Format{Duration: "3600"},
	}
}

func noCandidateProbe() ffprobe.Result {
	return ffprobe.Result{
		Streams: []ffprobe.Stream{
			{Index: 0, CodecType: "audio", Channels: 8, Tags: map[string]string{"language": "en"}},
			{Index: 1, CodecType: "audio", Channels: 2, Tags: map[string]string{"language": "fr"}},
		},
		Format: ffprobe.Format{Duration: "3600"},
	}
}
