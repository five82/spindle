package stage

import (
	"fmt"

	"github.com/five82/spindle/internal/ripspec"
)

// ParseRipSpec parses a rip spec string into an Envelope.
func ParseRipSpec(raw string) (ripspec.Envelope, error) {
	env, err := ripspec.Parse(raw)
	if err != nil {
		return ripspec.Envelope{}, fmt.Errorf("invalid rip spec: %w", err)
	}
	return env, nil
}
