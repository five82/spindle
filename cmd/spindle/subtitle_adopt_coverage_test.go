package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/subtitle"
	"github.com/five82/spindle/internal/transcription"
)

func TestStandaloneAdoptSidecarWithCachedCandidate(t *testing.T) {
	oldVerbose := flagVerbose
	t.Cleanup(func() { flagVerbose = oldVerbose })
	flagVerbose = true
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"ffprobe":  "#!/bin/sh\necho '{\"streams\":[{\"codec_type\":\"video\"}],\"format\":{\"duration\":\"140.0\"}}'\n",
		"uvx":      "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = -i ]; then shift; input=$1; fi; if [ \"$1\" = -o ]; then shift; output=$1; fi; shift; done\ncp \"$input\" \"$output\"\n",
		"mkvmerge": "#!/bin/sh\nif [ \"$1\" = --identify ]; then if [ \"$HAS_SUBTITLE\" = yes ]; then echo 'Track ID 2: subtitles'; fi; exit 0; fi\nif [ \"$1\" != -o ]; then exit 1; fi\nprintf 'muxed video' > \"$2\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var cues strings.Builder
	for i := 0; i < 12; i++ {
		start := i*10 + 5
		end := start + 8
		fmt.Fprintf(&cues, "%d\n00:%02d:%02d,000 --> 00:%02d:%02d,000\nThis is spoken dialogue line number %d with unique words\n\n", i+1, start/60, start%60, end/60, end%60, i)
	}
	reference := filepath.Join(dir, "reference.srt")
	if err := os.WriteFile(reference, []byte(cues.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cache := cfg.OpenSubtitlesCacheDir()
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "77.srt"), []byte(cues.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"a","attributes":{"language":"en","files":[{"file_id":77}]}}]}`))
	}))
	defer server.Close()
	handler := subtitle.New(cfg, nil, opensubtitles.New(opensubtitles.Params{APIKey: "key", BaseURL: server.URL}, nil))
	video := filepath.Join(dir, "Movie.mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "output")
	var output bytes.Buffer
	params := standaloneAdoptParams{file: video, identity: subtitle.PathIdentity{TMDBID: 42}, transcript: &transcription.TranscribeResult{SRTPath: reference, Duration: 140}, workDir: filepath.Join(dir, "work"), outputDir: dest, sidecarMode: true, out: &output}
	if err := adoptStandaloneSubtitle(context.Background(), handler, params); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Saved sidecar:") || !strings.Contains(output.String(), "Gate:") {
		t.Fatalf("progress: %s", output.String())
	}
	sidecar := filepath.Join(dest, "Movie.en.srt")
	if data, err := os.ReadFile(sidecar); err != nil || !strings.Contains(string(data), "unique words") {
		t.Fatalf("sidecar: %q %v", data, err)
	}
	if data, err := os.ReadFile(video); err != nil || string(data) != "video" {
		t.Fatalf("video changed: %q %v", data, err)
	}
	params.sidecarMode = false
	for _, existing := range []string{"no", "yes"} {
		t.Setenv("HAS_SUBTITLE", existing)
		output.Reset()
		if err := adoptStandaloneSubtitle(context.Background(), handler, params); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(video); err != nil || string(data) != "muxed video" {
			t.Fatalf("muxed output: %q %v", data, err)
		}
		want := "Muxing subtitle into MKV"
		if existing == "yes" {
			want = "Replacing existing subtitle tracks"
		}
		if !strings.Contains(output.String(), want) {
			t.Fatalf("progress: %s", output.String())
		}
	}
}
