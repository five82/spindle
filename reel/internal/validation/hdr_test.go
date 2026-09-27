package validation

import (
	"testing"
)

func TestValidateHDRResult(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name        string
		expectedHDR *bool
		actualHDR   *bool
		wantValid   bool
		wantMsg     string
	}{
		{
			name:        "HDR preserved correctly",
			expectedHDR: boolPtr(true),
			actualHDR:   boolPtr(true),
			wantValid:   true,
			wantMsg:     "HDR preserved",
		},
		{
			name:        "SDR preserved correctly",
			expectedHDR: boolPtr(false),
			actualHDR:   boolPtr(false),
			wantValid:   true,
			wantMsg:     "SDR preserved",
		},
		{
			name:        "HDR mismatch - expected HDR got SDR",
			expectedHDR: boolPtr(true),
			actualHDR:   boolPtr(false),
			wantValid:   false,
			wantMsg:     "Expected HDR, found SDR",
		},
		{
			name:        "HDR mismatch - expected SDR got HDR",
			expectedHDR: boolPtr(false),
			actualHDR:   boolPtr(true),
			wantValid:   false,
			wantMsg:     "Expected SDR, found HDR",
		},
		{
			name:        "No expectation but detected HDR",
			expectedHDR: nil,
			actualHDR:   boolPtr(true),
			wantValid:   true,
			wantMsg:     "Output is HDR",
		},
		{
			name:        "No expectation but detected SDR",
			expectedHDR: nil,
			actualHDR:   boolPtr(false),
			wantValid:   true,
			wantMsg:     "Output is SDR",
		},
		{
			name:        "Expected HDR but could not detect",
			expectedHDR: boolPtr(true),
			actualHDR:   nil,
			wantValid:   false,
			wantMsg:     "Expected HDR, but could not detect HDR status",
		},
		{
			name:        "Expected SDR but could not detect",
			expectedHDR: boolPtr(false),
			actualHDR:   nil,
			wantValid:   false,
			wantMsg:     "Expected SDR, but could not detect HDR status",
		},
		{
			name:        "Neither expected nor actual available",
			expectedHDR: nil,
			actualHDR:   nil,
			wantValid:   false,
			wantMsg:     "Could not detect HDR status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateHDRResult(tt.expectedHDR, tt.actualHDR)

			if result.IsValid != tt.wantValid {
				t.Errorf("IsValid = %v, want %v", result.IsValid, tt.wantValid)
			}

			if result.Message != tt.wantMsg {
				t.Errorf("Message = %v, want %v", result.Message, tt.wantMsg)
			}

			// Check actual HDR value
			if tt.actualHDR == nil {
				if result.ActualHDR != nil {
					t.Errorf("ActualHDR = %v, want nil", *result.ActualHDR)
				}
			} else {
				if result.ActualHDR == nil {
					t.Errorf("ActualHDR = nil, want %v", *tt.actualHDR)
				} else if *result.ActualHDR != *tt.actualHDR {
					t.Errorf("ActualHDR = %v, want %v", *result.ActualHDR, *tt.actualHDR)
				}
			}
		})
	}
}

func TestValidateHDRStatusWithAvailabilityCheck_ProbeNotAvailable(t *testing.T) {
	result := validateHDRStatusWithAvailabilityCheck("/nonexistent/file.mkv", nil, false)

	if !result.IsValid {
		t.Error("Should pass validation when media probing is unavailable")
	}

	if result.ActualHDR != nil {
		t.Error("ActualHDR should be nil when media probing is unavailable")
	}

	expectedMsg := "Native media probe unavailable - HDR validation skipped"
	if result.Message != expectedMsg {
		t.Errorf("Message = %v, want %v", result.Message, expectedMsg)
	}

	if result.ProbeUsed {
		t.Error("ProbeUsed should be false when media probing is unavailable")
	}
}
