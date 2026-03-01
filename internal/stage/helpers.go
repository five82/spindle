package stage

import (
	"spindle/internal/ripspec"
	"spindle/internal/services"
)

// ParseRipSpec parses a rip spec string and returns the envelope.
// On failure it returns a services.ErrValidation suitable for stage Execute methods.
func ParseRipSpec(raw string) (ripspec.Envelope, error) {
	env, err := ripspec.Parse(raw)
	if err != nil {
		return ripspec.Envelope{}, services.Wrap(
			services.ErrValidation, "stage", "parse rip spec",
			"Rip specification missing or invalid; rerun identification", err)
	}
	return env, nil
}
