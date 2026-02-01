# Preset Decider (LLM)

Spindle can call an OpenRouter-hosted LLM to decide whether Drapto should run
with its `clean`, `grain`, or default profile for each queue item. This document
describes how the integration works, how to configure it, and what to expect if
the model cannot provide a confident answer.

## When to use it

The Drapto CLI ships with presets:

| Preset | Intent (from Drapto) |
| --- | --- |
| `grain` | Preserve texture on film-sourced/noisy captures. Lower CRFs and addtional psy settings. |
| `clean` | Favor higher CRFs for animation or digitally shot content with minimal noise. |
| _no preset_ | Balanced defaults (CRF 25/27/29, SVT preset 6, AC bias 0.10, variance boost off). |

These presets let you select a profile based on content type rather than a single default.

## Rationale for LLM selection

Static rules for preset selection based on genre and year cover most content. The LLM is meant for edge cases such as older computer-animated movies and newer films with heavy grain.

The default model is `google/gemini-3-flash-preview`. GPT-5 mini performed well in testing. Some smaller models such as gpt-oss-20b were inconsistent on outliers.

## Data sent to the LLM

For each job the encoder sends:

```
<prompt from internal/services/llm/prompt.go>

Now classify this title:
South Park Season 5 1997 hd tv show
```

Only metadata already present in the queue item (title, season, year, movie/tv
flag) plus the inferred resolution are shared. File paths or user identifiers
are never included.

## Configuration

```toml
preset_decider_enabled = true
preset_decider_model = "google/gemini-3-flash-preview"
preset_decider_base_url = "https://openrouter.ai/api/v1/chat/completions"
preset_decider_api_key = " your_openrouter_key "
preset_decider_referer = "https://github.com/five82/spindle"
preset_decider_title = "Spindle Preset Decider"
```

- `preset_decider_enabled` – master toggle.
- `preset_decider_api_key` – read from config or the `OPENROUTER_API_KEY`
  (preferred) / `PRESET_DECIDER_API_KEY` env vars. For backward compatibility,
  `deepseek_api_key` and `DEEPSEEK_API_KEY` continue to work but are
  discouraged.
- `preset_decider_model` – any OpenRouter model that supports structured JSON
  output. Defaults to `google/gemini-3-flash-preview`.
- `preset_decider_base_url` – override if you self-host a router or call a
  different provider.
- `preset_decider_referer` & `preset_decider_title` – sent via headers because
  OpenRouter requires attribution; adjust if you deploy under a different URL.

The daemon must be restarted after changing the model or base URL.

## Workflow recap

1. Extract title/year/season/resolution from queue metadata.
2. Call the configured model via OpenRouter.
3. Enforce a 0.7 confidence gate; only `clean`/`grain` are accepted.
4. Update Drapto’s command-line arguments with `--drapto-preset <profile>` when
   confident, otherwise fall back to the default settings.
5. Log the raw JSON and append a short summary to the queue item’s progress
   message (e.g. `Encoding completed – preset Grain (1965 film-sourced)`).

If the LLM response is malformed or the API returns an error, Spindle logs the
failure and keeps using Drapto’s defaults so encoding continues.

## Health checks & observability

- `spindle status` now pings OpenRouter with a small JSON-only prompt so you can
  verify connectivity before starting work.
- Encoder logs include `preset_suggested`, `preset_confidence`, and `preset_raw`
  fields for inspection.
- A confidence threshold of 0.7 avoids most hallucinations; you can experiment
  locally by changing `presetConfidenceThreshold` in
  `internal/encoding/preset_selector.go` if needed.

## Troubleshooting tips

- 400 errors typically mean the model name is wrong or the provider requires a
  different response format. Double-check `preset_decider_model` against
  https://openrouter.ai/docs/models.
- Timeouts are usually network related; the health check uses a 45s timeout and
  reports whether the request failed or timed out.
- Remember to keep your API key private—do not check it into source control.
