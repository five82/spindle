package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPhysicalCoresLinuxTopologyCountsPackages(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		cpu, core, pkg string
	}{
		{"cpu0", "0\n", "0\n"},
		{"cpu1", "0\n", "0\n"}, // SMT sibling of cpu0.
		{"cpu2", "0\n", "1\n"}, // Same core ID, different socket.
		{"cpu3", "1\n", ""},    // Missing package ID uses the core ID.
		{"cpu4", "", "0\n"},    // Missing topology is ignored.
	} {
		dir := filepath.Join(root, tc.cpu, "topology")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if tc.core != "" {
			if err := os.WriteFile(filepath.Join(dir, "core_id"), []byte(tc.core), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if tc.pkg != "" {
			if err := os.WriteFile(filepath.Join(dir, "physical_package_id"), []byte(tc.pkg), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Mkdir(filepath.Join(root, "cpufreq"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := physicalCoresLinuxAt(root); got != 3 {
		t.Fatalf("physical cores = %d, want 3", got)
	}
	if got := physicalCoresLinuxAt(t.TempDir()); got != 0 {
		t.Fatalf("empty topology = %d, want 0", got)
	}
	if got := physicalCoresLinuxAt(filepath.Join(root, "missing")); got != 0 {
		t.Fatalf("unreadable topology = %d, want 0", got)
	}
}
