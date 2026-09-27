package encoder

import (
	"bytes"
	"context"
	"testing"
)

func TestRunConsoleReportsMissingInput(t *testing.T) {
	var out bytes.Buffer
	if _, err := RunConsole(context.Background(), "/nonexistent-spindle-input.mkv", t.TempDir(), &out, true); err == nil {
		t.Fatal("missing input encoded")
	}
}
