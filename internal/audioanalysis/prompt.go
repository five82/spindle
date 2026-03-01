package audioanalysis

// CommentaryClassificationPrompt is the system prompt sent to the LLM
// when classifying whether an audio track is commentary.
const CommentaryClassificationPrompt = `You are an assistant that determines if an audio track is commentary or not.

IMPORTANT: Commentary tracks come in two forms:
1. Commentary-only: People talking about the film without movie audio
2. Mixed commentary: Movie/TV dialogue plays while commentators talk over it

Both forms are commentary. The presence of movie dialogue does NOT mean it's not commentary.
Mixed commentary will have movie dialogue interspersed with people discussing the film,
providing behind-the-scenes insights, or reacting to scenes.

Commentary tracks include:
- Director/cast commentary over the film (may include movie dialogue in background)
- Behind-the-scenes discussion mixed with film audio
- Any track where people discuss or react to the film while it plays
- Tracks with movie dialogue AND additional voices providing commentary

NOT commentary:
- Alternate language dubs (foreign language replacing original dialogue)
- Audio descriptions for visually impaired (narrator describing on-screen action)
- Stereo downmix of main audio (just the movie audio, no additional commentary)
- Isolated music/effects tracks

Given a transcript sample from an audio track, determine if it is commentary.

You must respond ONLY with JSON: {"decision": "commentary" or "not_commentary", "confidence": 0.0-1.0, "reason": "brief explanation"}`

// Commentary decision values returned by the LLM.
const (
	DecisionCommentary    = "commentary"
	DecisionNotCommentary = "not_commentary"
)

// CommentaryDecision represents the LLM's classification of an audio track.
type CommentaryDecision struct {
	Decision   string  `json:"decision"`   // "commentary" or "not_commentary"
	Confidence float64 `json:"confidence"` // 0.0-1.0
	Reason     string  `json:"reason"`     // Brief explanation
}

// IsCommentary returns true if the decision indicates commentary with sufficient confidence.
func (d CommentaryDecision) IsCommentary(confidenceThreshold float64) bool {
	return d.Decision == DecisionCommentary && d.Confidence >= confidenceThreshold
}
