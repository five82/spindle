# System Design: LLM Prompts

All LLM interactions use the `CompleteJSON` interface (system prompt + user
prompt -> JSON response). Temperature is hardcoded to 0 for deterministic
output. See API_SERVICES.md Section 5 for the client protocol.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Edition Detection

**Stage**: Identification (DESIGN_STAGES.md Section 1.8)
**Package**: `identify`
**Confidence threshold**: 0.8

### 1.1 System Prompt

```
You determine if a disc is an alternate movie edition (not the standard theatrical release).

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

Respond ONLY with JSON: {"is_edition": true/false, "confidence": 0.0-1.0, "reason": "brief explanation"}
```

### 1.2 User Prompt

```
Disc: {disc_title}
TMDB: {tmdb_title}
```

Where `{disc_title}` and `{tmdb_title}` are trimmed strings.

### 1.3 Response Schema

```json
{"is_edition": true, "confidence": 0.92, "reason": "LOGAN_NOIR indicates a black and white edition"}
```

| Field | Type | Values |
|-------|------|--------|
| `is_edition` | bool | `true` / `false` |
| `confidence` | float | 0.0 - 1.0 (clamped) |
| `reason` | string | Brief explanation |

### 1.4 Trigger Conditions

Edition detection runs only for movies, after regex-based detection:

1. Known edition patterns checked first (regex). If matched, LLM is skipped.
2. If disc title has extra content beyond TMDB title but no regex match
   (ambiguous marker), send to LLM.
3. LLM result accepted when `confidence >= 0.8`.

### 1.5 Failure Behavior

LLM failure is non-fatal. If the call fails, edition detection falls back to
regex-only results. No review flag.

---

## 2. Commentary Classification

**Stage**: Audio Analysis (DESIGN_STAGES.md Section 5.2)
**Package**: `audioanalysis`
**Confidence threshold**: `commentary.confidence_threshold` (default 0.80)

### 2.1 System Prompt

```
You are an assistant that determines if an audio track is commentary or not.

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

You must respond ONLY with JSON: {"decision": "commentary" or "not_commentary", "confidence": 0.0-1.0, "reason": "brief explanation"}
```

### 2.2 User Prompt

```
Title: {title} ({year})

Transcript sample:
{transcript}
```

Where:
- `{title}` is the disc title (trimmed)
- `({year})` is omitted when year is empty
- `{transcript}` is truncated to 4000 characters with `\n[truncated]` appended
  if truncation occurs

The transcript is a WhisperX transcription of the first 10 minutes of the
candidate audio track.

### 2.3 Response Schema

```json
{"decision": "commentary", "confidence": 0.87, "reason": "Multiple voices discussing filmmaking over movie dialogue"}
```

| Field | Type | Values |
|-------|------|--------|
| `decision` | string | `"commentary"` or `"not_commentary"` |
| `confidence` | float | 0.0 - 1.0 |
| `reason` | string | Brief explanation |

A track is classified as commentary when
`decision == "commentary" && confidence >= confidence_threshold`.

### 2.4 Trigger Conditions

LLM classification runs only for candidates that survive stereo downmix
filtering (cosine similarity < `similarity_threshold`). Candidates are
non-primary 2-channel English/unknown-language audio tracks.

### 2.5 Failure Behavior

LLM failure is non-fatal but conservative:
- The candidate track is **preserved as commentary** (confidence 0, with error
  in reason). Losing a real commentary track is worse than keeping an extra
  audio track.
- Item flagged for review with reason describing the failure.

---

## 3. Episode Verification

**Stage**: Episode Identification (CONTENT_ID_DESIGN.md Section 11)
**Package**: `contentid`
**Verify threshold**: `llmVerifyThreshold` (default 0.85)

### 3.1 System Prompt

```
You compare two TV episode transcripts to determine if they are from the same episode.

TRANSCRIPT A is a WhisperX speech-to-text transcription from a Blu-ray disc.
TRANSCRIPT B is a reference subtitle from OpenSubtitles for a specific episode.

Both cover only the middle portion of the episode (approximately 10 minutes).
WhisperX transcripts may contain speech recognition errors.
Reference subtitles may differ in exact wording due to localization.

Focus on whether the same scenes and dialogue events occur in both.
Do NOT penalize minor word differences, transcription errors, or timing differences.

Respond ONLY with JSON: {"same_episode": true/false, "confidence": 0.0-1.0, "explanation": "brief reason"}
```

### 3.2 User Prompt

```
Episode key: {episode_key}
Target episode: {target_episode}

=== TRANSCRIPT A (WhisperX from disc) ===
{whisperx_text}

=== TRANSCRIPT B (OpenSubtitles reference) ===
{reference_text}
```

Where:
- `{episode_key}` is the rip's placeholder key (e.g., `s01_001`)
- `{target_episode}` is the candidate episode number (e.g., `3`)
- Each transcript is the middle ~10 minutes of dialogue, extracted via
  `MiddleSRTRange` with half-window `min(300s, totalDuration/2)`, truncated
  to 6000 characters

### 3.3 Response Schema

```json
{"same_episode": true, "confidence": 0.91, "explanation": "Both transcripts contain the same courtroom scene dialogue"}
```

| Field | Type | Values |
|-------|------|--------|
| `same_episode` | bool | `true` / `false` |
| `confidence` | float | 0.0 - 1.0 |
| `explanation` | string | Brief reason |

### 3.4 Trigger Conditions

Verification runs when an LLM client is configured and at least one cosine
similarity match scores below `llmVerifyThreshold` (0.85) or was flagged by
the contiguity check.

### 3.5 Escalation Logic

| Condition | Action |
|-----------|--------|
| 0 below threshold | Skip verification entirely |
| All verified | No changes |
| 1 rejection | Flag for review; keep original matches |
| 2+ rejections | Cross-match: N x M LLM comparisons across rejected episodes and references, greedy assignment via Hungarian algorithm |
| All cross-matches rejected | Flag for review |

### 3.6 Failure Behavior

LLM call failure during verification: original cosine match is retained, item
flagged for review. The match is kept because a network error should not
discard a plausible algorithmic match.

LLM call failure during cross-matching: that combination is skipped. If all
combinations fail, the item is flagged for review.

---

## 4. Common Patterns

All three prompts share these conventions:

1. **JSON-only response**: Every system prompt ends with an explicit response
   format instruction.
2. **Confidence score**: Every response includes a 0.0-1.0 confidence value.
3. **Explanation field**: Every response includes a brief human-readable reason.
4. **Temperature 0**: Deterministic output for reproducibility.
5. **Threshold-gated acceptance**: Each use site compares confidence against a
   threshold before accepting the LLM's decision.
6. **Non-fatal on failure**: No LLM failure causes a pipeline abort. Each call
   site has a defined fallback (regex-only, conservative preserve, or retain
   original match).

### 4.1 LLM Client Interface

All prompts are dispatched through a single interface:

```go
type Completer interface {
    CompleteJSON(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
```

Response parsing strips markdown code fences and extracts JSON before
unmarshaling into the prompt-specific response struct.
