package disc_test

import (
	"context"
	"errors"
	"testing"

	"spindle/internal/disc"
)

type stubExec struct {
	output []byte
	err    error
}

func (s stubExec) Run(ctx context.Context, binary string, args []string) ([]byte, error) {
	return s.output, s.err
}

func TestScannerParsesFingerprint(t *testing.T) {
	payload := `{"fingerprint":"abc123","titles":[{"id":1,"name":"Main Feature","length":7200}]}`
	scanner := disc.NewScannerWithExecutor("makemkvcon", stubExec{output: []byte(payload)})
	result, err := scanner.Scan(context.Background(), "disc:0")
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if result.Fingerprint != "abc123" {
		t.Fatalf("unexpected fingerprint: %s", result.Fingerprint)
	}
	if len(result.Titles) != 1 || result.Titles[0].ID != 1 {
		t.Fatalf("unexpected titles: %#v", result.Titles)
	}
}

func TestScannerRequiresFingerprint(t *testing.T) {
	scanner := disc.NewScannerWithExecutor("makemkvcon", stubExec{output: []byte(`{"fingerprint":""}`)})
	if _, err := scanner.Scan(context.Background(), "disc:0"); !errors.Is(err, disc.ErrFingerprintMissing) {
		t.Fatalf("expected fingerprint error, got %v", err)
	}
}

func TestScannerNeedsBinary(t *testing.T) {
	scanner := disc.NewScannerWithExecutor("", stubExec{})
	if _, err := scanner.Scan(context.Background(), "disc:0"); err == nil {
		t.Fatal("expected error for missing binary")
	}
}
