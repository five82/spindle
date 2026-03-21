package stage

import (
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/services"
)

// ParseRipSpec parses a rip spec string into an Envelope.
// Returns services.ErrValidation on failure.
func ParseRipSpec(raw string) (ripspec.Envelope, error) {
	env, err := ripspec.Parse(raw)
	if err != nil {
		return ripspec.Envelope{}, &services.ErrValidation{
			Msg: "invalid rip spec: " + err.Error(),
		}
	}
	return env, nil
}
