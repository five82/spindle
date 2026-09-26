package encoder

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunWorkerReportsMissingInputAsFailure(t *testing.T) {
	var output bytes.Buffer
	err := RunWorker(context.Background(), "/no/such/input.mkv", t.TempDir(), &output)
	if err == nil {
		t.Fatal("missing input accepted")
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var event wireEvent
	if decodeErr := json.Unmarshal([]byte(lines[len(lines)-1]), &event); decodeErr != nil {
		t.Fatalf("invalid worker event: %q: %v", output.String(), decodeErr)
	}
	if event.Event != wireFailure {
		t.Fatalf("last event = %+v, want failure", event)
	}
	var message wireMessage
	if decodeErr := json.Unmarshal(event.Payload, &message); decodeErr != nil || message.Message == "" {
		t.Fatalf("failure message = %+v: %v", message, decodeErr)
	}
}
