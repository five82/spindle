import argparse
import json
from pathlib import Path


def _bool_arg(value: str) -> bool:
    normalized = value.strip().lower()
    if normalized in {"1", "true", "yes", "on"}:
        return True
    if normalized in {"0", "false", "no", "off"}:
        return False
    raise argparse.ArgumentTypeError(f"invalid boolean value: {value}")


def _clean_json(value):
    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    if isinstance(value, dict):
        return {str(k): _clean_json(v) for k, v in value.items() if v is not None}
    if isinstance(value, (list, tuple)):
        return [_clean_json(v) for v in value]
    item = getattr(value, "item", None)
    if callable(item):
        try:
            return item()
        except Exception:
            pass
    return str(value)


def _format_timestamp(seconds: float) -> str:
    if seconds < 0:
        seconds = 0.0
    total_ms = int(round(seconds * 1000.0))
    hours = total_ms // 3600000
    total_ms -= hours * 3600000
    minutes = total_ms // 60000
    total_ms -= minutes * 60000
    secs = total_ms // 1000
    millis = total_ms - secs * 1000
    return f"{hours:02d}:{minutes:02d}:{secs:02d},{millis:03d}"


def _write_srt(segments, path: Path) -> None:
    lines = []
    index = 1
    for segment in segments:
        if not isinstance(segment, dict):
            continue
        start = segment.get("start")
        end = segment.get("end")
        text = segment.get("text")
        if not isinstance(start, (int, float)) or not isinstance(end, (int, float)):
            continue
        if not isinstance(text, str) or not text.strip():
            continue
        lines.append(str(index))
        lines.append(f"{_format_timestamp(float(start))} --> {_format_timestamp(float(end))}")
        lines.append(text.strip())
        lines.append("")
        index += 1
    path.write_text("\n".join(lines), encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser("spindle-whisperx-wrapper")
    parser.add_argument("--audio", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--language", required=True)
    parser.add_argument("--vad-method", required=True)
    parser.add_argument("--device", required=True)
    parser.add_argument("--compute-type", required=True)
    parser.add_argument("--batch-size", type=int, default=16)
    parser.add_argument("--chunk-size", type=int, default=30)
    parser.add_argument("--vad-onset", type=float, default=0.500)
    parser.add_argument("--vad-offset", type=float, default=0.363)
    parser.add_argument("--hf-token", default="")
    parser.add_argument("--condition-on-previous-text", type=_bool_arg, default=False)
    parser.add_argument("--transcription-profile-name", required=True)
    args = parser.parse_args()

    try:
        import whisperx
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"import_error:{exc}") from exc

    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    try:
        audio = whisperx.load_audio(args.audio)
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"load_audio_error:{exc}") from exc

    asr_options = {"condition_on_previous_text": args.condition_on_previous_text}
    vad_options = {
        "chunk_size": args.chunk_size,
        "vad_onset": args.vad_onset,
        "vad_offset": args.vad_offset,
    }

    try:
        model = whisperx.load_model(
            args.model,
            args.device,
            compute_type=args.compute_type,
            language=args.language,
            asr_options=asr_options,
            vad_method=args.vad_method,
            vad_options=vad_options,
            use_auth_token=args.hf_token or None,
        )
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"load_model_error:{exc}") from exc

    try:
        raw_result = model.transcribe(audio, batch_size=args.batch_size, chunk_size=args.chunk_size)
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"transcribe_error:{exc}") from exc

    detected_language = raw_result.get("language") or args.language
    raw_segments = raw_result.get("segments") or []

    aligned_segments = raw_segments
    try:
        align_model, align_metadata = whisperx.load_align_model(
            language_code=detected_language,
            device=args.device,
        )
        aligned_result = whisperx.align(
            raw_segments,
            align_model,
            align_metadata,
            audio,
            args.device,
            return_char_alignments=False,
        )
        if isinstance(aligned_result, dict) and aligned_result.get("segments"):
            aligned_segments = aligned_result.get("segments") or raw_segments
    except Exception:
        aligned_result = {"segments": raw_segments, "language": detected_language}

    payload = {
        "language": detected_language,
        "detected_language": raw_result.get("language") or detected_language,
        "segments": _clean_json(aligned_segments),
        "speech_segments": _clean_json(raw_segments),
        "transcription_profile": {
            "name": args.transcription_profile_name,
            "vad_method": args.vad_method,
            "device": args.device,
            "compute_type": args.compute_type,
            "condition_on_previous_text": args.condition_on_previous_text,
            "batch_size": args.batch_size,
            "chunk_size": args.chunk_size,
            "vad_onset": args.vad_onset,
            "vad_offset": args.vad_offset,
            "use_auth_token": bool(args.hf_token),
        },
    }

    json_path = output_dir / "audio.json"
    srt_path = output_dir / "audio.srt"
    json_path.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
    _write_srt(payload["segments"], srt_path)


if __name__ == "__main__":
    main()
