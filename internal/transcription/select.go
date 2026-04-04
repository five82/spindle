package transcription

import (
	"context"
	"fmt"
	"strings"

	"github.com/five82/spindle/internal/language"
	mediaaudio "github.com/five82/spindle/internal/media/audio"
	"github.com/five82/spindle/internal/media/ffprobe"
)

var inspectMedia = ffprobe.Inspect

// SelectedAudio describes the audio stream chosen for transcription.
type SelectedAudio struct {
	Index    int
	Language string
	Label    string
}

// SelectPrimaryAudioTrack probes a media file, runs the shared audio-selection
// policy, and returns the selected audio-relative index plus a normalized
// language suitable for WhisperX.
func (s *Service) SelectPrimaryAudioTrack(ctx context.Context, inputPath, fallbackLanguage string) (SelectedAudio, error) {
	probe, err := inspectMedia(ctx, "", inputPath)
	if err != nil {
		return SelectedAudio{}, fmt.Errorf("probe media: %w", err)
	}

	selection := mediaaudio.Select(probe.Streams, s.logger)
	if selection.PrimaryIndex < 0 {
		return SelectedAudio{}, fmt.Errorf("no audio streams found")
	}

	selectedLanguage := language.ToISO2(language.ExtractFromTags(selection.Primary.Tags))
	if selectedLanguage == "" {
		selectedLanguage = language.ToISO2(strings.TrimSpace(fallbackLanguage))
	}
	if selectedLanguage == "" {
		selectedLanguage = "en"
	}

	return SelectedAudio{
		Index:    selection.PrimaryIndex,
		Language: selectedLanguage,
		Label:    selection.PrimaryLabel(),
	}, nil
}
