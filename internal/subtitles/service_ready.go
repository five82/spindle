package subtitles

import (
	"fmt"
	"os/exec"
	"strings"

	"spindle/internal/deps"
	"spindle/internal/services"
)

func (s *Service) ensureReady() error {
	if s == nil {
		return services.Wrap(services.ErrConfiguration, "subtitles", "init", "Subtitle service unavailable", nil)
	}
	s.readyOnce.Do(func() {
		if s.skipCheck {
			return
		}
		if _, err := exec.LookPath(whisperXCommand); err != nil {
			s.readyErr = services.Wrap(services.ErrConfiguration, "subtitles", "locate whisperx", fmt.Sprintf("Could not find %q on PATH", whisperXCommand), err)
			return
		}
		if _, err := exec.LookPath(ffmpegCommand); err != nil {
			s.readyErr = services.Wrap(services.ErrConfiguration, "subtitles", "locate ffmpeg", fmt.Sprintf("Could not find %q on PATH", ffmpegCommand), err)
			return
		}
	})
	if s.readyErr != nil {
		return s.readyErr
	}
	// configuredVADMethod already returns normalized lowercase values
	if s.configuredVADMethod() == whisperXVADMethodPyannote && strings.TrimSpace(s.hfToken) == "" {
		return services.Wrap(services.ErrConfiguration, "subtitles", "validate vad", "pyannote VAD selected but no Hugging Face token configured (set whisperx_hf_token)", nil)
	}
	return nil
}

func (s *Service) ffprobeBinary() string {
	if s != nil && s.config != nil {
		return deps.ResolveFFprobePath(s.config.FFprobeBinary())
	}
	return "ffprobe"
}
