package util

import (
	"runtime"
	"testing"
)

func TestHostSystemAndMemoryReporting(t *testing.T) {
	info := GetSystemInfo()
	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH || info.NumCPU != runtime.NumCPU() || info.Hostname == "" {
		t.Fatalf("system info: %+v", info)
	}
	if LogicalCores() != runtime.NumCPU() || PhysicalCores() < 1 {
		t.Fatal("invalid core count")
	}
	if runtime.GOOS == "linux" {
		stats, ok := ReadMemoryStats()
		if !ok || stats.MemTotal == 0 || stats.MemAvailable == 0 || AvailableMemoryBytes() == 0 {
			t.Fatalf("memory: %+v, ok=%v", stats, ok)
		}
		if stats.SwapUsed() > stats.SwapTotal {
			t.Fatalf("swap: %+v", stats)
		}
	}
	if (MemoryStats{SwapTotal: 10, SwapFree: 20}).SwapUsed() != 0 || (MemoryStats{SwapTotal: 20, SwapFree: 10}).SwapUsed() != 10 {
		t.Fatal("swap usage must be clamped at zero")
	}
}
