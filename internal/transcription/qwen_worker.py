#!/usr/bin/env python3
import json
import sys
from typing import Any, Dict, List


def _emit(payload: Dict[str, Any]) -> None:
    sys.stdout.write(json.dumps(payload, ensure_ascii=True) + "\n")
    sys.stdout.flush()


def _load_model(command: Dict[str, Any]):
    import torch
    from qwen_asr import Qwen3ASRModel

    dtype_name = (command.get("dtype") or "bfloat16").lower()
    dtype = getattr(torch, dtype_name, torch.bfloat16)
    kwargs: Dict[str, Any] = {
        "torch_dtype": dtype,
        "device_map": command.get("device") or "cuda:0",
        "trust_remote_code": True,
    }
    max_inference_batch_size = int(command.get("max_inference_batch_size") or 1)
    if command.get("forced_aligner_model"):
        kwargs["forced_aligner"] = command["forced_aligner_model"]
        kwargs["forced_aligner_kwargs"] = {
            "torch_dtype": dtype,
            "device_map": command.get("device") or "cuda:0",
            "trust_remote_code": True,
        }
    if command.get("use_flash_attention"):
        kwargs["attn_implementation"] = "flash_attention_2"
        if "forced_aligner_kwargs" in kwargs:
            kwargs["forced_aligner_kwargs"]["attn_implementation"] = "flash_attention_2"
    return Qwen3ASRModel.from_pretrained(
        command["asr_model"],
        max_inference_batch_size=max_inference_batch_size,
        **kwargs,
    )


def _extract_text(raw: Any) -> str:
    if isinstance(raw, str):
        return raw.strip()
    if isinstance(raw, dict):
        for key in ("text", "transcript", "pred_text"):
            value = raw.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
    for key in ("text", "transcript", "pred_text"):
        value = getattr(raw, key, None)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return ""


def _extract_language(raw: Any) -> str:
    if isinstance(raw, dict):
        for key in ("language", "detected_language", "lang"):
            value = raw.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
    for key in ("language", "detected_language", "lang"):
        value = getattr(raw, key, None)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return ""


def _extract_timestamps(raw: Any) -> List[Dict[str, Any]]:
    candidates = None
    if isinstance(raw, dict):
        candidates = raw.get("time_stamps") or raw.get("timestamps") or raw.get("words")
    if candidates is None:
        candidates = getattr(raw, "time_stamps", None) or getattr(raw, "timestamps", None) or getattr(raw, "words", None)
    if candidates is None:
        return []
    try:
        items = list(candidates)
    except Exception:
        return []
    out: List[Dict[str, Any]] = []
    for item in items:
        if isinstance(item, dict):
            text = item.get("text") or item.get("word") or ""
            start = item.get("start_time")
            if start is None:
                start = item.get("start")
            end = item.get("end_time")
            if end is None:
                end = item.get("end")
        else:
            text = getattr(item, "text", None) or getattr(item, "word", None) or ""
            start = getattr(item, "start_time", None)
            if start is None:
                start = getattr(item, "start", None)
            end = getattr(item, "end_time", None)
            if end is None:
                end = getattr(item, "end", None)
        try:
            start_f = float(start)
            end_f = float(end)
        except Exception:
            continue
        out.append({"text": str(text), "start_time": start_f, "end_time": end_f})
    return out


def _transcribe(model: Any, command: Dict[str, Any]) -> Dict[str, Any]:
    audio_path = command["audio_path"]
    language = command.get("language") or "English"
    want_timestamps = bool(command.get("return_time_stamps"))

    attempts = [
        {"audio": audio_path, "language": language, "return_time_stamps": want_timestamps},
        {"audio_path": audio_path, "language": language, "return_time_stamps": want_timestamps},
        {"audio": audio_path, "language": language},
        {"audio_path": audio_path, "language": language},
    ]

    last_error = None
    raw: Any = None
    for kwargs in attempts:
        try:
            raw = model.transcribe(**kwargs)
            break
        except TypeError as exc:
            last_error = exc
            continue
    if raw is None:
        raise RuntimeError(f"model.transcribe failed: {last_error}")

    if isinstance(raw, list) and raw:
        raw = raw[0]

    if not (hasattr(raw, "text") or hasattr(raw, "time_stamps") or hasattr(raw, "language")):
        try:
            from qwen_asr.inference.utils import parse_asr_output

            parsed = parse_asr_output(raw)
            if parsed is not None:
                raw = parsed
        except Exception:
            pass

    text = _extract_text(raw)
    timestamps = _extract_timestamps(raw) if want_timestamps else []
    language_name = _extract_language(raw) or language
    return {
        "language": language_name,
        "text": text,
        "time_stamps": timestamps,
    }


def main() -> int:
    model = None
    while True:
        line = sys.stdin.readline()
        if not line:
            return 0
        line = line.strip()
        if not line:
            continue
        try:
            command = json.loads(line)
            cmd = command.get("command")
            req_id = command.get("id") or "unknown"
            if cmd == "shutdown":
                _emit({"id": req_id, "ok": True})
                return 0
            if cmd == "health":
                import torch
                try:
                    import qwen_asr  # noqa: F401
                    qwen_version = getattr(qwen_asr, "__version__", "")
                except Exception:
                    qwen_version = ""
                if model is None and command.get("asr_model"):
                    model = _load_model(command)
                _emit(
                    {
                        "id": req_id,
                        "ok": True,
                        "cuda_visible": bool(torch.cuda.is_available()),
                        "device_count": int(torch.cuda.device_count()) if torch.cuda.is_available() else 0,
                        "torch_version": getattr(torch, "__version__", ""),
                        "qwen_asr_version": qwen_version,
                    }
                )
                continue
            if cmd == "transcribe":
                if model is None:
                    raise RuntimeError("worker not initialized")
                result = _transcribe(model, command)
                result.update({"id": req_id, "ok": True})
                _emit(result)
                continue
            raise RuntimeError(f"unsupported command: {cmd}")
        except Exception as exc:
            _emit({"id": command.get("id", "unknown") if 'command' in locals() else "unknown", "ok": False, "error": str(exc)})
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
