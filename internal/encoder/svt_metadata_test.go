package encoder

import (
	"testing"

	"github.com/five82/reel/internal/video"
)

func TestSVTConfigAcceptsColorAndQualityMetadata(t *testing.T) {
	primaries, transfer, matrix := int32(1), int32(1), int32(1)
	colorRange, chroma := int32(1), int32(1)
	mastering := "G(0.2650,0.6900)B(0.1500,0.0600)R(0.6800,0.3200)WP(0.3127,0.3290)L(1000.0000,0.0050)"
	contentLight := "1000,400"
	cfg := &EncConfig{
		Inf: &video.Info{
			FPSNum: 25, FPSDen: 1, ColorPrimaries: &primaries,
			TransferCharacteristics: &transfer, MatrixCoefficients: &matrix,
			ColorRange: &colorRange, ChromaSamplePosition: &chroma,
			MasteringDisplay: &mastering, ContentLight: &contentLight,
		},
		Width: 32, Height: 32, CRF: 30, Preset: 12,
		ACBias: 0.5, EnableVarianceBoost: true, VarianceBoostStrength: 2, VarianceOctile: 4,
	}
	enc, err := newSvtEncoder(cfg)
	if err != nil {
		t.Fatalf("SVT rejected valid metadata: %v", err)
	}
	enc.close()
}
