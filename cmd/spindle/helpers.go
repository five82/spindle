package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"spindle/internal/ripcache"
)

// resolveCacheTarget resolves a cache entry number or path to a video file target.
// Returns (targetPath, label, error).
func resolveCacheTarget(ctx *commandContext, arg string, out io.Writer) (string, string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", errors.New("cache entry or path is required")
	}

	if entryNum, err := strconv.Atoi(arg); err == nil {
		if entryNum < 1 {
			return "", "", fmt.Errorf("invalid cache entry number: %d", entryNum)
		}
		manager, warn, err := cacheManager(ctx)
		if warn != "" {
			fmt.Fprintln(out, warn)
		}
		if err != nil || manager == nil {
			if err != nil {
				return "", "", err
			}
			return "", "", errors.New("rip cache is unavailable")
		}
		return ripcache.ResolveTargetArg(context.Background(), manager, arg)
	}

	return ripcache.ResolveTargetArg(context.Background(), nil, arg)
}
