//go:build linux

package discmonitor

import (
	"testing"
)

func TestParseLsblk(t *testing.T) {
	input := []byte(`{
		"blockdevices": [
			{"name": "sr0", "label": "MY_DISC", "fstype": "udf", "mountpoint": "/media/cdrom"}
		]
	}`)

	dev, err := parseLsblk(input)
	if err != nil {
		t.Fatalf("parseLsblk returned error: %v", err)
	}
	if dev.Name != "sr0" {
		t.Errorf("Name = %q, want %q", dev.Name, "sr0")
	}
	if dev.Label != "MY_DISC" {
		t.Errorf("Label = %q, want %q", dev.Label, "MY_DISC")
	}
	if dev.FSType != "udf" {
		t.Errorf("FSType = %q, want %q", dev.FSType, "udf")
	}
	if dev.MountPoint != "/media/cdrom" {
		t.Errorf("MountPoint = %q, want %q", dev.MountPoint, "/media/cdrom")
	}
}

func TestParseLsblkEmpty(t *testing.T) {
	input := []byte(`{"blockdevices": []}`)
	_, err := parseLsblk(input)
	if err == nil {
		t.Fatal("parseLsblk should return error for empty blockdevices")
	}
}

func TestParseLsblkInvalidJSON(t *testing.T) {
	_, err := parseLsblk([]byte(`not json`))
	if err == nil {
		t.Fatal("parseLsblk should return error for invalid JSON")
	}
}

func TestParseLsblkNullFields(t *testing.T) {
	input := []byte(`{
		"blockdevices": [
			{"name": "sr0", "label": null, "fstype": null, "mountpoint": null}
		]
	}`)

	dev, err := parseLsblk(input)
	if err != nil {
		t.Fatalf("parseLsblk returned error: %v", err)
	}
	if dev.Label != "" {
		t.Errorf("Label = %q, want empty", dev.Label)
	}
	if dev.FSType != "" {
		t.Errorf("FSType = %q, want empty", dev.FSType)
	}
}

func TestClassifyDisc(t *testing.T) {
	tests := []struct {
		fstype string
		want   string
	}{
		{"udf", "Blu-ray"},
		{"iso9660", "DVD"},
		{"", "Unknown"},
		{"ext4", "Unknown"},
	}
	for _, tt := range tests {
		got := classifyDisc(tt.fstype)
		if got != tt.want {
			t.Errorf("classifyDisc(%q) = %q, want %q", tt.fstype, got, tt.want)
		}
	}
}

func TestValidateLabel(t *testing.T) {
	tests := []struct {
		label string
		want  bool
	}{
		{"MY_DISC", true},
		{"disc 1", true},
		{"a", true},
		{"", false},
		{"   ", false},
		{"\t\n", false},
		{"\x00\x01\x02", false},
		{" hello ", true},
	}
	for _, tt := range tests {
		got := ValidateLabel(tt.label)
		if got != tt.want {
			t.Errorf("ValidateLabel(%q) = %v, want %v", tt.label, got, tt.want)
		}
	}
}

func TestIsUnusableLabel(t *testing.T) {
	tests := []struct {
		label string
		want  bool
	}{
		// Unusable: empty/whitespace
		{"", true},
		{"   ", true},
		{"\t", true},
		// Unusable: generic exact matches
		{"LOGICAL_VOLUME_ID", true},
		{"logical_volume_id", true},
		{"VOLUME_ID", true},
		{"DVD_VIDEO", true},
		{"BLURAY", true},
		{"BD_ROM", true},
		{"UNTITLED", true},
		{"UNKNOWN DISC", true},
		{"Unknown Disc", true},
		// Unusable: generic prefixes
		{"VOLUME_1", true},
		{"DISK_01", true},
		{"TRACK_03", true},
		// Unusable: all digits
		{"12345", true},
		{"0", true},
		// Usable
		{"MY_MOVIE", false},
		{"The Matrix", false},
		{"DISC1", false},
		{"a", false},
	}
	for _, tt := range tests {
		got := IsUnusableLabel(tt.label)
		if got != tt.want {
			t.Errorf("IsUnusableLabel(%q) = %v, want %v", tt.label, got, tt.want)
		}
	}
}

func TestExtractDiscNameFromVolumeID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MOVIE_TITLE", "MOVIE TITLE"},
		{"01_MOVIE_TITLE", "MOVIE TITLE"},
		{"SHOW_S01_DISC_01", "SHOW"},
		{"SHOW_TV", "SHOW"},
		{"01_MY_SHOW_TV", "MY SHOW"},
		{"JUST_UNDERSCORES", "JUST UNDERSCORES"},
		{"", ""},
		{"NOCHANGE", "NOCHANGE"},
		{"99_TITLE_S02_DISC_03", "TITLE"},
	}
	for _, tt := range tests {
		got := ExtractDiscNameFromVolumeID(tt.input)
		if got != tt.want {
			t.Errorf("ExtractDiscNameFromVolumeID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestShouldRefreshDiscTitle(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"  ", true},
		{"Unknown Disc", true},
		{"unknown disc", true},
		{"UNKNOWN DISC", true},
		{"My Movie", false},
		{"a", false},
	}
	for _, tt := range tests {
		got := shouldRefreshDiscTitle(tt.title)
		if got != tt.want {
			t.Errorf("shouldRefreshDiscTitle(%q) = %v, want %v", tt.title, got, tt.want)
		}
	}
}

func TestNewDefaults(t *testing.T) {
	m := New("", nil, nil)
	if m.device != "/dev/sr0" {
		t.Errorf("default device = %q, want %q", m.device, "/dev/sr0")
	}
	if m.logger == nil {
		t.Error("logger should not be nil")
	}
}

func TestNewCustomDevice(t *testing.T) {
	m := New("/dev/sr1", nil, nil)
	if m.device != "/dev/sr1" {
		t.Errorf("device = %q, want %q", m.device, "/dev/sr1")
	}
}

func TestPauseResume(t *testing.T) {
	m := New("", nil, nil)
	if m.IsPaused() {
		t.Error("new monitor should not be paused")
	}
	if !m.PauseDisc() {
		t.Error("PauseDisc should return true when not already paused")
	}
	if !m.IsPaused() {
		t.Error("monitor should be paused after PauseDisc()")
	}
	if m.PauseDisc() {
		t.Error("PauseDisc should return false when already paused")
	}
	if !m.ResumeDisc() {
		t.Error("ResumeDisc should return true when paused")
	}
	if m.IsPaused() {
		t.Error("monitor should not be paused after ResumeDisc()")
	}
	if m.ResumeDisc() {
		t.Error("ResumeDisc should return false when not paused")
	}
}
