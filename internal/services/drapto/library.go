package drapto

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	draptolib "github.com/five82/drapto"
)

// Library implements Client using the Drapto Go library directly,
// bypassing the CLI shell-out.
type Library struct{}

// NewLibrary constructs a Library client.
func NewLibrary() *Library {
	return &Library{}
}

// Encode encodes a video file using the Drapto library.
func (l *Library) Encode(ctx context.Context, inputPath, outputDir string, opts EncodeOptions) (string, error) {
	if inputPath == "" {
		return "", errors.New("input path required")
	}
	if strings.TrimSpace(outputDir) == "" {
		return "", errors.New("output directory required")
	}

	// Build encoder options
	encoderOpts := []draptolib.Option{
		draptolib.WithResponsive(),
	}

	// Create encoder
	encoder, err := draptolib.New(encoderOpts...)
	if err != nil {
		return "", err
	}

	// Create reporter adapter if progress callback provided
	var rep draptolib.Reporter
	if opts.Progress != nil {
		rep = newSpindleReporter(opts.Progress)
	}

	// Run encode
	_, err = encoder.EncodeWithReporter(ctx, inputPath, outputDir, rep)
	if err != nil {
		return "", err
	}

	// Return output path (same logic as CLI client)
	base := filepath.Base(inputPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = base
	}
	return filepath.Join(strings.TrimSpace(outputDir), stem+".mkv"), nil
}

var _ Client = (*Library)(nil)
