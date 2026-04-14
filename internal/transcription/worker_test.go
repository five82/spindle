package transcription

import (
	"bufio"
	"bytes"
	"context"
	"testing"
)

func TestReadWorkerResponse(t *testing.T) {
	reader := bufio.NewReader(bytes.NewBufferString(`{"id":"req-1","ok":true,"language":"English","text":"Hello","time_stamps":[{"text":"Hello","start_time":0.1,"end_time":0.4}]}` + "\n"))
	resp, err := readWorkerResponse(context.Background(), reader, "req-1")
	if err != nil {
		t.Fatalf("readWorkerResponse() error = %v", err)
	}
	if resp.Text != "Hello" {
		t.Fatalf("Text = %q", resp.Text)
	}
	if len(resp.TimeStamps) != 1 {
		t.Fatalf("expected 1 timestamp, got %d", len(resp.TimeStamps))
	}
}

func TestReadWorkerResponseError(t *testing.T) {
	reader := bufio.NewReader(bytes.NewBufferString(`{"id":"req-2","ok":false,"error":"boom"}` + "\n"))
	if _, err := readWorkerResponse(context.Background(), reader, "req-2"); err == nil {
		t.Fatal("expected worker error")
	}
}
