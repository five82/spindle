package api

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	draptolib "github.com/five82/drapto"

	"spindle/internal/audioanalysis"
	"spindle/internal/config"
	"spindle/internal/ripcache"
)

type ResolveCacheTargetRequest struct {
	Config *config.Config
	Arg    string
}

// ResolveCacheTarget resolves a cache entry number or direct path into a target file path.
// Returns the resolved target, a user-facing label, and an optional warning.
func ResolveCacheTarget(ctx context.Context, req ResolveCacheTargetRequest) (string, string, string, error) {
	arg := strings.TrimSpace(req.Arg)
	if arg == "" {
		return "", "", "", fmt.Errorf("cache entry or path is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	entryNum, err := strconv.Atoi(arg)
	if err != nil {
		target, label, err := ripcache.ResolveTargetArg(ctx, nil, arg)
		return target, label, "", err
	}
	if entryNum < 1 {
		return "", "", "", fmt.Errorf("invalid cache entry number: %d", entryNum)
	}

	manager, warning, err := OpenRipCacheManagerForCLI(OpenCacheResourceRequest{
		Config: req.Config,
	})
	if err != nil {
		return "", "", warning, err
	}
	if manager == nil {
		return "", "", warning, fmt.Errorf("rip cache is unavailable")
	}

	target, label, err := ripcache.ResolveTargetArg(ctx, manager, arg)
	return target, label, warning, err
}

type RunCommentaryDiagnosticRequest struct {
	Config *config.Config
	Target string
	Output io.Writer
}

func RunCommentaryDiagnostic(ctx context.Context, req RunCommentaryDiagnosticRequest) (*audioanalysis.DiagnosticResult, error) {
	cfg := req.Config
	if cfg == nil {
		return nil, fmt.Errorf("configuration is required")
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return nil, fmt.Errorf("target is required")
	}
	return audioanalysis.RunDiagnostic(ctx, cfg, target, req.Output)
}

type RunCropDiagnosticRequest struct {
	Target string
}

func RunCropDiagnostic(ctx context.Context, req RunCropDiagnosticRequest) (*draptolib.CropDetectionResult, error) {
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return nil, fmt.Errorf("target is required")
	}
	return draptolib.DetectCrop(ctx, target)
}
