import argparse
import json
from pathlib import Path

SAMPLE_RATE = 16000
FOREIGN_LANGUAGE_CONFIDENCE = 0.75


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


def _language_probability(result):
    for key in ("language_probability", "language_prob", "language_confidence"):
        value = result.get(key) if isinstance(result, dict) else None
        if isinstance(value, (int, float)):
            return float(value)
    return None


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


def _align_or_raw(whisperx, args, audio, raw_segments, detected_language):
    if not raw_segments:
        return raw_segments
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
            return aligned_result.get("segments") or raw_segments
    except Exception:
        pass
    return raw_segments


def _transcribe_default(whisperx, args, audio):
    model = _load_model(whisperx, args, language=args.language, task="transcribe")
    raw_result = model.transcribe(audio, batch_size=args.batch_size, chunk_size=args.chunk_size)
    detected_language = _language_code(raw_result.get("language"), args.language)
    raw_segments = raw_result.get("segments") or []
    aligned_segments = _align_or_raw(whisperx, args, audio, raw_segments, detected_language)
    return {
        "language": detected_language,
        "detected_language": raw_result.get("language") or detected_language,
        "segments": _clean_json(aligned_segments),
        "speech_segments": _clean_json(raw_segments),
        "mixed_language": False,
    }


def _chunk_audio(audio, start, end):
    start_index = max(0, int(float(start) * SAMPLE_RATE))
    end_index = max(start_index, int(float(end) * SAMPLE_RATE))
    return audio[start_index:end_index]


def _translated_chunk(auto_model, audio, segment, args):
    start = segment.get("start")
    end = segment.get("end")
    if not isinstance(start, (int, float)) or not isinstance(end, (int, float)):
        return None
    if float(end) <= float(start):
        return None
    chunk = _chunk_audio(audio, start, end)
    try:
        result = auto_model.transcribe(chunk, batch_size=args.batch_size, chunk_size=max(1, int(float(end) - float(start)) + 1))
    except Exception:
        return None
    source_language = _language_code(result.get("language"), "")
    if not source_language or source_language == "en":
        return None
    probability = _language_probability(result)
    if probability is not None and probability < FOREIGN_LANGUAGE_CONFIDENCE:
        return None
    parts = []
    for translated in result.get("segments") or []:
        if isinstance(translated, dict) and isinstance(translated.get("text"), str):
            text = translated.get("text", "").strip()
            if text:
                parts.append(text)
    if not parts:
        return None
    enriched = dict(segment)
    enriched["text"] = " ".join(parts).strip()
    enriched["source_language"] = source_language
    enriched["target_language"] = "en"
    enriched["task"] = "translate"
    enriched["foreign"] = True
    if probability is not None:
        enriched["language_probability"] = probability
    # Word timing from source-language audio is not meaningful for translated
    # English text. Keep segment-level timing only for forced-cue rendering.
    enriched.pop("words", None)
    enriched.pop("chars", None)
    return enriched


def _transcribe_mixed(whisperx, args, audio):
    payload = _transcribe_default(whisperx, args, audio)
    segments = payload.get("segments") or []
    normalized = []
    for segment in segments:
        if isinstance(segment, dict):
            enriched = dict(segment)
            enriched.setdefault("source_language", _language_code(args.language, "en"))
            enriched.setdefault("target_language", _language_code(args.language, "en"))
            enriched.setdefault("task", "transcribe")
            enriched.setdefault("foreign", False)
            normalized.append(enriched)
    payload["segments"] = normalized
    payload["mixed_language"] = True

    try:
        auto_model = _load_model(whisperx, args, language=None, task="translate")
    except Exception:
        payload["foreign_detection_error"] = "load_translate_model_failed"
        return payload

    foreign_count = 0
    foreign_languages = []
    for idx, segment in enumerate(normalized):
        translated = _translated_chunk(auto_model, audio, segment, args)
        if translated is None:
            continue
        normalized[idx] = translated
        foreign_count += 1
        language = translated.get("source_language")
        if isinstance(language, str) and language not in foreign_languages:
            foreign_languages.append(language)

    payload["segments"] = _clean_json(normalized)
    payload["foreign_segments"] = foreign_count
    payload["foreign_languages"] = foreign_languages
    return payload


def main() -> None:
    parser = argparse.ArgumentParser("spindle-whisperx-wrapper")
    parser.add_argument("--audio", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--language", required=True)
    parser.add_argument("--mode", choices=("transcribe", "mixed"), default="transcribe")
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

    try:
        if args.mode == "mixed":
            payload = _transcribe_mixed(whisperx, args, audio)
        else:
            payload = _transcribe_default(whisperx, args, audio)
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"transcribe_error:{exc}") from exc

    profile = {
        "name": args.transcription_profile_name,
        "mode": args.mode,
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
    payload["transcription_profile"] = profile

    json_path = output_dir / "audio.json"
    srt_path = output_dir / "audio.srt"
    json_path.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
    _write_srt(payload.get("segments") or [], srt_path)


if __name__ == "__main__":
    main()
