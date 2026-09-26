package reel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncoderEntryPointsHandleMissingInputs(t *testing.T) {
	encoder, err := New(WithCRF(30))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.mkv")
	output := filepath.Join(dir, "output")
	if _, err := encoder.EncodeWithReporter(context.Background(), missing, output, nil); err == nil || !strings.Contains(err.Error(), "no files were encoded") {
		t.Fatalf("reporter encode: %v", err)
	}
	if _, err := encoder.Encode(context.Background(), missing, output, func(Event) error { return nil }); err == nil || !strings.Contains(err.Error(), "no files were encoded") {
		t.Fatalf("event encode: %v", err)
	}
	batch, err := encoder.EncodeBatch(context.Background(), []string{missing, missing}, output, nil)
	if err != nil || batch.TotalFiles != 2 || batch.SuccessfulCount != 0 || len(batch.Results) != 0 {
		t.Fatalf("missing batch: %+v, %v", batch, err)
	}
	empty, err := encoder.EncodeBatch(context.Background(), nil, output, func(Event) error { return nil })
	if err != nil || empty.TotalFiles != 0 {
		t.Fatalf("empty batch: %+v, %v", empty, err)
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Encode(context.Background(), missing, blocked, nil); err == nil || !strings.Contains(err.Error(), "output directory") {
		t.Fatalf("blocked output: %v", err)
	}
	if _, err := encoder.EncodeBatch(context.Background(), nil, blocked, nil); err == nil {
		t.Fatal("blocked batch output")
	}
	if _, err := encoder.EncodeWithReporter(context.Background(), missing, blocked, nil); err == nil {
		t.Fatal("blocked reporter output")
	}
}
