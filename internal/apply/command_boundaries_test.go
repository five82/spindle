package apply

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

// Executable stand-ins exercise the same command/rename boundaries as production
// without requiring media tools or real MKVs on the test host.
func installTool(t *testing.T, name, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func probeJSON(t *testing.T, streams ...ffprobe.Stream) string {
	t.Helper()
	b, err := json.Marshal(ffprobe.Result{Streams: streams, Format: ffprobe.Format{Duration: "100"}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRefineAudioTargetsCommandBoundaries(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "video.mkv")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	installTool(t, "ffprobe", "printf '%s' \"$PROBE_JSON\"\n")
	t.Setenv("PROBE_JSON", probeJSON(t,
		ffprobe.Stream{CodecType: "video"},
		ffprobe.Stream{CodecType: "audio", CodecName: "aac", Channels: 2, Tags: map[string]string{"language": "eng"}, Disposition: map[string]int{"default": 1}},
		ffprobe.Stream{CodecType: "audio", CodecName: "aac", Channels: 2, Tags: map[string]string{"language": "fra"}},
	))
	installTool(t, "ffmpeg", "printf '%s\\n' \"$@\" > \"$FFMPEG_ARGS\"\nfor last do :; done\nprintf 'refined' > \"$last\"\n")
	argsPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("FFMPEG_ARGS", argsPath)
	result, err := refineAudioTargets(ctx, slog.Default(), []string{path, path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.KeptIndices, []int{0}) {
		t.Fatalf("kept = %v", result.KeptIndices)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "refined" {
		t.Fatalf("file = %q", data)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "0:a:0\n") || strings.Contains(string(args), "0:a:1\n") || !strings.Contains(string(args), "-disposition:a:0\ndefault") {
		t.Fatalf("ffmpeg args: %s", args)
	}

	t.Setenv("PROBE_JSON", probeJSON(t, ffprobe.Stream{CodecType: "video"}))
	result, err = refineAudioTargets(ctx, slog.Default(), []string{path}, nil)
	if err != nil || len(result.KeptIndices) != 0 {
		t.Fatalf("no audio: %+v, %v", result, err)
	}
	t.Setenv("PROBE_JSON", "invalid")
	if _, err = refineAudioTargets(ctx, slog.Default(), []string{path}, nil); err == nil || !strings.Contains(err.Error(), "ffprobe") {
		t.Fatalf("bad probe: %v", err)
	}
}

func TestRemuxFailurePreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "film.mkv")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	installTool(t, "ffmpeg", "for last do :; done\nprintf 'partial' > \"$last\"\nexit 1\n")
	if err := remuxAudioTracks(context.Background(), slog.Default(), path, []int{0}); err == nil {
		t.Fatal("expected remux error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("original changed: %q", data)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".refine-film.mkv")); !os.IsNotExist(err) {
		t.Fatalf("temp file remains: %v", err)
	}
}

func TestMeasureVideoDurationsDistinctPathsAndPartialFailure(t *testing.T) {
	// ffprobe passes the input path as its final argument.
	t.Setenv("PROBE_JSON", probeJSON(t, ffprobe.Stream{CodecType: "video", Duration: "90"}))
	installTool(t, "ffprobe", "for last do :; done\ncase \"$last\" in *bad*) exit 1;; esac\nprintf '%s' \"$PROBE_JSON\"\n")
	durations, err := measureVideoDurations(context.Background(), []string{"good", "good", "bad"})
	if err == nil || !strings.Contains(err.Error(), "bad") || durations["good"] != 90 || len(durations) != 1 {
		t.Fatalf("durations = %v, err = %v", durations, err)
	}
}

func TestFinalOutputProbeAndSync(t *testing.T) {
	installTool(t, "ffprobe", "for last do :; done\ncase \"$last\" in *source*) printf '%s' \"$SOURCE_JSON\";; *missing*) exit 1;; *) printf '%s' \"$OUTPUT_JSON\";; esac\n")
	t.Setenv("SOURCE_JSON", probeJSON(t, ffprobe.Stream{CodecType: "video", StartTime: "0", Duration: "100"}, ffprobe.Stream{CodecType: "audio", StartTime: "0.5", Duration: "100", Tags: map[string]string{"language": "eng"}}))
	t.Setenv("OUTPUT_JSON", probeJSON(t, ffprobe.Stream{CodecType: "video", StartTime: "0", Duration: "100"}, ffprobe.Stream{CodecType: "audio", StartTime: "0.5", Duration: "100", Tags: map[string]string{"language": "eng"}, Disposition: map[string]int{"default": 1}}))
	env := &ripspec.Envelope{}
	exp := finalExpectation{key: "main", encodedPath: "output.mkv", keptAudio: 1}
	entry := checkFinalOutput(context.Background(), env, exp, 0)
	if !entry.Passed || entry.AVSync == nil || entry.AVSync.Error != "ripped source asset unavailable" {
		t.Fatalf("missing source: %+v", entry)
	}
	exp.encodedPath = "missing.mkv"
	entry = checkFinalOutput(context.Background(), env, exp, 0)
	if entry.Error == "" || entry.AVSync != nil {
		t.Fatalf("missing output: %+v", entry)
	}
}
