package util

import (
	"runtime"
	"testing"
)

func TestLogicalCores(t *testing.T) {
	cores := LogicalCores()
	if cores <= 0 {
		t.Errorf("LogicalCores() = %d, want > 0", cores)
	}
	// Should match runtime.NumCPU()
	if cores != runtime.NumCPU() {
		t.Errorf("LogicalCores() = %d, want %d (runtime.NumCPU())", cores, runtime.NumCPU())
	}
}

func TestPhysicalCores(t *testing.T) {
	physical := PhysicalCores()
	logical := LogicalCores()

	if physical <= 0 {
		t.Errorf("PhysicalCores() = %d, want > 0", physical)
	}

	// Physical cores should never exceed logical cores
	if physical > logical {
		t.Errorf("PhysicalCores() = %d > LogicalCores() = %d, physical should not exceed logical", physical, logical)
	}
}

func TestPhysicalCoresLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-specific test")
	}

	cores := physicalCoresLinux()
	// On Linux, this should return a positive value (or 0 if sysfs unavailable)
	if cores < 0 {
		t.Errorf("physicalCoresLinux() = %d, want >= 0", cores)
	}

	// If detection succeeded, verify it's reasonable
	if cores > 0 {
		logical := LogicalCores()
		if cores > logical {
			t.Errorf("physicalCoresLinux() = %d > LogicalCores() = %d", cores, logical)
		}
	}
}

func TestPhysicalCoresDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific test")
	}

	cores := physicalCoresDarwin()
	// On macOS, this should return a positive value (or 0 if sysctl fails)
	if cores < 0 {
		t.Errorf("physicalCoresDarwin() = %d, want >= 0", cores)
	}

	// If detection succeeded, verify it's reasonable
	if cores > 0 {
		logical := LogicalCores()
		if cores > logical {
			t.Errorf("physicalCoresDarwin() = %d > LogicalCores() = %d", cores, logical)
		}
	}
}

func TestSMTDetection(t *testing.T) {
	physical := PhysicalCores()
	logical := LogicalCores()
	hasSMT := logical > physical

	// Log the detection results for debugging
	t.Logf("CPU topology: physical=%d, logical=%d, hasSMT=%v", physical, logical, hasSMT)

	// The ratio should be reasonable: 1:1 (no SMT) or 1:2 (SMT)
	if physical > 0 {
		ratio := float64(logical) / float64(physical)
		if ratio < 1.0 || ratio > 4.0 {
			t.Errorf("Unusual logical/physical ratio: %f (physical=%d, logical=%d)", ratio, physical, logical)
		}
	}
}
