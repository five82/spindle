package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services/whisperx"
	"spindle/internal/testsupport"
)

func TestStagePersistsRipSpecPerEpisode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := testsupport.NewConfig(t)
	cfg.Subtitles.Enabled = true
	cfg.Subtitles.OpenSubtitlesEnabled = false
	cfg.Paths.WhisperXCacheDir = t.TempDir()

	store := testsupport.MustOpenStore(t, cfg)

	stub := setupInspectAndStub(t, 600, false)
	runner := newBlockingWhisperXRunner(stub)

	service := NewService(cfg, logging.NewNop(), WithCommandRunner(runner.Runner), WithoutDependencyCheck())
	stage := NewGenerator(store, service, logging.NewNop())

	item, err := store.NewDisc(context.Background(), "TV Disc", "fp-subtitle-stage")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusSubtitling
	item.DiscFingerprint = "SUBTITLESTAGETESTFP"

	stagingRoot := item.StagingRoot(cfg.Paths.StagingDir)
	if strings.TrimSpace(stagingRoot) == "" {
		t.Fatalf("expected staging root")
	}
	encodedDir := filepath.Join(stagingRoot, "encoded")
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatalf("mkdir encoded: %v", err)
	}

	season := 1
	ep1Key := ripspec.EpisodeKey(season, 1)
	ep2Key := ripspec.EpisodeKey(season, 2)
	ep1Encoded := filepath.Join(encodedDir, "ep1.mkv")
	ep2Encoded := filepath.Join(encodedDir, "ep2.mkv")
	testsupport.WriteFile(t, ep1Encoded, 1024)
	testsupport.WriteFile(t, ep2Encoded, 1024)
	item.EncodedFile = ep1Encoded

	env := ripspec.Envelope{
		Metadata: ripspec.EnvelopeMetadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: ep1Key, TitleID: 1, Season: season, Episode: 1, EpisodeTitle: "Episode One"},
			{Key: ep2Key, TitleID: 2, Season: season, Episode: 2, EpisodeTitle: "Episode Two"},
		},
		Assets: ripspec.Assets{
			Encoded: []ripspec.Asset{
				{EpisodeKey: ep1Key, TitleID: 1, Path: ep1Encoded},
				{EpisodeKey: ep2Key, TitleID: 2, Path: ep2Encoded},
			},
		},
	}
	encodedSpec, err := env.Encode()
	if err != nil {
		t.Fatalf("encode rip spec: %v", err)
	}
	item.RipSpecData = encodedSpec

	if err := store.Update(context.Background(), item); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := stage.Prepare(context.Background(), item); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- stage.Execute(context.Background(), item)
	}()

	select {
	case <-runner.startedSecond:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for episode 2 subtitles")
	}

	mid, err := store.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID (mid): %v", err)
	}
	midEnv, err := ripspec.Parse(mid.RipSpecData)
	if err != nil {
		t.Fatalf("parse rip spec (mid): %v", err)
	}
	if len(midEnv.Assets.Subtitled) != 1 {
		t.Fatalf("expected 1 subtitled asset mid-run, got %d", len(midEnv.Assets.Subtitled))
	}
	if _, ok := midEnv.Assets.FindAsset(ripspec.AssetKindSubtitled, ep1Key); !ok {
		t.Fatalf("expected subtitled asset for %s mid-run", ep1Key)
	}
	if _, ok := midEnv.Assets.FindAsset(ripspec.AssetKindSubtitled, ep2Key); ok {
		t.Fatalf("did not expect subtitled asset for %s mid-run", ep2Key)
	}
	results := midEnv.Attributes.SubtitleGenerationResults
	if len(results) != 1 {
		t.Fatalf("expected 1 subtitle_generation_results entry mid-run, got %d", len(results))
	}
	if results[0].EpisodeKey != strings.ToLower(ep1Key) {
		t.Fatalf("expected episode_key %q, got %q", strings.ToLower(ep1Key), results[0].EpisodeKey)
	}
	if results[0].Source != "whisperx" {
		t.Fatalf("expected source whisperx, got %q", results[0].Source)
	}

	close(runner.allowSecond)
	if err := <-errCh; err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

type blockingWhisperXRunner struct {
	stub          *whisperXStub
	count         int
	startedSecond chan struct{}
	allowSecond   chan struct{}
}

func newBlockingWhisperXRunner(stub *whisperXStub) *blockingWhisperXRunner {
	return &blockingWhisperXRunner{
		stub:          stub,
		startedSecond: make(chan struct{}),
		allowSecond:   make(chan struct{}),
	}
}

func (r *blockingWhisperXRunner) Runner(ctx context.Context, name string, args ...string) error {
	if name == whisperx.UVXCommand && containsArg(args, "--output_dir") && !containsArg(args, "--download-models") && !containsArg(args, "--model_cache_only") {
		r.count++
		if r.count == 2 {
			close(r.startedSecond)
			select {
			case <-r.allowSecond:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return r.stub.Runner(ctx, name, args...)
}

// TestForcedSubtitlePathIncludedInMux verifies that when ForcedSubtitlePath is set
// on GenerateResult, it gets included in the mux request. This was a bug where
// tryForcedSubtitlesForTarget downloaded the forced subtitle but didn't return the
// path, so tryMuxSubtitles never received it.
func TestForcedSubtitlePathIncludedInMux(t *testing.T) {
	tmpDir := t.TempDir()
	mkvPath := filepath.Join(tmpDir, "video.mkv")
	regularSRT := filepath.Join(tmpDir, "video.en.srt")
	forcedSRT := filepath.Join(tmpDir, "video.en.forced.srt")

	// Create dummy files
	testsupport.WriteFile(t, mkvPath, 1024)
	if err := os.WriteFile(regularSRT, []byte("1\n00:00:01,000 --> 00:00:02,000\nRegular\n"), 0644); err != nil {
		t.Fatalf("write regular SRT: %v", err)
	}
	if err := os.WriteFile(forcedSRT, []byte("1\n00:00:01,000 --> 00:00:02,000\nForced\n"), 0644); err != nil {
		t.Fatalf("write forced SRT: %v", err)
	}

	muxer := NewMuxer(nil)

	// Track what paths were passed to mkvmerge
	var muxedPaths []string
	muxer.WithCommandRunner(func(ctx context.Context, name string, args ...string) error {
		if name == "mkvmerge" {
			for _, arg := range args {
				if strings.HasSuffix(arg, ".srt") {
					muxedPaths = append(muxedPaths, arg)
				}
			}
			// Create output file to simulate mkvmerge success
			for i, arg := range args {
				if arg == "-o" && i+1 < len(args) {
					if err := os.WriteFile(args[i+1], []byte("muxed"), 0644); err != nil {
						return err
					}
					break
				}
			}
		}
		return nil
	})

	// This simulates what tryMuxSubtitles does internally when it collects paths
	// from GenerateResult. The fix ensures ForcedSubtitlePath gets populated.
	result := GenerateResult{
		SubtitlePath:       regularSRT,
		ForcedSubtitlePath: forcedSRT, // This is the fix - forced path should be set
	}

	// Collect paths the same way tryMuxSubtitles does
	var srtPaths []string
	if strings.TrimSpace(result.SubtitlePath) != "" {
		srtPaths = append(srtPaths, result.SubtitlePath)
	}
	if strings.TrimSpace(result.ForcedSubtitlePath) != "" {
		srtPaths = append(srtPaths, result.ForcedSubtitlePath)
	}

	// Verify both paths are collected
	if len(srtPaths) != 2 {
		t.Fatalf("expected 2 SRT paths collected, got %d: %v", len(srtPaths), srtPaths)
	}

	// Call muxer directly with the collected paths
	_, err := muxer.MuxSubtitles(context.Background(), MuxRequest{
		MKVPath:           mkvPath,
		SubtitlePaths:     srtPaths,
		Language:          "en",
		StripExistingSubs: true,
	})
	if err != nil {
		t.Fatalf("MuxSubtitles failed: %v", err)
	}

	// Verify both subtitle paths were passed to mkvmerge
	if len(muxedPaths) != 2 {
		t.Fatalf("expected 2 SRT paths to be muxed, got %d: %v", len(muxedPaths), muxedPaths)
	}

	hasRegular := false
	hasForced := false
	for _, p := range muxedPaths {
		if strings.Contains(p, "video.en.srt") && !strings.Contains(p, "forced") {
			hasRegular = true
		}
		if strings.Contains(p, "forced.srt") {
			hasForced = true
		}
	}

	if !hasRegular {
		t.Errorf("regular subtitle not found in muxed paths: %v", muxedPaths)
	}
	if !hasForced {
		t.Errorf("forced subtitle not found in muxed paths: %v", muxedPaths)
	}
}
