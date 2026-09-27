package processing

import (
	"testing"

	"github.com/five82/reel/internal/config"
)

func TestLogSuggestionIncludesLogFile(t *testing.T) {
	cfg := &config.Config{LogFile: "/tmp/reel.log"}

	got := logSuggestion(cfg)
	want := "Check the log for more details: /tmp/reel.log"
	if got != want {
		t.Fatalf("logSuggestion() = %q, want %q", got, want)
	}
}

func TestLogSuggestionEmptyWithoutLogFile(t *testing.T) {
	if got := logSuggestion(&config.Config{}); got != "" {
		t.Fatalf("logSuggestion() = %q, want empty string", got)
	}
	if got := logSuggestion(nil); got != "" {
		t.Fatalf("logSuggestion(nil) = %q, want empty string", got)
	}
}
