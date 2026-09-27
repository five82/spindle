// Package validation provides post-encode validation checks.
package validation

import (
	"github.com/five82/reel/internal/media"
)

// HDRValidationResult contains the result of HDR validation.
type HDRValidationResult struct {
	IsValid   bool
	ActualHDR *bool
	Message   string
	ProbeUsed bool
}

// ValidateHDRStatusWithPath validates HDR status using native media probing.
func ValidateHDRStatusWithPath(outputPath string, expectedHDR *bool) HDRValidationResult {
	return validateHDRStatusWithAvailabilityCheck(outputPath, expectedHDR, media.IsAvailable())
}

// validateHDRStatusWithAvailabilityCheck is the internal validation function.
// This allows for easier testing of unavailable probing behavior.
func validateHDRStatusWithAvailabilityCheck(outputPath string, expectedHDR *bool, probeAvailable bool) HDRValidationResult {
	if !probeAvailable {
		return HDRValidationResult{
			IsValid:   true, // Pass validation when probing is unavailable.
			ActualHDR: nil,
			Message:   "Native media probe unavailable - HDR validation skipped",
			ProbeUsed: false,
		}
	}

	var actualHDR *bool
	hdrInfo, err := media.GetStreamHDRInfo(outputPath)
	if err == nil {
		actualHDR = &hdrInfo.IsHDR
	}

	return validateHDRResult(expectedHDR, actualHDR)
}

// validateHDRResult performs the common HDR validation logic.
func validateHDRResult(expectedHDR, actualHDR *bool) HDRValidationResult {
	result := HDRValidationResult{ProbeUsed: true}

	switch {
	case expectedHDR != nil && actualHDR != nil:
		// Both expected and actual are known
		if *expectedHDR == *actualHDR {
			status := "SDR"
			if *actualHDR {
				status = "HDR"
			}
			result.IsValid = true
			result.ActualHDR = actualHDR
			result.Message = status + " preserved"
		} else {
			expectedStr := "SDR"
			if *expectedHDR {
				expectedStr = "HDR"
			}
			actualStr := "SDR"
			if *actualHDR {
				actualStr = "HDR"
			}
			result.IsValid = false
			result.ActualHDR = actualHDR
			result.Message = "Expected " + expectedStr + ", found " + actualStr
		}

	case expectedHDR == nil && actualHDR != nil:
		// No expectation, but we detected the status
		status := "SDR"
		if *actualHDR {
			status = "HDR"
		}
		result.IsValid = true
		result.ActualHDR = actualHDR
		result.Message = "Output is " + status

	case expectedHDR != nil && actualHDR == nil:
		// Had an expectation but couldn't detect
		expectedStr := "SDR"
		if *expectedHDR {
			expectedStr = "HDR"
		}
		result.IsValid = false
		result.ActualHDR = nil
		result.Message = "Expected " + expectedStr + ", but could not detect HDR status"

	default:
		// Neither expected nor actual are available
		result.IsValid = false
		result.ActualHDR = nil
		result.Message = "Could not detect HDR status"
	}

	return result
}

// GetDetailedHDRInfo returns detailed HDR metadata from native media probing.
// This is useful for debugging and detailed reporting.
func GetDetailedHDRInfo(path string) (*media.HDRInfo, error) {
	if !media.IsAvailable() {
		return nil, nil
	}
	return media.GetHDRInfo(path)
}
