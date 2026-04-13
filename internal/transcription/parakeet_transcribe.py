#!/usr/bin/env python3
"""Parakeet transcription helper for Spindle.

This helper writes the canonical transcription artifacts expected by the Go
pipeline today:

- audio.srt
- audio.json

The JSON payload intentionally preserves the existing segment-oriented shape so
subtitle filtering and formatting can continue to consume it without needing a
backend-specific branch.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import wave
from contextlib import nullcontext
from pathlib import Path
from typing import Any

os.environ.setdefault("TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD", "1")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser("spindle-parakeet-transcribe")
    parser.add_argument("--input", required=True, help="Input mono 16k WAV path")
    parser.add_argument("--output-dir", required=True, help="Directory for audio.srt/audio.json")
    parser.add_argument("--model", default="nvidia/parakeet-tdt-0.6b-v2")
    parser.add_argument("--device", default="cuda", choices=["auto", "cuda", "cpu"])
    parser.add_argument("--dtype", default="bf16", choices=["bf16", "fp32"])
    parser.add_argument("--language", default="en")
    parser.add_argument("--long-audio-threshold-sec", type=float, default=480.0)
    return parser.parse_args()


def wav_duration_seconds(path: Path) -> float:
    with wave.open(str(path), "rb") as handle:
        frames = handle.getnframes()
        rate = handle.getframerate()
        if rate <= 0:
            raise RuntimeError(f"invalid wav framerate: {rate}")
        return frames / float(rate)


def format_srt_time(seconds: float) -> str:
    total_ms = max(0, int(round(seconds * 1000.0)))
    hours = total_ms // 3_600_000
    total_ms %= 3_600_000
    minutes = total_ms // 60_000
    total_ms %= 60_000
    secs = total_ms // 1000
    millis = total_ms % 1000
    return f"{hours:02}:{minutes:02}:{secs:02},{millis:03}"


def write_srt(path: Path, segments: list[dict[str, Any]]) -> None:
    lines: list[str] = []
    cue_idx = 1
    for seg in segments:
        text = str(seg.get("text", "")).strip()
        if not text:
            continue
        start = float(seg["start"])
        end = float(seg["end"])
        if end < start:
            end = start
        lines.extend(
            [
                str(cue_idx),
                f"{format_srt_time(start)} --> {format_srt_time(end)}",
                text,
                "",
            ]
        )
        cue_idx += 1
    path.write_text("\n".join(lines), encoding="utf-8")


def normalize_device(requested: str, torch_module: Any) -> str:
    if requested == "auto":
        return "cuda" if torch_module.cuda.is_available() else "cpu"
    if requested == "cuda" and not torch_module.cuda.is_available():
        raise RuntimeError("cuda requested but torch.cuda.is_available() is false")
    return requested


def assign_words_to_segments(
    segments: list[dict[str, Any]],
    words: list[dict[str, Any]],
    epsilon: float = 0.05,
) -> list[dict[str, Any]]:
    assigned: list[dict[str, Any]] = []
    word_idx = 0
    word_count = len(words)

    for seg in segments:
        seg_start = float(seg["start"])
        seg_end = float(seg["end"])
        seg_words: list[dict[str, Any]] = []

        while word_idx < word_count and float(words[word_idx].get("end", 0.0)) < seg_start - epsilon:
            word_idx += 1

        scan_idx = word_idx
        while scan_idx < word_count:
            word = words[scan_idx]
            w_start = float(word.get("start", 0.0))
            w_end = float(word.get("end", 0.0))
            if w_start > seg_end + epsilon:
                break
            if w_end >= seg_start - epsilon and w_start <= seg_end + epsilon:
                token = str(word.get("word", "")).strip()
                if token:
                    seg_words.append(
                        {
                            "word": token,
                            "start": round(w_start, 3),
                            "end": round(max(w_start, w_end), 3),
                        }
                    )
            scan_idx += 1

        seg_payload = {
            "start": round(seg_start, 3),
            "end": round(max(seg_start, seg_end), 3),
            "text": str(seg.get("text", "")).strip(),
        }
        if seg_words:
            seg_payload["words"] = seg_words
        assigned.append(seg_payload)
        word_idx = scan_idx

    return assigned


def should_retry_fp32(exc: Exception) -> bool:
    message = str(exc).lower()
    return "same dtype" in message and "bfloat16" in message and "float" in message



def transcribe_audio(model: Any, input_path: Path, torch_module: Any, use_bf16: bool) -> Any:
    autocast_ctx = nullcontext()
    if use_bf16:
        autocast_ctx = torch_module.autocast(device_type="cuda", dtype=torch_module.bfloat16)
    with torch_module.inference_mode():
        with autocast_ctx:
            return model.transcribe([str(input_path)], timestamps=True)



def main() -> int:
    args = parse_args()

    if args.language.lower() != "en":
        raise SystemExit(f"Parakeet TDT 0.6B v2 is English-only; got language={args.language}")

    input_path = Path(args.input)
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    srt_path = output_dir / "audio.srt"
    json_path = output_dir / "audio.json"

    duration_sec = wav_duration_seconds(input_path)

    try:
        import torch
        from nemo.collections.asr.models import ASRModel
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"import_error:{exc}") from exc

    device = normalize_device(args.device, torch)
    use_bf16 = args.dtype == "bf16" and device == "cuda"

    model = None
    try:
        model = ASRModel.from_pretrained(model_name=args.model)
        model.eval()

        if hasattr(model, "to"):
            model.to(device)
            model.to(torch.float32)

        if duration_sec > args.long_audio_threshold_sec:
            if hasattr(model, "change_attention_model"):
                model.change_attention_model("rel_pos_local_attn", [256, 256])
            if hasattr(model, "change_subsampling_conv_chunking_factor"):
                model.change_subsampling_conv_chunking_factor(1)

        try:
            output = transcribe_audio(model, input_path, torch, use_bf16)
        except RuntimeError as exc:
            if not use_bf16 or not should_retry_fp32(exc):
                raise
            print(
                "precision_fallback: bf16 transcription failed with dtype mismatch; retrying in fp32",
                file=sys.stderr,
            )
            if hasattr(model, "to"):
                model.to(torch.float32)
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            output = transcribe_audio(model, input_path, torch, False)

        if not output or not isinstance(output, list):
            raise RuntimeError("unexpected empty transcription output")

        hyp = output[0]
        ts = getattr(hyp, "timestamp", None)
        if not ts:
            raise RuntimeError("transcription output missing timestamp data")

        segment_ts = list(ts.get("segment") or [])
        word_ts = list(ts.get("word") or [])
        text = getattr(hyp, "text", "") or ""

        segments: list[dict[str, Any]] = []
        for seg in segment_ts:
            seg_text = str(seg.get("segment", "")).strip()
            start = float(seg.get("start", 0.0))
            end = float(seg.get("end", start))
            if not seg_text:
                continue
            segments.append(
                {
                    "start": start,
                    "end": max(start, end),
                    "text": seg_text,
                }
            )

        if not segments and text.strip():
            segments = [
                {
                    "start": 0.0,
                    "end": round(duration_sec, 3),
                    "text": text.strip(),
                }
            ]

        if not segments:
            raise RuntimeError("no usable segments in transcription output")

        canonical_segments = assign_words_to_segments(segments, word_ts)
        payload = {
            "language": "en",
            "segments": canonical_segments,
        }

        json_path.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
        write_srt(srt_path, canonical_segments)
        return 0
    except torch.cuda.OutOfMemoryError as exc:  # type: ignore[attr-defined]
        raise SystemExit(f"cuda_oom:{exc}") from exc
    except Exception as exc:
        raise SystemExit(f"transcription_error:{exc}") from exc
    finally:
        try:
            if model is not None and hasattr(model, "cpu") and device == "cuda":
                model.cpu()
        except Exception:
            pass
        try:
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
        except Exception:
            pass


if __name__ == "__main__":
    sys.exit(main())
