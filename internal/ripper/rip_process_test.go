package ripper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripspec"
)

func TestRipTitlePersistsEachEpisodeAndPropagatesFailure(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$RIP_FAIL" = yes ]; then exit 1; fi
for arg do out="$arg"; done
# --minlength is last, output directory is second to last.
for arg do
 case "$arg" in */ripped) truncate -s 11000000 "$arg/show_t01.mkv";; esac
done
printf '%s\n' 'PRGT:5024,0,"Saving"' 'PRGV:100,100,65536' 'MSG:5036,0,2,"Copy complete","%1 titles saved, %2 failed",1,0'
`
	if err := os.WriteFile(filepath.Join(bin, "makemkvcon"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	probe := `#!/bin/sh
case " $* " in
 *-show_entries\ packet=pts_time*) printf '0\n120\n';;
 *) printf '%s\n' '{"streams":[{"codec_type":"video"},{"codec_type":"audio"}],"format":{"duration":"120"}}';;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(probe), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := filepath.Join(t.TempDir(), "ripped")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := newRipSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "tv"}, Episodes: []ripspec.Episode{{Key: "one", TitleID: 1}}})
	h := &Handler{cfg: &config.Config{MakeMKV: config.MakeMKVConfig{OpticalDrive: "disc:0", RipTimeout: 10}}}
	if err := h.ripTitles(context.Background(), sess, dir, []ripspec.Title{{ID: 1, Duration: 120}}); err != nil {
		t.Fatal(err)
	}
	asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindRipped, "one")
	if !ok || !strings.HasSuffix(asset.Path, "show_t01.mkv") {
		t.Fatalf("rip not persisted: %+v %t", asset, ok)
	}
	fresh, err := sess.Store.GetByID(sess.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Assets.FindAsset(ripspec.AssetKindRipped, "one"); !ok {
		t.Fatalf("rip missing from store: %+v", saved.Assets)
	}
	t.Setenv("RIP_FAIL", "yes")
	if err := h.ripTitle(context.Background(), sess, dir, ripspec.Title{ID: 2}, 0, 1, "two"); err == nil || !strings.Contains(err.Error(), "rip title 2") {
		t.Fatalf("rip failure: %v", err)
	}
}
