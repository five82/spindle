package encoder

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDispatchWireEventBadPayload(t *testing.T) {
	for _, event := range []string{
		wireInitialization, wireStageProgress, wireCropResult, wireEncodingConfig,
		wireEncodingStarted, wireEncodingProgress, wireValidationComplete,
		wireEncodingComplete, wireWarning, wireVerbose, wireError, wireResult, wireFailure,
	} {
		t.Run(event, func(t *testing.T) {
			_, _, err := dispatchWireEvent(wireEvent{Event: event, Payload: json.RawMessage("{")}, &spindleReporter{})
			if err == nil {
				t.Fatal("invalid payload accepted")
			}
		})
	}
	if res, failure, err := dispatchWireEvent(wireEvent{Event: "future_event"}, &spindleReporter{}); res != nil || failure != "" || err != nil {
		t.Fatalf("unknown event: result=%+v failure=%q err=%v", res, failure, err)
	}
}

func TestWireWriterConcurrentEmission(t *testing.T) {
	var output bytes.Buffer
	w := &wireWriter{enc: json.NewEncoder(&output)}
	const count = 100
	done := make(chan struct{}, count)
	for range count {
		go func() {
			w.emit(wireWarning, wireMessage{Message: "warning"})
			done <- struct{}{}
		}()
	}
	for range count {
		<-done
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != count {
		t.Fatalf("got %d events, want %d", len(lines), count)
	}
	for _, line := range lines {
		var ev wireEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Event != wireWarning {
			t.Fatalf("corrupted event %q: %v", line, err)
		}
	}
}
