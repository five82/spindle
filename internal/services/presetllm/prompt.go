package presetllm

// PresetClassificationPrompt captures the instructions sent to the configured
// LLM when classifying whether Drapto should use clean, grain, or default
// settings. Update this text centrally so every call stays in sync.
const PresetClassificationPrompt = `You are an assistant that chooses an encoding profile for a movie or tv show.

Available profiles:

- "default": these are our default encoding settings and are a good balance between speed, efficiency, and size. Most content should be classified as default.

- "clean": digitally shot live-action with little visible noise, or fully computer-animated films dating back to the 1990s. Typically modern animation/CG and very clean digital captures.

- "grain": older or film-sourced content with greater than normal amounts of film grain or noise, where preserving texture is important.

You must respond ONLY with a JSON object like: {"profile": "clean", "confidence": 0.92, "reason": "short explanation"}

Now classify this title:`
