package organizer

import (
	"fmt"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/services"
)

// ValidateEditionFilename verifies that the edition suffix appears in the final filename
// when edition metadata is present. This catches logic bugs in the metadata->filename path.
func ValidateEditionFilename(finalPath string, edition string, logger *slog.Logger) error {
	edition = strings.TrimSpace(edition)
	if edition == "" {
		return nil // No edition to validate
	}

	finalPath = strings.TrimSpace(finalPath)
	if finalPath == "" {
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate edition",
			"Final path is required for edition validation",
			nil,
		)
	}

	// Extract base filename without extension
	base := filepath.Base(finalPath)
	nameOnly := strings.TrimSuffix(base, filepath.Ext(base))

	// Check for edition suffix in the expected format " - {Edition}"
	expectedSuffix := " - " + edition

	if !strings.HasSuffix(nameOnly, expectedSuffix) {
		if logger != nil {
			logger.Error("edition filename validation failed",
				logging.String("final_path", finalPath),
				logging.String("expected_edition", edition),
				logging.String("filename", nameOnly),
				logging.String(logging.FieldEventType, "edition_validation_failed"),
				logging.String(logging.FieldErrorHint, "edition detected but not present in filename"),
			)
		}
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"edition validation",
			fmt.Sprintf("Edition %q not found in filename %q (expected suffix %q)", edition, nameOnly, expectedSuffix),
			nil,
		)
	}

	if logger != nil {
		logger.Info("edition filename validation passed",
			logging.String(logging.FieldEventType, "edition_validation_passed"),
			logging.String("edition", edition),
			logging.String("filename", nameOnly),
		)
	}

	return nil
}
