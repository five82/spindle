package ffprobe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectCommandAndFailures(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ffprobe")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct{ name, body, want string }{
		{"success", `printf '{"streams":[{"codec_type":"audio","index":3}],"format":{"duration":"42"}}'`, ""},
		{"invalid JSON", `printf 'not json'`, "parse ffprobe output"},
		{"command failure", `exit 7`, "ffprobe movie.mkv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := "#!/bin/sh\n" + `test "$1" = -v && test "$2" = quiet && test "$7" = movie.mkv || exit 8` + "\n" + tc.body + "\n"
			if err := os.WriteFile(script, []byte(data), 0o755); err != nil {
				t.Fatal(err)
			}
			got, err := Inspect(context.Background(), "", "movie.mkv")
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Inspect error = %v, want %q", err, tc.want)
				}
				if got != nil {
					t.Fatalf("unexpected result: %+v", got)
				}
			} else if err != nil || got.AudioStreamCount() != 1 || got.DurationSeconds() != 42 {
				t.Fatalf("Inspect = %+v, %v", got, err)
			}
		})
	}
	if _, err := Inspect(context.Background(), filepath.Join(dir, "missing"), "movie.mkv"); err == nil {
		t.Fatal("missing binary succeeded")
	}
	var value FlexString
	if err := value.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("object accepted as FlexString")
	}
}
