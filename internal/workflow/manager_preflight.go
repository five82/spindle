package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/preflight"
)

// runPreflightChecks validates external service readiness before processing an item.
// Returns nil when all checks pass, or an error describing all failures.
func (m *Manager) runPreflightChecks(ctx context.Context, logger *slog.Logger) error {
	results := preflight.RunFeatureChecks(ctx, m.cfg)
	if len(results) == 0 {
		return nil
	}

	var failures []string
	for _, r := range results {
		if r.Passed {
			logger.Info("preflight check passed",
				logging.String("check", r.Name),
				logging.String("detail", r.Detail),
				logging.String(logging.FieldEventType, "preflight_passed"),
			)
		} else {
			logger.Error("preflight check failed",
				logging.String("check", r.Name),
				logging.String("detail", r.Detail),
				logging.String(logging.FieldEventType, "preflight_failed"),
				logging.String(logging.FieldErrorHint, "fix the reported issue and restart the daemon"),
			)
			failures = append(failures, fmt.Sprintf("%s: %s", r.Name, r.Detail))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("preflight checks failed: %s", strings.Join(failures, "; "))
	}
	return nil
}
