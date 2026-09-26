package util

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// SystemInfo contains information about the host system.
type SystemInfo struct {
	Hostname string
	NumCPU   int
	OS       string
	Arch     string
}

// GetSystemInfo collects system information.
func GetSystemInfo() SystemInfo {
	hostname, _ := os.Hostname()
	return SystemInfo{
		Hostname: hostname,
		NumCPU:   runtime.NumCPU(),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}
}

// MemoryStats contains selected values from /proc/meminfo.
type MemoryStats struct {
	MemTotal     uint64
	MemAvailable uint64
	SwapTotal    uint64
	SwapFree     uint64
}

// SwapUsed returns currently used swap in bytes.
func (m MemoryStats) SwapUsed() uint64 {
	if m.SwapTotal <= m.SwapFree {
		return 0
	}
	return m.SwapTotal - m.SwapFree
}

// ReadMemoryStats reads memory and swap information from /proc/meminfo.
func ReadMemoryStats() (MemoryStats, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemoryStats{}, false
	}
	defer func() { _ = f.Close() }()

	var stats MemoryStats
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		bytes := kb * 1024
		switch fields[0] {
		case "MemTotal:":
			stats.MemTotal = bytes
		case "MemAvailable:":
			stats.MemAvailable = bytes
		case "SwapTotal:":
			stats.SwapTotal = bytes
		case "SwapFree:":
			stats.SwapFree = bytes
		}
	}

	return stats, stats.MemAvailable > 0
}

// AvailableMemoryBytes returns the available memory in bytes.
// On Linux, this reads MemAvailable from /proc/meminfo.
// Returns 0 if memory cannot be determined.
func AvailableMemoryBytes() uint64 {
	stats, ok := ReadMemoryStats()
	if !ok {
		return 0
	}
	return stats.MemAvailable
}

// LogicalCores returns the number of logical CPU cores (includes hyperthreads).
// This is equivalent to runtime.NumCPU().
func LogicalCores() int {
	return runtime.NumCPU()
}

// PhysicalCores returns the number of physical CPU cores.
// On systems with SMT/hyperthreading, this will be less than LogicalCores().
// Falls back to LogicalCores()/2 if detection fails.
func PhysicalCores() int {
	switch runtime.GOOS {
	case "linux":
		if cores := physicalCoresLinux(); cores > 0 {
			return cores
		}
	case "darwin":
		if cores := physicalCoresDarwin(); cores > 0 {
			return cores
		}
	}
	// Fallback: assume hyperthreading (2 threads per core)
	logical := LogicalCores()
	if logical > 1 {
		return logical / 2
	}
	return 1
}

// physicalCoresLinux reads physical core count from sysfs topology.
// Returns 0 if detection fails.
func physicalCoresLinux() int {
	return physicalCoresLinuxAt("/sys/devices/system/cpu")
}

func physicalCoresLinuxAt(cpuDir string) int {
	// Count unique physical core IDs across all CPUs
	entries, err := os.ReadDir(cpuDir)
	if err != nil {
		return 0
	}

	coreIDs := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		// Match cpu0, cpu1, cpu2, etc.
		if !strings.HasPrefix(name, "cpu") {
			continue
		}
		suffix := name[3:]
		if len(suffix) == 0 {
			continue
		}
		// Check if suffix is a number
		if _, err := strconv.Atoi(suffix); err != nil {
			continue
		}

		// Read core_id for this CPU
		coreIDPath := filepath.Join(cpuDir, name, "topology", "core_id")
		data, err := os.ReadFile(coreIDPath)
		if err != nil {
			continue
		}

		// Also read physical_package_id to handle multi-socket systems
		pkgIDPath := filepath.Join(cpuDir, name, "topology", "physical_package_id")
		pkgData, err := os.ReadFile(pkgIDPath)
		if err != nil {
			// Single socket system, just use core_id
			coreIDs[strings.TrimSpace(string(data))] = struct{}{}
		} else {
			// Multi-socket: combine package and core ID
			key := strings.TrimSpace(string(pkgData)) + ":" + strings.TrimSpace(string(data))
			coreIDs[key] = struct{}{}
		}
	}

	if len(coreIDs) > 0 {
		return len(coreIDs)
	}
	return 0
}

// physicalCoresDarwin uses sysctl to get physical core count on macOS.
// Returns 0 if detection fails.
func physicalCoresDarwin() int {
	out, err := exec.Command("sysctl", "-n", "hw.physicalcpu").Output()
	if err != nil {
		return 0
	}
	cores, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || cores <= 0 {
		return 0
	}
	return cores
}
