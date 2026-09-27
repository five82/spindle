package ripper

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

func TestRunFreshMoviePersistsValidatedRipAndCache(t *testing.T) {
	bin := t.TempDir()
	for name, script := range map[string]string{
		"ffprobe":    "#!/bin/sh\nprintf '%s\\n' '{\"streams\":[{\"codec_type\":\"video\"},{\"codec_type\":\"audio\"}],\"format\":{\"duration\":\"120\"}}'\n",
		"makemkvcon": "#!/bin/sh\nfor arg do\n case \"$arg\" in */ripped) truncate -s 52428801 \"$arg/movie_t01.mkv\";; esac\ndone\nprintf '%s\\n' 'PRGV:100,100,65536' 'MSG:5036,0,2,\"Copy complete\",\"%1 titles saved, %2 failed\",1,0'\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sess := ripCoverageSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}, Titles: []ripspec.Title{{ID: 1, Duration: 7200, SizeBytes: 52428801}}})
	cache := ripcache.New(t.TempDir(), 1)
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}, MakeMKV: config.MakeMKVConfig{OpticalDrive: "disc:0", RipTimeout: 10}}, nil, cache, nil, NoTitleOverride)
	if err := h.Run(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	fresh, err := sess.Store.GetByID(sess.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	asset, ok := saved.Assets.FindAsset(ripspec.AssetKindRipped, "main")
	if !ok || !asset.IsCompleted() || asset.TitleID != 1 {
		t.Fatalf("asset: %+v %t", asset, ok)
	}
	if meta, err := cache.GetMetadata(sess.Item.DiscFingerprint); err != nil || meta == nil || meta.TitleCount != 1 {
		t.Fatalf("cache: %+v %v", meta, err)
	}
}
