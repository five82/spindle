package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestEncodeAndDaemonCommandFailureBoundaries(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "absent.sock")
	cmd := newEncodeCmd()
	if err := cmd.RunE(cmd, []string{filepath.Join(dir, "missing.mkv")}); err == nil || !strings.Contains(err.Error(), "input file:") {
		t.Fatalf("missing input: %v", err)
	}
	input := filepath.Join(dir, "input.mkv")
	if err := os.WriteFile(input, []byte("bad media"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("output-dir", filepath.Join(blocker, "out")); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{input}); err == nil || !strings.Contains(err.Error(), "create output dir:") {
		t.Fatalf("invalid output directory: %v", err)
	}
	cfg.Paths.StateDir = blocker
	if err := newStartCmd().RunE(nil, nil); err == nil || !strings.Contains(err.Error(), "create log directory:") {
		t.Fatalf("start with blocked directory: %v", err)
	}
	if err := newRestartCmd().RunE(nil, nil); err == nil || !strings.Contains(err.Error(), "start:") {
		t.Fatalf("restart with blocked directory: %v", err)
	}
}

func TestSubtitleCommandResolvesIdentityBeforeExternalWork(t *testing.T) {
	oldCfg, oldQuiet := cfg, flagQuiet
	t.Cleanup(func() { cfg, flagQuiet = oldCfg, oldQuiet })
	cfg = &config.Config{}
	file := filepath.Join(t.TempDir(), "feature.mkv")
	if err := os.WriteFile(file, []byte("not media"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		flags map[string]string
		want  string
	}{
		{"one file explicit identity", []string{file}, map[string]string{"tmdb-id": "42", "work-dir": t.TempDir()}, ""},
		{"two files select primary audio", []string{file, file}, map[string]string{"tmdb-id": "42", "work-dir": t.TempDir()}, "select primary audio"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newGensubtitleCmd()
			for k, v := range tc.flags {
				if err := cmd.Flags().Set(k, v); err != nil {
					t.Fatal(err)
				}
			}
			err := cmd.RunE(cmd, tc.args)
			if err == nil {
				t.Fatal("expected failure on invalid media")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if tc.want == "" && !strings.Contains(err.Error(), "got no subtitles") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestDebugCommentaryClassificationWithStubTranscripts(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/systemone" {
			t.Errorf("debug used wrong endpoint: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"answers":{"decision":{"type":"choice","choice":"commentary","confidence":0.1,"probabilities":{"commentary":0.9,"not_commentary":0.1}}}}`)
	}))
	defer server.Close()
	cfg = &config.Config{}
	cfg.LLM.APIKey = "test"
	cfg.LLM.BaseURL = server.URL
	cfg.LLM.Model = "test"
	cfg.Commentary.SimilarityThreshold = 0.8
	dir := t.TempDir()
	file := filepath.Join(dir, "film.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{
		"ffprobe": `#!/bin/sh
printf '{"streams":[{"codec_type":"audio","index":0,"channels":6},{"codec_type":"audio","index":1,"channels":2,"tags":{"title":"Director commentary"}}]}'
`,
		"ffmpeg": `#!/bin/sh
for arg do last="$arg"; done
printf wave > "$last"
`,
		"uvx": `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = '--output-dir' ]; then
    shift
    if echo "$1" | grep -q 'audio-0'; then text='hero travels the ocean waves searching for treasure'; else text='director discusses photography lighting production crew camera'; fi
    printf '1\n00:00:01,000 --> 00:00:03,500\n%s\n' "$text" > "$1/audio.srt"
    printf '{}' > "$1/audio.json"
  fi
  shift
done
`,
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd := newDebugCommentaryCmd()
	for _, threshold := range []float64{0.8, 0} {
		cfg.Commentary.SimilarityThreshold = threshold
		output := captureStdout(t, func() {
			if err := cmd.RunE(cmd, []string{file}); err != nil {
				t.Fatal(err)
			}
		})
		for _, want := range []string{"LLM decision:", "commentary", "Commentary probability:", "0.900", "Jev commentary probability 0.9 >= 0.65"} {
			if !strings.Contains(output, want) {
				t.Fatalf("missing %q in classification: %s", want, output)
			}
		}
	}
}

func TestDebugCommentaryProbePaths(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	cfg = &config.Config{}
	dir := t.TempDir()
	file := filepath.Join(dir, "film.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "ffprobe")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd := newDebugCommentaryCmd()
	if err := cmd.RunE(cmd, []string{filepath.Join(dir, "missing.mkv")}); err == nil {
		t.Fatal("missing file accepted")
	}
	for _, tc := range []struct{ name, output, want string }{
		{"probe fails", "exit 1", "ffprobe:"},
		{"no audio", `printf '{"streams":[{"codec_type":"video"}]}'`, "No audio streams"},
		{"one audio", `printf '{"streams":[{"codec_type":"audio","index":1,"codec_name":"aac","channels":2,"tags":{"language":"eng","title":"Main"}}]}'`, "Only one audio stream"},
		{"two audio without LLM", `printf '{"streams":[{"codec_type":"audio","index":1},{"codec_type":"audio","index":2}]}'`, "LLM not configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+tc.output+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			var err error
			output := captureStdout(t, func() { err = cmd.RunE(cmd, []string{file}) })
			if tc.name == "probe fails" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error: %v", err)
				}
			} else if err != nil || !strings.Contains(output, tc.want) {
				t.Fatalf("output: %s error: %v", output, err)
			}
		})
	}
}
