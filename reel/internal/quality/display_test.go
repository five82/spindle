package quality

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/five82/reel/internal/video"
)

func TestEnsureDisplayModelWritesReelDisplayModelWithVshipCompatibleSDRColorspace(t *testing.T) {
	path, err := EnsureDisplayModel(t.TempDir(), &video.Info{}, "")
	if err != nil {
		t.Fatalf("EnsureDisplayModel() error = %v", err)
	}

	model := readGeneratedDisplayModel(t, path)
	if model.Colorspace != "SDR" {
		t.Fatalf("generated SDR colorspace = %q, want %q", model.Colorspace, "SDR")
	}
}

func TestEnsureDisplayModelWritesVshipCompatibleHDRColorspaces(t *testing.T) {
	tests := []struct {
		name     string
		transfer int32
		want     string
	}{
		{name: "pq", transfer: 16, want: "BT.2020-PQ"},
		{name: "hlg", transfer: 18, want: "BT.2020-HLG"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := EnsureDisplayModel(t.TempDir(), &video.Info{TransferCharacteristics: &tt.transfer}, "")
			if err != nil {
				t.Fatalf("EnsureDisplayModel() error = %v", err)
			}

			model := readGeneratedDisplayModel(t, path)
			if model.Colorspace != tt.want {
				t.Fatalf("generated HDR colorspace = %q, want %q", model.Colorspace, tt.want)
			}
		})
	}
}

func TestEnsureDisplayModelAcceptsReelOverride(t *testing.T) {
	path := writeDisplayJSON(t, map[string]displayModel{DisplayModelKey: {Name: "custom"}})

	gotPath, err := EnsureDisplayModel(t.TempDir(), &video.Info{}, path)
	if err != nil {
		t.Fatalf("EnsureDisplayModel() error = %v", err)
	}
	if gotPath != path {
		t.Fatalf("display path = %q, want %q", gotPath, path)
	}
}

func TestEnsureDisplayModelRejectsOverrideWithoutReelModel(t *testing.T) {
	path := writeDisplayJSON(t, map[string]displayModel{"xav": {Name: "wrong name"}})

	_, err := EnsureDisplayModel(t.TempDir(), &video.Info{}, path)
	if err == nil {
		t.Fatalf("EnsureDisplayModel() error = nil, want error")
	}
	if !strings.Contains(err.Error(), `"reel" display model`) {
		t.Fatalf("EnsureDisplayModel() error = %q, want missing reel model", err)
	}
}

func readGeneratedDisplayModel(t *testing.T, path string) displayModel {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read generated display model: %v", err)
	}

	var models map[string]displayModel
	if err := json.Unmarshal(data, &models); err != nil {
		t.Fatalf("failed to decode generated display model: %v", err)
	}

	model, ok := models[DisplayModelKey]
	if !ok {
		t.Fatalf("generated display model missing %q key", DisplayModelKey)
	}
	return model
}

func writeDisplayJSON(t *testing.T, models map[string]displayModel) string {
	t.Helper()

	data, err := json.Marshal(models)
	if err != nil {
		t.Fatalf("failed to encode display model: %v", err)
	}
	path := t.TempDir() + "/display.json"
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write display model: %v", err)
	}
	return path
}
