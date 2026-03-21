//go:build linux

package discmonitor

import (
	"encoding/json"
	"fmt"
)

type lsblkOutput struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

type lsblkDevice struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	FSType     string `json:"fstype"`
	MountPoint string `json:"mountpoint"`
}

// parseLsblk parses lsblk JSON output and returns the first block device.
func parseLsblk(data []byte) (*lsblkDevice, error) {
	var out lsblkOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal lsblk JSON: %w", err)
	}
	if len(out.BlockDevices) == 0 {
		return nil, fmt.Errorf("no block devices in lsblk output")
	}
	return &out.BlockDevices[0], nil
}

// classifyDisc maps a filesystem type to a disc type string.
func classifyDisc(fstype string) string {
	switch fstype {
	case "udf":
		return "Blu-ray"
	case "iso9660":
		return "DVD"
	default:
		return "Unknown"
	}
}
