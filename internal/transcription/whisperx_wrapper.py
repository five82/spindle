import argparse
import json
from pathlib import Path

SAMPLE_RATE = 16000


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


def _language_code(value, default=""):
    if not isinstance(value, str):
        return default
    value = value.strip().lower().split("-", 1)[0]
    return value or default


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


def _load_model(whisperx, args, *, language, task):
    asr_options = {"condition_on_previous_text": args.condition_on_previous_text}
    vad_options = {
        "chunk_size": args.chunk_size,
        "vad_onset": args.vad_onset,
        "vad_offset": args.vad_offset,
    }
    kwargs = {
        "compute_type": args.compute_type,
        "language": language,
        "asr_options": asr_options,
        "vad_method": args.vad_method,
        "vad_options": vad_options,
        "use_auth_token": args.hf_token or None,
    }
    if task:
        kwargs["task"] = task
    try:
        return whisperx.load_model(args.model, args.device, **kwargs)
    except TypeError:
        kwargs.pop("task", None)
        if task:
            asr_options["task"] = task
        return whisperx.load_model(args.model, args.device, **kwargs)


def _align_cached(whisperx, args, align_cache, audio, raw_segments, detected_language):
    if not raw_segments:
        return raw_segments
    try:
        if detected_language not in align_cache:
            align_cache[detected_language] = whisperx.load_align_model(
                language_code=detected_language,
                device=args.device,
            )
        align_model, align_metadata = align_cache[detected_language]
        aligned_result = whisperx.align(
            raw_segments,
            align_model,
            align_metadata,
            audio,
            args.device,
            return_char_alignments=False,
        )
        if isinstance(aligned_result, dict) and aligned_result.get("segments"):
            return aligned_result.get("segments") or raw_segments
    except Exception:
        align_cache.pop(detected_language, None)
    return raw_segments


def _transcribe_one(whisperx, args, model_cache, align_cache, audio, language):
    if language not in model_cache:
        model_cache[language] = _load_model(whisperx, args, language=language, task="transcribe")
    model = model_cache[language]
    raw_result = model.transcribe(audio, batch_size=args.batch_size, chunk_size=args.chunk_size)
    detected_language = _language_code(raw_result.get("language"), language)
    raw_segments = raw_result.get("segments") or []
    aligned_segments = _align_cached(whisperx, args, align_cache, audio, raw_segments, detected_language)
    return {
        "language": detected_language,
        "detected_language": raw_result.get("language") or detected_language,
        "segments": _clean_json(aligned_segments),
        "speech_segments": _clean_json(raw_segments),
    }


def main() -> None:
    parser = argparse.ArgumentParser("spindle-whisperx-wrapper")
    # --audio, --output-dir, and --language repeat together: item i reads
    # audio[i] in language[i] and writes audio.srt/audio.json to output-dir[i].
    # ASR and align models are loaded once per language and reused across
    # items, which is the point of batching.
    parser.add_argument("--audio", action="append", required=True)
    parser.add_argument("--output-dir", action="append", required=True)
    parser.add_argument("--language", action="append", required=True)
    parser.add_argument("--model", required=True)
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

    if len(args.audio) != len(args.output_dir) or len(args.audio) != len(args.language):
        raise SystemExit(
            f"arg_error:audio/output-dir/language counts differ "
            f"({len(args.audio)}/{len(args.output_dir)}/{len(args.language)})"
        )

    try:
        import whisperx
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"import_error:{exc}") from exc

    profile = {
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
    }

    model_cache = {}
    align_cache = {}
    for audio_path, out_dir, language in zip(args.audio, args.output_dir, args.language):
        output_dir = Path(out_dir)
        output_dir.mkdir(parents=True, exist_ok=True)

        try:
            audio = whisperx.load_audio(audio_path)
        except Exception as exc:  # pragma: no cover
            raise SystemExit(f"load_audio_error:{audio_path}:{exc}") from exc

        try:
            payload = _transcribe_one(whisperx, args, model_cache, align_cache, audio, language)
        except Exception as exc:  # pragma: no cover
            raise SystemExit(f"transcribe_error:{audio_path}:{exc}") from exc

        payload["transcription_profile"] = profile

        json_path = output_dir / "audio.json"
        srt_path = output_dir / "audio.srt"
        json_path.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
        _write_srt(payload.get("segments") or [], srt_path)


if __name__ == "__main__":
    main()
