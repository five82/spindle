package disc

import (
	"context"
	"fmt"
	"os/exec"
)

// Ejector defines disc eject operations.
type Ejector interface {
	Eject(ctx context.Context, device string) error
}

type commandEjector struct{}

// NewEjector creates an ejector that shells out to the eject utility.
func NewEjector() Ejector {
	return commandEjector{}
}

func (commandEjector) Eject(ctx context.Context, device string) error {
	var cmd *exec.Cmd
	if device == "" {
		cmd = exec.CommandContext(ctx, "eject")
	} else {
		cmd = exec.CommandContext(ctx, "eject", device)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("eject %s: %w", device, err)
	}
	return nil
}
