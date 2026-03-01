package api

import (
	"context"
	"fmt"
	"strings"

	"spindle/internal/staging"
)

// ActiveFingerprintProvider surfaces active queue fingerprints for cleanup workflows.
type ActiveFingerprintProvider interface {
	ActiveFingerprints(ctx context.Context) (map[string]struct{}, error)
}

type CleanStagingRequest struct {
	StagingDir   string
	CleanAll     bool
	Fingerprints ActiveFingerprintProvider
}

type CleanStagingResult struct {
	Configured bool
	Scope      string
	Cleanup    staging.CleanStaleResult
}

// CleanStagingDirectories applies staging cleanup policy used by CLI commands.
func CleanStagingDirectories(ctx context.Context, req CleanStagingRequest) (CleanStagingResult, error) {
	stagingDir := strings.TrimSpace(req.StagingDir)
	if stagingDir == "" {
		return CleanStagingResult{Configured: false}, nil
	}

	if req.CleanAll {
		return CleanStagingResult{
			Configured: true,
			Scope:      "staging",
			Cleanup:    staging.CleanStale(ctx, stagingDir, 0, nil),
		}, nil
	}

	if req.Fingerprints == nil {
		return CleanStagingResult{}, fmt.Errorf("active fingerprint provider is required when clean_all is false")
	}
	fingerprints, err := req.Fingerprints.ActiveFingerprints(ctx)
	if err != nil {
		return CleanStagingResult{}, err
	}
	return CleanStagingResult{
		Configured: true,
		Scope:      "orphaned staging",
		Cleanup:    staging.CleanOrphaned(ctx, stagingDir, fingerprints, nil),
	}, nil
}
