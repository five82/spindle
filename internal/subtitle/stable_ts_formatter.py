import argparse
import json
from pathlib import Path


def _normalize_token(value: str) -> str:
    return "".join(ch.lower() for ch in value if ch.isalnum())


def _parse_text_words(text: str):
    words = []
    start = None
    for idx, ch in enumerate(text):
        if ch.isalnum() or ch in ("'", "’", "-"):
            if start is None:
                start = idx
        elif start is not None:
            token = text[start:idx]
            norm = _normalize_token(token)
            if norm:
                words.append({"start": start, "norm": norm})
            start = None
    if start is not None:
        token = text[start:]
        norm = _normalize_token(token)
        if norm:
            words.append({"start": start, "norm": norm})
    return words


def _project_words_from_text(existing_text: str, words):
    if not existing_text or not isinstance(words, list):
        return words
    text_words = _parse_text_words(existing_text)
    if not text_words:
        return words
    projected = []
    word_index = 0
    for entry in words:
        token = entry.get("word")
        norm = _normalize_token(token if isinstance(token, str) else "")
        if not norm:
            projected.append(dict(entry))
            continue
        match = -1
        for idx in range(word_index, len(text_words)):
            if text_words[idx]["norm"] == norm:
                match = idx
                break
        if match == -1:
            return words
        start = text_words[match]["start"]
        end = len(existing_text)
        if match + 1 < len(text_words):
            end = text_words[match + 1]["start"]
        updated = dict(entry)
        updated["word"] = existing_text[start:end]
        projected.append(updated)
        word_index = match + 1
    return projected


def _load_segments(path: str):
    with open(path, "r", encoding="utf-8") as handle:
        payload = json.load(handle)

    if isinstance(payload, dict):
        segments = payload.get("segments") or payload.get("speech_segments")
        language = payload.get("language") or payload.get("detected_language")
    elif isinstance(payload, list):
        segments = payload
        language = None
    else:
        raise SystemExit("unsupported canonical transcript payload")

    if not segments:
        raise SystemExit("no segments provided")

    return segments, language


def _normalize_language(value: str | None) -> str:
    if not value:
        return "en"
    trimmed = value.strip()
    if not trimmed:
        return "en"
    return trimmed.split("-", 1)[0].lower()


def _sanitize_segments(raw_segments):
    if not isinstance(raw_segments, list):
        raise SystemExit("invalid segments payload")

    sanitized = []
    for entry in raw_segments:
        if not isinstance(entry, dict):
            continue
        segment = dict(entry)
        segment.pop("chars", None)
        existing_text = segment.get("text") if isinstance(segment.get("text"), str) else ""
        words = segment.get("words")
        if isinstance(words, list):
            words = _project_words_from_text(existing_text, words)
            normalized_words = []
            text_parts = []
            for idx, word_entry in enumerate(words):
                if not isinstance(word_entry, dict):
                    continue
                word = dict(word_entry)
                if "score" in word and "probability" not in word:
                    word["probability"] = word.pop("score")
                else:
                    word.pop("score", None)
                word.pop("speaker", None)
                word.pop("case", None)
                word.pop("chars", None)
                token = word.get("word")
                if isinstance(token, str):
                    trimmed = token.strip()
                    if idx == 0:
                        normalized = trimmed
                    else:
                        needs_space = True
                        if trimmed.startswith(("'", "’", ")", "]", "}", "?", "!", ".", ",", ":", ";")):
                            needs_space = False
                        if needs_space and trimmed and not trimmed.startswith(" "):
                            normalized = " " + trimmed
                        else:
                            normalized = trimmed
                    word["word"] = normalized
                    text_parts.append(word["word"])
                normalized_words.append(word)
            if existing_text.strip():
                segment["text"] = " ".join(existing_text.split())
            elif text_parts:
                segment["text"] = "".join(text_parts).strip()
            segment["words"] = normalized_words
        sanitized.append(segment)
    if not sanitized:
        raise SystemExit("no usable segments after sanitization")
    return sanitized


def main() -> None:
    parser = argparse.ArgumentParser("stable-ts-post")
    parser.add_argument("aligned_json", help="Canonical transcript JSON file")
    parser.add_argument("output_srt", help="Path to write formatted SRT")
    parser.add_argument("--language", default=None, help="Override language code")
    args = parser.parse_args()

    segments, detected_language = _load_segments(args.aligned_json)
    language = _normalize_language(args.language or detected_language)

    try:
        import stable_whisper
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"import_error:{exc}") from exc

    try:
        segments = _sanitize_segments(segments)
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"sanitize_error:{exc}") from exc

    try:
        result_wrapper = stable_whisper.WhisperResult(
            {"language": language, "segments": segments},
            force_order=True,
            check_sorted=False,
            show_unsorted=False,
        )
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"build_result_error:{exc}") from exc

    if getattr(result_wrapper, "has_segments", False):
        try:
            if getattr(result_wrapper, "has_words", False):
                result_wrapper.regroup(True)
                result_wrapper.clamp_max()
        except Exception as exc:  # pragma: no cover
            raise SystemExit(f"regroup_error:{exc}") from exc

    try:
        srt_content = result_wrapper.to_srt_vtt(
            filepath=None,
            segment_level=True,
            word_level=False,
            min_dur=0.12,
            strip=True,
        )
    except Exception as exc:  # pragma: no cover
        raise SystemExit(f"srt_render_error:{exc}") from exc

    Path(args.output_srt).write_text(srt_content, encoding="utf-8")


if __name__ == "__main__":
    main()
