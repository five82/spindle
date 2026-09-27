package encoder

import (
	"testing"
)

func TestChromaSamplePosition(t *testing.T) {
	tests := []struct {
		input int32
		want  string
	}{
		{1, "vertical"},
		{2, "colocated"},
		{0, "unknown"},
		{99, "unknown"},
	}
	for _, tt := range tests {
		if got := chromaSamplePosition(tt.input); got != tt.want {
			t.Errorf("chromaSamplePosition(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSvtParamsDisplay(t *testing.T) {
	got := SvtParamsDisplay(0.5, true, 0)
	wantParts := []string{"ac-bias=0.5", "enable-variance-boost=1", "tune=0", "keyint=10s", "scd=0", "scm=0"}
	for _, part := range wantParts {
		if !contains(got, part) {
			t.Errorf("SvtParamsDisplay() = %q, missing %q", got, part)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
