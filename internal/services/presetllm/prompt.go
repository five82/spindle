package presetllm

// PresetClassificationPrompt captures the instructions sent to the configured
// LLM when classifying whether Drapto should use clean, grain, or default
// settings. Update this text centrally so every call stays in sync.
const PresetClassificationPrompt = `You are an assistant that chooses an encoding profile for a movie or tv show.

Available profiles:

- "clean": digitally shot live-action with little visible noise, or fully computer-animated films and TV. Typically modern animation/CG and very clean digital captures.

- "grain": older or film-sourced content with greater than normal amounts of film grain or noise, where preserving texture is important.

- "default": these are our default encoding settings and are a good balance between speed, efficiency, and size.

Rules:

- Use "clean" for animated or CGI-heavy films where the image is mostly noise-free, even if the movie is old (e.g. Toy Story 1995).

- Use "grain" for older film-origin content, especially pre-2000, unless you are confident they were mostly digital or CGI. Content with heavier amounts of grain that should be preserved.

- Use "default" for all other content. Most content should be classified as default.

You must respond ONLY with a JSON object like: {"profile": "clean", "confidence": 0.92, "reason": "short explanation"}

Now classify this title:`
