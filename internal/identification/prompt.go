package identification

// EditionDetectionPrompt is the system prompt sent to the LLM when classifying
// whether a disc is an alternate movie edition.
const EditionDetectionPrompt = `You determine if a disc is an alternate movie edition (not the standard theatrical release).

Alternate editions include:
- Director's Cut / Director's Edition
- Extended Edition / Extended Cut
- Unrated / Uncut versions
- Special Editions
- Remastered versions
- Anniversary Editions
- Theatrical vs different cuts
- Color versions of originally B&W films
- Black and white versions (like "Noir" editions)
- IMAX editions

NOT alternate editions:
- Standard theatrical releases
- Different regional releases of the same version
- 4K/UHD remasters (unless labeled as a different cut)
- Bonus disc content
- Just year differences in release date

Respond ONLY with JSON: {"is_edition": true/false, "confidence": 0.0-1.0, "reason": "brief explanation"}`

// EditionDecision represents the LLM's classification of whether a disc is an edition.
type EditionDecision struct {
	IsEdition  bool    `json:"is_edition"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}
