package processing

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/encode"
	"github.com/five82/reel/internal/media"
	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/video"
)

func TestAudioDescriptions(t *testing.T) {
	streams := []media.AudioStreamInfo{{Index: 2, Channels: 2}, {Index: 4, Channels: 6}}
	if got := audioChannelsFromStreams(streams); !reflect.DeepEqual(got, []uint32{2, 6}) {
		t.Fatalf("channels = %v", got)
	}
	for _, tt := range []struct {
		channels                  []uint32
		streams                   []media.AudioStreamInfo
		basic, configured, result string
	}{
		{nil, nil, "No audio", "No audio", "No audio"},
		{[]uint32{2}, nil, "2 channels", "2 channels", "Opus 2ch @ 128kbps"},
		{[]uint32{2, 6}, nil, "2 streams: Stream 0 (2ch), Stream 1 (6ch)", "2 streams: Stream 0 (2ch), Stream 1 (6ch)", "Opus (2ch@128k, 6ch@258k)"},
		{nil, []media.AudioStreamInfo{}, "No audio", "No audio", "No audio"},
		{nil, streams[:1], "No audio", "2 channels @ 128kbps Opus", "Opus 2ch @ 128kbps"},
		{nil, streams, "No audio", "Stream 2: 2ch [128kbps Opus], Stream 4: 6ch [258kbps Opus]", "Opus (2ch@128k, 6ch@258k)"},
	} {
		if got := FormatAudioDescription(tt.channels); got != tt.basic {
			t.Errorf("basic = %q, want %q", got, tt.basic)
		}
		if got := FormatAudioDescriptionConfig(tt.channels, tt.streams); got != tt.configured {
			t.Errorf("config = %q, want %q", got, tt.configured)
		}
		if got := GenerateAudioResultsDescription(tt.channels, tt.streams); got != tt.result {
			t.Errorf("results = %q, want %q", got, tt.result)
		}
	}
	if got := GetAudioStreamInfo(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Fatalf("missing audio = %v", got)
	}
}

func TestQualityAndEncodeDescriptions(t *testing.T) {
	cfg := config.NewConfig("/input", "/output", "/logs")
	cfg.QualityMode = config.QualityModeCRF
	for _, tt := range []struct {
		width uint32
		tier  string
	}{{640, "SD"}, {1920, "HD"}, {3840, "UHD"}} {
		inf := &video.Info{Width: tt.width}
		crf, _ := determineQualitySettings(&media.VideoProperties{Width: tt.width}, cfg)
		if got := formatQualityDescription(inf, crf, cfg); !strings.Contains(got, tt.tier) {
			t.Fatalf("description for %d: %s", tt.width, got)
		}
	}
	cfg.QualityMode = config.QualityModeTarget
	cfg.TargetQualityMin, cfg.TargetQualityMax = 8, 9
	for _, tt := range []struct {
		width uint32
		hdr   bool
		want  string
	}{{1920, false, "SSIMULACRA2"}, {3840, false, "CVVDP"}, {1920, true, "CVVDP"}} {
		inf := &video.Info{Width: tt.width}
		if tt.hdr {
			pq := int32(16)
			inf.TransferCharacteristics = &pq
		}
		if got := formatQualityDescription(inf, 25, cfg); !strings.Contains(got, tt.want) {
			t.Fatalf("description = %q, want %s", got, tt.want)
		}
	}
	if formatDynamicRange(true) != "HDR" || formatDynamicRange(false) != "SDR" {
		t.Fatal("dynamic range label")
	}
	for _, tt := range []struct {
		hdr    media.HDRInfo
		matrix string
	}{{media.HDRInfo{}, "bt709"}, {media.HDRInfo{IsHDR: true}, "bt2020nc"}, {media.HDRInfo{IsHDR: true, MatrixCoefficients: "bt2020c"}, "bt2020c"}} {
		params := setupEncodeParams(cfg, 25, &tt.hdr)
		if params.MatrixCoefficients != tt.matrix || params.PixelFormat != "yuv420p10le" || params.Quality != 25 || params.Preset != cfg.SVTAV1Preset {
			t.Fatalf("params = %+v", params)
		}
	}
}

func TestResumeManifestRecordsSourceAndTreatment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.mkv")
	if err := os.WriteFile(path, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig(path, t.TempDir(), t.TempDir())
	cfg.QualityMode = config.QualityModeTarget
	inf := &video.Info{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Frames: 240}
	chunks := []chunk.Chunk{{Idx: 0, Start: 0, End: 120}, {Idx: 1, Start: 120, End: 240}}
	treatment := encode.GrainTreatment{Stats: &perf.GrainTreatmentStats{Denoise: "fftdnoiz", GrainTable: "table"}}
	m, err := buildResumeManifest(path, inf, cfg, chunks, "crop=1920:800:0:140", 5, 25, treatment)
	if err != nil {
		t.Fatal(err)
	}
	if m.InputSize != 6 || m.InputPath != chunk.CanonicalInputPath(path) || m.Width != 1920 || m.Frames != 240 || m.ChunkFingerprint != chunk.ChunkFingerprint(chunks) || m.Denoise != "fftdnoiz" || m.GrainTable != "table" {
		t.Fatalf("manifest = %+v", m)
	}
	cfg.QualityMode = config.QualityModeCRF
	cfg.Denoise, cfg.GrainTable = "hqdn3d", "custom"
	m, err = buildResumeManifest(path, inf, cfg, chunks, "", 5, 25, treatment)
	if err != nil || m.Denoise != "hqdn3d" || m.GrainTable != "custom" {
		t.Fatalf("fixed CRF manifest = %+v, %v", m, err)
	}
	if _, err := buildResumeManifest(path+".missing", inf, cfg, chunks, "", 5, 25, treatment); err == nil {
		t.Fatal("missing source accepted")
	}
	if d, tab := treatmentIdentity(encode.GrainTreatment{}); d != "" || tab != "" {
		t.Fatalf("empty treatment = %q, %q", d, tab)
	}
}
