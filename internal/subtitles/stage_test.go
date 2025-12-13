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
	"spindle/internal/testsupport"
)

func TestStagePersistsRipSpecPerEpisode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := testsupport.NewConfig(t)
	cfg.SubtitlesEnabled = true
	cfg.OpenSubtitlesEnabled = false
	cfg.WhisperXCacheDir = t.TempDir()

	store := testsupport.MustOpenStore(t, cfg)

	stub := setupInspectAndStub(t, 600, false)
	runner := newBlockingWhisperXRunner(stub)

	service := NewService(cfg, logging.NewNop(), WithCommandRunner(runner.Runner), WithoutDependencyCheck())
	stage := NewStage(store, service, logging.NewNop())

	item, err := store.NewDisc(context.Background(), "TV Disc", "fp-subtitle-stage")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	item.Status = queue.StatusSubtitling
	item.DiscFingerprint = "SUBTITLESTAGETESTFP"

	stagingRoot := item.StagingRoot(cfg.StagingDir)
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
		Metadata: map[string]any{"media_type": "tv"},
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
	if _, ok := midEnv.Assets.FindAsset("subtitled", ep1Key); !ok {
		t.Fatalf("expected subtitled asset for %s mid-run", ep1Key)
	}
	if _, ok := midEnv.Assets.FindAsset("subtitled", ep2Key); ok {
		t.Fatalf("did not expect subtitled asset for %s mid-run", ep2Key)
	}
	if midEnv.Attributes == nil {
		t.Fatalf("expected subtitle generation attributes to be populated")
	}
	resultsAny, ok := midEnv.Attributes["subtitle_generation_results"]
	if !ok {
		t.Fatalf("expected subtitle_generation_results attribute mid-run")
	}
	results, ok := resultsAny.([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("expected 1 subtitle_generation_results entry mid-run, got %#v", resultsAny)
	}
	entry, ok := results[0].(map[string]any)
	if !ok {
		t.Fatalf("expected subtitle_generation_results entry to be map, got %#v", results[0])
	}
	if entry["episode_key"] != strings.ToLower(ep1Key) {
		t.Fatalf("expected episode_key %q, got %#v", strings.ToLower(ep1Key), entry["episode_key"])
	}
	if entry["source"] != "whisperx" {
		t.Fatalf("expected source whisperx, got %#v", entry["source"])
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
	if name == whisperXCommand && containsArg(args, "--output_dir") && !containsArg(args, "--download-models") && !containsArg(args, "--model_cache_only") {
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
