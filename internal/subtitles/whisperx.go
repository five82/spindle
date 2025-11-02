package subtitles

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type whisperXWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type whisperXSegment struct {
	Text  string         `json:"text"`
	Start float64        `json:"start"`
	End   float64        `json:"end"`
	Words []whisperXWord `json:"words"`
}

type whisperXPayload struct {
	Segments []whisperXSegment `json:"segments"`
}

func loadWhisperSegments(path string) ([]whisperXSegment, error) {
	if strings.TrimSpace(path) == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload whisperXPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse whisperx json: %w", err)
	}
	return payload.Segments, nil
}
