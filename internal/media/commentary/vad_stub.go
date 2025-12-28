//go:build !cgo

package commentary

import "errors"

type vadProcessor struct{}

func newVAD() (*vadProcessor, error) {
	return nil, errors.New("webrtcvad unavailable (cgo disabled)")
}

func (v *vadProcessor) Process(sampleRate int, frame []byte) (bool, error) {
	return false, errors.New("webrtcvad unavailable (cgo disabled)")
}
