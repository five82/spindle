#!/usr/bin/env python3
"""Extract and compare speaker embeddings using pyannote.

This script is called from the spindle commentary detector to compare
speaker voice identities between primary audio and candidate tracks.

Audio is pre-loaded via torchaudio to avoid pyannote's torchcodec issues.

Usage:
    python speaker_embed.py --primary p.wav --candidate c.wav --hf-token TOKEN

Output (JSON):
    {
        "primary_speaker_count": 3,
        "candidate_speaker_count": 1,
        "max_similarity": 0.23,
        "same_speakers": false
    }

Dependencies:
    - pyannote.audio
    - torch
    - torchaudio
    - numpy

Run via uvx:
    uvx --with pyannote.audio --with torchaudio --with numpy python speaker_embed.py ...
"""

import argparse
import json
import sys
import warnings

# Suppress the torchcodec warning since we pre-load audio ourselves
warnings.filterwarnings("ignore", message=".*torchcodec.*")

import numpy as np
import torch
import torchaudio
from pyannote.audio import Inference, Model, Pipeline
from pyannote.core import Segment


def load_audio(audio_path: str, sample_rate: int = 16000) -> dict:
    """Load audio using torchaudio and return pyannote-compatible dict."""
    waveform, sr = torchaudio.load(audio_path)
    # Resample if needed
    if sr != sample_rate:
        resampler = torchaudio.transforms.Resample(sr, sample_rate)
        waveform = resampler(waveform)
    # Convert to mono if stereo
    if waveform.shape[0] > 1:
        waveform = waveform.mean(dim=0, keepdim=True)
    return {"waveform": waveform, "sample_rate": sample_rate}


def extract_embeddings(audio_path: str, hf_token: str) -> dict:
    """Run diarization and extract per-speaker embeddings."""
    # Pre-load audio to avoid torchcodec issues
    audio_dict = load_audio(audio_path)

    # Use GPU if available
    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")

    pipeline = Pipeline.from_pretrained(
        "pyannote/speaker-diarization-3.1",
        token=hf_token,
    ).to(device)
    # Load embedding model explicitly, then wrap with Inference
    emb_model = Model.from_pretrained("pyannote/embedding", token=hf_token).to(device)
    embedding = Inference(emb_model, window="whole")

    # Run diarization on pre-loaded audio
    result = pipeline(audio_dict)
    # pyannote 3.x returns DiarizeOutput; extract the annotation
    diarization = result.speaker_diarization if hasattr(result, 'speaker_diarization') else result

    # Find longest segment per speaker
    speakers = {}
    for turn, _, speaker in diarization.itertracks(yield_label=True):
        dur = turn.end - turn.start
        if speaker not in speakers or dur > speakers[speaker]["duration"]:
            speakers[speaker] = {"start": turn.start, "end": turn.end, "duration": dur}

    # Extract embeddings for each speaker's best segment
    embeddings = {}
    for speaker, seg in speakers.items():
        # Crop waveform manually for embedding extraction
        sr = audio_dict["sample_rate"]
        start_sample = int(seg["start"] * sr)
        end_sample = int(seg["end"] * sr)
        segment_waveform = audio_dict["waveform"][:, start_sample:end_sample]
        segment_dict = {"waveform": segment_waveform, "sample_rate": sr}
        emb = embedding(segment_dict)
        embeddings[speaker] = emb.flatten().tolist()

    return {"speaker_count": len(embeddings), "embeddings": embeddings}


def cosine_similarity(a: list, b: list) -> float:
    """Compute cosine similarity between two embedding vectors."""
    a_arr, b_arr = np.array(a), np.array(b)
    norm_a = np.linalg.norm(a_arr)
    norm_b = np.linalg.norm(b_arr)
    if norm_a == 0 or norm_b == 0:
        return 0.0
    return float(np.dot(a_arr, b_arr) / (norm_a * norm_b))


def compare_embeddings(primary: dict, candidate: dict) -> dict:
    """Compare speaker embeddings between primary and candidate."""
    max_sim = 0.0

    # Handle edge case where one or both have no speakers detected
    if not primary["embeddings"] or not candidate["embeddings"]:
        return {
            "primary_speaker_count": primary["speaker_count"],
            "candidate_speaker_count": candidate["speaker_count"],
            "max_similarity": 0.0,
            "same_speakers": False,
        }

    for p_emb in primary["embeddings"].values():
        for c_emb in candidate["embeddings"].values():
            sim = cosine_similarity(p_emb, c_emb)
            max_sim = max(max_sim, sim)

    return {
        "primary_speaker_count": primary["speaker_count"],
        "candidate_speaker_count": candidate["speaker_count"],
        "max_similarity": max_sim,
        "same_speakers": max_sim >= 0.7,  # Threshold for "same voice"
    }


def main():
    parser = argparse.ArgumentParser(
        description="Compare speaker embeddings between two audio files"
    )
    parser.add_argument("--primary", required=True, help="Path to primary audio file")
    parser.add_argument(
        "--candidate", required=True, help="Path to candidate audio file"
    )
    parser.add_argument("--hf-token", required=True, help="HuggingFace API token")
    args = parser.parse_args()

    try:
        primary = extract_embeddings(args.primary, args.hf_token)
        candidate = extract_embeddings(args.candidate, args.hf_token)
        result = compare_embeddings(primary, candidate)
        print(json.dumps(result))
    except Exception as e:
        # Output error as JSON for Go to parse
        print(json.dumps({"error": str(e)}), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
