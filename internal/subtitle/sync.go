package subtitle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/five82/spindle/internal/transcription"
)

const (
	ffsubsyncCommand = "uvx"
	ffsubsyncPackage = "ffsubsync"
)

var runFFSubsync = func(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, ffsubsyncCommand, args...)
	// uvx grandchild processes must die with the group or they outlive
	// cancellation and block the pipe wait (see ConfigureGroupKill).
	transcription.ConfigureGroupKill(cmd)
	return cmd.CombinedOutput()
}

// syncSubtitleToReference retimes inputSRT against referenceSRT (the WhisperX
// transcript of the actual rip) with ffsubsync, which corrects both constant
// offsets and framerate drift, writing the result to outputSRT.
func syncSubtitleToReference(ctx context.Context, referenceSRT, inputSRT, outputSRT string) error {
	args := []string{ffsubsyncPackage, referenceSRT, "-i", inputSRT, "-o", outputSRT}
	output, err := runFFSubsync(ctx, args)
	if err != nil {
		return fmt.Errorf("ffsubsync: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if _, err := os.Stat(outputSRT); err != nil {
		return fmt.Errorf("ffsubsync produced no output: %w", err)
	}
	return nil
}
