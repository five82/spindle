package disc

import (
	"context"
	"fmt"
	"testing"
)

func TestDriveStatusString(t *testing.T) {
	tests := []struct {
		status DriveStatus
		want   string
	}{
		{DriveStatusNoInfo, "no_info"},
		{DriveStatusNoDisc, "no_disc"},
		{DriveStatusTrayOpen, "tray_open"},
		{DriveStatusNotReady, "not_ready"},
		{DriveStatusDiscOK, "disc_ok"},
		{DriveStatus(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.status.String()
			if got != tt.want {
				t.Errorf("DriveStatus(%d).String() = %q, want %q", int(tt.status), got, tt.want)
			}
		})
	}
}

func TestCheckDriveStatusEmptyPath(t *testing.T) {
	_, err := CheckDriveStatus("")
	if err == nil {
		t.Fatal("expected error for empty device path")
	}
}

func TestCheckDriveStatusInvalidPath(t *testing.T) {
	_, err := CheckDriveStatus("/dev/nonexistent_device_12345")
	if err == nil {
		t.Fatal("expected error for nonexistent device")
	}
}

func TestWaitForReadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := WaitForReady(ctx, "/dev/nonexistent_device_12345")
	if err == nil {
		t.Fatal("expected error for cancelled context or invalid device")
	}
}

func TestExtractDevicePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/dev/sr0", "/dev/sr0"},
		{"dev:/dev/sr0", "/dev/sr0"},
		{"disc:0", ""},
		{"disc:1", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("input=%q", tt.input), func(t *testing.T) {
			got := ExtractDevicePath(tt.input)
			if got != tt.want {
				t.Errorf("ExtractDevicePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
