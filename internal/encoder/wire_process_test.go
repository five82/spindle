package encoder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/reel"
)

// Intercept the re-executed test binary before the test flag parser. The
// worker protocol can then be tested without invoking Reel or encoding media.
func TestMain(m *testing.M) {
	if os.Getenv("SPINDLE_TEST_ENCODE_WORKER") != "" && len(os.Args) > 1 && os.Args[1] == "encode-worker" {
		switch os.Getenv("SPINDLE_TEST_ENCODE_WORKER") {
		case "success":
			_, _ = fmt.Fprintln(os.Stdout, `not-json`)
			_, _ = fmt.Fprintln(os.Stdout, `{"event":"warning","payload":{}`)
			_, _ = fmt.Fprintln(os.Stdout, `{"event":"encoding_started","payload":{"total_frames":42}}`)
			_ = json.NewEncoder(os.Stdout).Encode(wireEvent{Event: wireResult, Payload: mustWorkerResult()})
		case "failure":
			_, _ = fmt.Fprintln(os.Stdout, `{"event":"failure","payload":{"message":"reel failed"}}`)
		case "exit":
			_, _ = fmt.Fprintln(os.Stderr, "worker crashed")
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func mustWorkerResult() json.RawMessage {
	data, _ := json.Marshal(reel.Result{OutputFile: "encoded.mkv", OriginalSize: 100, EncodedSize: 40, ValidationPassed: true})
	return data
}

func TestRunEncodingWithWorkerProcess(t *testing.T) {
	for _, tc := range []struct {
		mode, wantError string
	}{
		{"success", ""},
		{"failure", "encoding failed for 1 of 1 jobs"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("SPINDLE_TEST_ENCODE_WORKER", tc.mode)
			base := t.TempDir()
			sess := encoderSession(t, ripspec.Envelope{
				Metadata: ripspec.Metadata{MediaType: "movie"},
				Assets:   ripspec.Assets{Ripped: []ripspec.Asset{{EpisodeKey: "main", Path: "source.mkv", Status: ripspec.AssetStatusCompleted}}},
			})
			sess.Logger = testEncoderLogger()
			h := New(&config.Config{Paths: config.PathsConfig{StagingDir: base}})
			err := h.Run(context.Background(), sess)
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("Run: %v, want error %q", err, tc.wantError)
			}
			fresh, err := sess.Store.GetByID(sess.Item.ID)
			if err != nil {
				t.Fatal(err)
			}
			env, err := ripspec.Parse(fresh.RipSpecData)
			if err != nil {
				t.Fatal(err)
			}
			asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, "main")
			if !ok || tc.wantError == "" && (!asset.IsCompleted() || asset.Path != "encoded.mkv") || tc.wantError != "" && !asset.IsFailed() {
				t.Fatalf("encoded asset: %+v", asset)
			}
		})
	}
}

func TestRunWorkerProcess(t *testing.T) {
	for _, tc := range []struct {
		mode, wantError string
		wantResult      bool
	}{
		{"success", "", true},
		{"failure", "encode worker: reel failed", false},
		{"exit", "worker crashed", false},
		{"empty", "produced no result", false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("SPINDLE_TEST_ENCODE_WORKER", tc.mode)
			sess := encoderSession(t, ripspec.Envelope{})
			sess.Task = &queue.Task{}
			rep := &spindleReporter{
				sess:   sess,
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)), now: time.Now,
			}
			result, err := runWorkerProcess(context.Background(), rep.logger, "input.mkv", t.TempDir(), rep)
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("result=%+v err=%v, want error %q", result, err, tc.wantError)
			}
			if (result != nil) != tc.wantResult {
				t.Fatalf("result=%+v, want result=%v", result, tc.wantResult)
			}
			if tc.wantResult {
				snap, err := encodingstate.Unmarshal(sess.Task.EncodingDetailsJSON)
				if err != nil || snap.TotalFrames != 42 {
					t.Fatalf("worker event not replayed: %+v %v", snap, err)
				}
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		t.Setenv("SPINDLE_TEST_ENCODE_WORKER", "empty")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := runWorkerProcess(ctx, slog.Default(), "input.mkv", t.TempDir(), &spindleReporter{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled worker: %v", err)
		}
	})
}
