package contentid

// EpisodeVerificationPrompt is the system prompt sent to the LLM for
// second-level episode verification. It asks the model to compare a
// WhisperX transcript against an OpenSubtitles reference and decide
// whether both represent the same episode.
const EpisodeVerificationPrompt = `You compare two TV episode transcripts to determine if they are from the same episode.

TRANSCRIPT A is a WhisperX speech-to-text transcription from a Blu-ray disc.
TRANSCRIPT B is a reference subtitle from OpenSubtitles for a specific episode.

Both cover only the middle portion of the episode (approximately 10 minutes).
WhisperX transcripts may contain speech recognition errors.
Reference subtitles may differ in exact wording due to localization.

Focus on whether the same scenes and dialogue events occur in both.
Do NOT penalize minor word differences, transcription errors, or timing differences.

Respond ONLY with JSON: {"same_episode": true/false, "confidence": 0.0-1.0, "explanation": "brief reason"}`

// EpisodeVerification holds the parsed LLM response for an episode comparison.
type EpisodeVerification struct {
	SameEpisode bool    `json:"same_episode"`
	Confidence  float64 `json:"confidence"`
	Explanation string  `json:"explanation"`
}
