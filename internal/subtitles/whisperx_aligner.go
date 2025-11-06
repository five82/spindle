package subtitles

const whisperXAlignerScript = `
import sys

import whisperx
from whisperx import utils as whisperx_utils


def parse_timestamp(value: str) -> float:
    value = value.strip()
    if not value:
        return 0.0
    if "," in value:
        hms, millis = value.split(",", 1)
    else:
        hms, millis = value, "0"
    parts = hms.split(":")
    if len(parts) != 3:
        return 0.0
    hours, minutes, seconds = parts
    try:
        total = int(hours) * 3600 + int(minutes) * 60 + int(seconds)
        total += int(millis) / 1000.0
        return float(total)
    except ValueError:
        return 0.0


def load_segments(path: str):
    with open(path, "r", encoding="utf-8") as handle:
        content = handle.read().replace("\r\n", "\n")
    blocks = [block.strip() for block in content.split("\n\n") if block.strip()]
    segments = []
    for block in blocks:
        lines = block.split("\n")
        if not lines:
            continue
        idx = 0
        if lines[idx].strip().isdigit():
            idx += 1
        if idx >= len(lines):
            continue
        if "-->" not in lines[idx]:
            continue
        timing = lines[idx]
        idx += 1
        if idx >= len(lines):
            continue
        text = " ".join(line.strip() for line in lines[idx:] if line.strip())
        if not text:
            continue
        if "-->" not in timing:
            continue
        start_raw, end_raw = timing.split("-->")
        start = parse_timestamp(start_raw)
        end = parse_timestamp(end_raw)
        if end <= start:
            end = start + 0.5
        segments.append({"text": text, "start": start, "end": end})
    return segments


def main():
    if len(sys.argv) < 6:
        raise SystemExit("usage: <audio_path> <input_srt> <output_srt> <language> <device>")
    audio_path, input_path, output_path, language, device = sys.argv[1:6]
    language = (language or "en").strip() or "en"

    audio = whisperx.load_audio(audio_path)
    align_model, metadata = whisperx.load_align_model(language_code=language, device=device)
    segments = load_segments(input_path)
    if not segments:
        raise SystemExit("no segments to align")

    aligned = whisperx.align(
        segments,
        align_model,
        metadata,
        audio,
        device,
        return_char_alignments=False,
    )
    result = {
        "segments": aligned.get("segments", []),
        "language": aligned.get("language") or language,
    }
    if not result["segments"]:
        raise SystemExit("no aligned segments produced")
    options = {
        "max_line_width": None,
        "max_line_count": None,
        "highlight_words": False,
    }
    writer = whisperx_utils.WriteSRT(".")
    with open(output_path, "w", encoding="utf-8") as handle:
        writer.write_result(result, file=handle, options=options)



if __name__ == "__main__":
    main()
`
