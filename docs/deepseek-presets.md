# DeepSeek-Driven Drapto Presets

Spindle can ask DeepSeek's `deepseek-reasoner` model to decide whether Drapto
should run in its `grain`, `clean`, or default mode for each queue item. This
doc explains what the integration does, how to configure it, and what to expect
when it cannot make a confident decision.

## Why it exists

Drapto exposes tuned preset bundles:

| Preset | Intent (from the Drapto project) |
| --- | --- |
| `grain` | Preserve texture on film-sourced or noisy captures (CRF 22/24/26, SVT preset 5, higher AC bias, variance boost enabled). |
| `clean` | Favor speed + size for animation or digitally shot content with little noise (CRF 26/28/30, SVT preset 6, lower AC bias, variance boost disabled). |
| _no preset_ | Drapto's global defaults (CRF 24/26/28, SVT preset 6, AC bias 0.30, variance boost enabled) which are a balanced middle ground. |

Manually picking between these profiles for every disc is tedious, so Spindle
lets an LLM make the call using the metadata it already gathered.

## How it works

1. **Metadata extraction** – The encoding stage grabs the title, show title,
   season, year, and media type from `queue.Item.MetadataJSON`. It also runs
   `ffprobe` on the first ripped source to estimate SD/HD/UHD.
2. **Prompt** – Spindle sends the text snippet stored in
   `internal/services/deepseek/prompt.go` plus a one-line description such as
   `"South Park Season 5 1997 hd tv show"` to DeepSeek's chat completion API.
3. **LLM response** – The model must return JSON with `profile`, `confidence`,
   and `reason`. Only `clean` or `grain` are considered; any other value maps to
   default behaviour.
4. **Confidence gate** – When the response has `confidence >= 0.7`, matches a
   supported profile, and metadata was available, Spindle forwards the profile
   to Drapto via `--drapto-preset <clean|grain>`. Otherwise it leaves Drapto in
   its baseline (no preset) mode.
5. **Surface the decision** – Raw JSON plus a short summary (e.g. `Encoding
   completed – preset Clean (computer-animated TV show)`) are logged so you can
   reason about the classifier's choice.

If the DeepSeek API errors, times out, or returns malformed JSON, encoding is
not blocked—Spindle simply logs the failure and continues with default Drapto
settings.

## Configuration

The feature is **disabled by default**; no network calls are made until you
flip the toggle.

```toml
deepseek_preset_decider_enabled = true
deepseek_api_key = "your_key_here"  # or export DEEPSEEK_API_KEY
```

- `deepseek_preset_decider_enabled` – master switch. When `false`, Spindle never
  sends metadata to DeepSeek and never requests `clean`/`grain` presets.
- `deepseek_api_key` – stored alongside other secrets in `config.toml`. Leave it
  blank and set the `DEEPSEEK_API_KEY` environment variable if you prefer not to
  keep tokens on disk.

After enabling the toggle, restart the daemon so the encoder picks up the API
client.

## Observability & troubleshooting

- **Logs** – look for `deepseek preset` lines in the encoder logs. They include
  the description string, returned profile, confidence, and any raw JSON.
- **Queue progress** – success messages append `– preset Clean (…)` or
  `– preset Grain (…)` when the classifier was applied.
- **Low confidence** – anything < 0.7 is treated as "unsure". You can adjust
  the threshold in code (`presetConfidenceThreshold` in
  `internal/encoding/preset_selector.go`) if you want to experiment locally.
- **API failures** – Spindle falls back to defaults and logs the HTTP status /
  decoding error. Verify outbound connectivity and make sure the key has
  permissions for the `deepseek-reasoner` model.

## Privacy considerations

Only the synthesized description line (title, year, resolution, media type) is
sent to DeepSeek; no file paths or user identifiers are included.
