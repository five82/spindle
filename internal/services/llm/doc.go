// Package llm provides an OpenRouter chat client for LLM-based classification.
//
// This package is used by:
//   - Encoding stage: determine Drapto encoding profile (clean, grain, default)
//   - Audio analysis stage: classify commentary tracks
//
// # Classification Logic
//
// The client sends a content description to a configured LLM model with a
// structured prompt requesting JSON output. The response contains the
// classification result, confidence score (0-1), and reasoning.
//
// # Configuration
//
// Requires api_key, model, and optionally base_url, referer, title, timeout.
// When unconfigured, callers should fall back to sensible defaults.
//
// # Entry Points
//
// NewClient: construct client from Config.
// Client.CompleteJSON: send system/user prompts, receive JSON response.
// Client.ClassifyPreset: preset-specific classification (for encoding stage).
// Client.HealthCheck: verify API key and model availability.
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
// sensible defaults. The Classification.Confidence field helps callers decide
// whether to trust the result or use a fallback.
package llm
