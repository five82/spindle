# Commentary Detection

Spindle automatically identifies and selects commentary audio tracks from disc rips, distinguishing them from audio description, downmix duplicates, and music-only tracks.

## Goal

Extract commentary tracks (director/cast discussions) for inclusion in the final encode while rejecting:
- **Audio description (AD)**: Narration for visually impaired viewers
- **Downmix duplicates**: Same audio in different channel configurations
- **Music/effects tracks**: Isolated scores or sound effects

Commentary tracks are valuable bonus content that fans want preserved alongside the primary audio.

## The Challenge

Disc audio tracks rarely have reliable metadata. A track labeled "stereo" might be:
- Director commentary
- Audio description
- Stereo downmix of the main audio
- Music-only track

Spindle uses multiple detection methods to classify tracks accurately.

## Detection Pipeline

### Phase 1: Metadata Classification

Quick rejection/acceptance based on track titles:
- **Positive signals**: "commentary", "director", "cast", "filmmaker"
- **Negative signals**: "audio description", "descriptive", "visually impaired"

Tracks with clear metadata skip further analysis.

### Phase 2: Audio Analysis

For ambiguous tracks, Spindle extracts sample windows and computes:

| Metric | What It Measures | Commentary Pattern |
|--------|------------------|-------------------|
| Speech ratio | % of track containing speech | 25-80% (people talking) |
| Speech overlap | When speech occurs vs primary | High (talks during dialogue) |
| Speech in silence | When speech occurs vs primary silence | Low-moderate |
| Fingerprint similarity | Audio content match | Low (different content) |
| Speech timing correlation | Pattern similarity to primary | Varies |

### Phase 3: Speaker Embedding (Optional)

Uses pyannote to extract voice embeddings and compare speakers:
- **Same voices as primary** + low speech-in-silence → downmix (reject)
- **Same voices** + high speech-in-silence → AD with movie audio bleed-through (reject)
- **Different voices** + low speech-in-silence → commentary (accept)
- **Different voices** + high speech-in-silence → audio description (reject)

### Phase 4: WhisperX Analysis (Fallback)

For still-ambiguous tracks, transcribes audio and looks for:
- Director/cast discussion keywords
- Audio description phrases ("he walks", "she looks")
- Movie dialogue (indicates downmix)

## Classification Logic

```
High fingerprint similarity (>98%)     → Duplicate downmix
Low speech ratio (<10%)                → Music/effects track
High speech-in-silence (>40%)          → Audio description
  + Low primary overlap (<30%)
High primary overlap (>60%)            → Commentary (mixed with movie)
  + Moderate fingerprint (<98%)
Low fingerprint + speech present       → Commentary only
```

## Key Insight: Audio Description Detection

AD tracks are tricky because they contain the **full movie audio** plus narrator. This means:
- Speaker embedding shows HIGH similarity (movie voices match)
- But speech-in-silence is HIGH (narrator fills quiet moments)

The combination of these metrics reliably identifies AD even when speaker voices appear to match.

## Dependencies

### Required
- **FFmpeg**: Audio extraction and sample generation
- **fpcalc** (Chromaprint): Audio fingerprinting

### Optional (Speaker Embedding)
Enabled via `speaker_embedding_enabled = true` in config.

Python dependencies (managed via uvx):
- `pyannote.audio` - Speaker diarization and embedding
- `torch` / `torchaudio` - Audio processing
- `numpy` - Numerical operations
- `soundfile` - Audio I/O fallback
- `omegaconf` - Model configuration

**HuggingFace Setup**:
1. Create account at https://huggingface.co
2. Generate access token at https://huggingface.co/settings/tokens
3. Accept model terms (one-time):
   - https://hf.co/pyannote/speaker-diarization-3.1
   - https://hf.co/pyannote/segmentation-3.0
   - https://hf.co/pyannote/embedding
   - https://hf.co/pyannote/speaker-diarization-community-1
4. Set `whisperx_hf_token` in config (shared with WhisperX VAD)

### Optional (WhisperX Fallback)
Uses existing WhisperX configuration for transcription-based analysis.

## Configuration

```toml
[commentary_detection]
# Enable speaker embedding analysis (requires HF token)
speaker_embedding_enabled = false

# Audio analysis thresholds
speech_ratio_min_commentary = 0.25      # Minimum speech for commentary
speech_ratio_max_music = 0.10           # Maximum speech for music track
speech_overlap_primary_min = 0.60       # Minimum overlap with primary speech
speech_overlap_primary_max_ad = 0.30    # Maximum overlap for AD detection
speech_in_silence_max = 0.40            # Maximum speech during primary silence
fingerprint_similarity_duplicate = 0.98 # Similarity threshold for duplicates

# Sample extraction
sample_window_count = 3                 # Number of sample windows
sample_window_seconds = 90              # Duration of each window
```

## Usage

### Manual Testing
```bash
# Analyze commentary candidates for cached item
spindle cache commentary <item_id> --verbose

# Listen to extracted samples
ls ~/.cache/spindle/commentary/*.opus
```

### Automatic Detection
Commentary detection runs automatically during the encoding stage when processing queue items with multiple audio tracks.

## Output

Detected commentary tracks are:
1. Included in the Drapto encode command
2. Tagged with appropriate metadata
3. Preserved in the final output file

## Debugging

Sample audio files are saved for manual verification:
```
~/.cache/spindle/commentary/
├── commentary_candidate_6_eng_speaker_same_voices.opus
├── commentary_candidate_7_eng_speaker_audio_description.opus
└── ...
```

Enable verbose logging to see detailed metrics:
```bash
spindle cache commentary <id> --verbose
```

Key log fields:
- `speech_ratio`: Percentage of track with speech
- `speech_overlap_primary`: Correlation with primary speech timing
- `speech_in_primary_silence`: Speech during quiet movie moments
- `fingerprint_similarity`: Audio content similarity to primary
- `max_similarity`: Speaker embedding cosine similarity
