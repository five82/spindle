package transcription

import "testing"

func TestServiceConfigReflectsDevice(t *testing.T) {
	for _, tc := range []struct {
		cuda   bool
		device string
	}{{false, "cpu"}, {true, "cuda"}} {
		service := &Service{model: "large-v3", cudaEnabled: tc.cuda, vadMethod: "silero"}
		model, device, vad := service.Config()
		if model != "large-v3" || device != tc.device || vad != "silero" {
			t.Fatalf("config: %s %s %s", model, device, vad)
		}
	}
}
