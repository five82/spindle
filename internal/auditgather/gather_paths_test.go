package auditgather

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

func TestGatherRipCacheStates(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: t.TempDir()}, RipCache: config.RipCacheConfig{Enabled: true, MaxGiB: 1}}
	item := &httpapi.ItemResponse{DiscFingerprint: "fingerprint"}
	if got := gatherRipCache(cfg, item); got.Found || got.Disabled || !strings.Contains(got.Path, "fingerprint") {
		t.Fatalf("cache miss: %+v", got)
	}
	cache := ripcache.New(cfg.RipCacheDir(), 1)
	if err := cache.WriteMetadata("fingerprint", ripcache.EntryMetadata{Fingerprint: "fingerprint", DiscTitle: "Show", TitleCount: 2}); err != nil {
		t.Fatal(err)
	}
	if got := gatherRipCache(cfg, item); !got.Found || got.Metadata == nil || got.Metadata.TitleCount != 2 {
		t.Fatalf("cache hit: %+v", got)
	}
	cfg.RipCache.Enabled = false
	if got := gatherRipCache(cfg, item); !got.Disabled {
		t.Fatalf("disabled: %+v", got)
	}
}

// A delivered file that cannot be probed must surface as a probe error, never
// be replaced by an earlier-stage intermediate that still probes cleanly.
func TestProbeFailureOfDeliveredFileIsReported(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "missing.mkv")
	p := probeFile(ctx, missing, ripspec.AssetKindFinal, "s01_001")
	if p.Path != missing || p.Error == "" || p.Role != ripspec.AssetKindFinal {
		t.Fatalf("probe failure: %+v", p)
	}
	env := &ripspec.Envelope{Assets: ripspec.Assets{Final: []ripspec.Asset{{EpisodeKey: "s01_001", Path: missing, Status: ripspec.AssetStatusCompleted}}}}
	if got := gatherMediaProbes(ctx, env, "tv"); len(got) != 0 {
		t.Fatalf("TV without encoded assets: %+v", got)
	}
	env.Assets.Encoded = []ripspec.Asset{{EpisodeKey: "s01_001", Path: "/encoded-intermediate.mkv", Status: ripspec.AssetStatusCompleted}}
	got := gatherMediaProbes(ctx, env, "tv")
	if len(got) != 1 || got[0].Path != missing || got[0].Role != ripspec.AssetKindFinal || got[0].Error == "" {
		t.Fatalf("delivered-file probe failure not reported: %+v", got)
	}
	if got := gatherMediaProbes(ctx, env, "movie"); len(got) != 0 {
		t.Fatalf("movie without main: %+v", got)
	}

	cfg := &config.Config{Paths: config.PathsConfig{StateDir: t.TempDir()}}
	env.Metadata.MediaType = "tv"
	env.Assets.Encoded = append(env.Assets.Encoded, ripspec.Asset{EpisodeKey: "s01_002", Path: "/missing-2.mkv", Status: ripspec.AssetStatusCompleted})
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Gather(ctx, cfg, &httpapi.ItemResponse{ID: 3, Stage: string(queue.StageCompleted), RipSpec: data})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range report.Analysis.Anomalies {
		found = found || (a.Category == "media" && strings.Contains(a.Message, "2 media probe(s) failed"))
	}
	if !found {
		t.Fatalf("probe failures not flagged: %+v", report.Analysis.Anomalies)
	}
}

func TestGatherEncodedItemWithMissingMediaAndBadSnapshot(t *testing.T) {
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: t.TempDir()}}
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "movie", DiscSource: "bluray"}, Assets: ripspec.Assets{Encoded: []ripspec.Asset{{EpisodeKey: "main", Path: "/nonexistent.mkv", Status: ripspec.AssetStatusCompleted}}}}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	item := &httpapi.ItemResponse{ID: 7, DiscTitle: "Movie", Stage: string(queue.StageCompleted), RipSpec: data, Encoding: json.RawMessage(`{"bad`)}
	report, err := Gather(context.Background(), cfg, item)
	if err != nil {
		t.Fatal(err)
	}
	if !report.StageGate.PhaseEncoded || report.Encoding != nil || len(report.Media) != 1 || report.Media[0].Error == "" || report.Analysis == nil {
		t.Fatalf("report: %+v", report)
	}
	if len(report.Errors) == 0 || !strings.Contains(strings.Join(report.Errors, " "), "encoding snapshot") {
		t.Fatalf("missing parse error: %v", report.Errors)
	}
}
