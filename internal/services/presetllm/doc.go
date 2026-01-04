// Package presetllm provides an OpenRouter chat client for encoding preset
// classification. The encoding stage uses this client to determine whether
// content should use Drapto's clean, grain, or default encoding profile.
//
// # Classification Logic
//
// The client sends a content description (title, year, type) to a configured
// LLM model with a structured prompt requesting JSON output. The response
// contains a profile name, confidence score (0-1), and reasoning. The encoding
// stage uses this classification to select the appropriate Drapto preset.
//
// Available profiles:
//   - "default": balanced settings suitable for most content.
//   - "clean": digitally shot content or CG animation with minimal noise.
//   - "grain": film-sourced content where preserving texture matters.
//
// # Configuration
//
// Requires preset_llm_api_key (OpenRouter key), preset_llm_model, and optionally
// preset_llm_base_url, preset_llm_referer, preset_llm_title, preset_llm_timeout.
// When unconfigured, the encoding stage falls back to the default profile.
//
// # Entry Points
//
// NewClient: construct client from Config.
// Client.ClassifyPreset: send description, receive Classification with profile/confidence/reason.
// Client.HealthCheck: verify API key and model availability.
// Client.CompleteJSON: low-level JSON completion for custom prompts.
//
// # Retry Behaviour
//
// The client retries on HTTP 408/429/5xx errors and network timeouts with
// exponential backoff (base 1s, max 10s, up to 5 attempts by default).
// Context cancellation aborts retries immediately.
//
// # Fallback
//
// If the LLM is unavailable or returns an error, callers should fall back to
// the default profile. The Classification.Confidence field helps callers
// decide whether to trust the result or use a fallback.
package presetllm
