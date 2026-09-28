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

func TestRestoreRejectsStaleOrIncompleteTVCache(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fileCount int
		videoEnd  int
	}{
		{"old seven-title selection", 7, 1380},
		{"audio outlives video", 6, 112},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			probe := `#!/bin/sh
case " $* " in
 *-show_entries\ packet=pts_time*) printf '0\n'"$VIDEO_END"'\n';;
 *) printf '%s\n' '{"streams":[{"codec_type":"video"},{"codec_type":"audio"}],"format":{"duration":"593.216"}}';;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(probe), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if tc.videoEnd == 112 {
				t.Setenv("VIDEO_END", "112")
			} else {
				t.Setenv("VIDEO_END", "1380")
			}
			episodes := make([]ripspec.Episode, 6)
			for i := range episodes {
				episodes[i] = ripspec.Episode{Key: ripspec.PlaceholderKey(3, i+1), TitleID: i, RuntimeSeconds: 1377}
			}
			sess := newRipSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "tv"}, Episodes: episodes})
			src := t.TempDir()
			for i := 0; i < tc.fileCount; i++ {
				f, err := os.Create(filepath.Join(src, "show_t0"+string(rune('0'+i))+".mkv"))
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Truncate(minRipFileSizeBytes + 1); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			cache := ripcache.New(t.TempDir(), 1)
			if err := cache.Register(sess.Item.DiscFingerprint, src, nil); err != nil {
				t.Fatal(err)
			}
			if err := cache.WriteMetadata(sess.Item.DiscFingerprint, ripcache.EntryMetadata{TitleCount: tc.fileCount}); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "ripped")
			h := New(&config.Config{}, nil, cache, nil, NoTitleOverride)
			if restored, err := h.restoreFromRipCache(context.Background(), sess, dest); restored || err != nil {
				t.Fatalf("invalid cache restored: %t %v", restored, err)
			}
			if cache.HasCache(sess.Item.DiscFingerprint) {
				t.Fatal("invalid cache still present")
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("restored files remain: %v", err)
			}
		})
	}
}
