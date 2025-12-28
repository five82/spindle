//go:build cgo

package commentary

import "github.com/visvasity/webrtcvad"

type vadProcessor struct {
	vad *webrtcvad.VAD
}

const vadModeAggressive = 3

func newVAD() (*vadProcessor, error) {
	vad, err := webrtcvad.New()
	if err != nil {
		return nil, err
	}
	// WebRTC VAD modes: 0 (quality) .. 3 (aggressive).
	if err := vad.SetMode(vadModeAggressive); err != nil {
		return nil, err
	}
	return &vadProcessor{vad: vad}, nil
}

func (v *vadProcessor) Process(sampleRate int, frame []byte) (bool, error) {
	return v.vad.Process(sampleRate, frame)
}
