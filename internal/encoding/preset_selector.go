package encoding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/queue"
	"spindle/internal/services/presetllm"
)

const (
	presetConfidenceThreshold = 0.7
	resolutionLabelSD         = "480p/SD"
	resolutionLabelHD         = "1080p/HD"
	resolutionLabel4K         = "3840p/4K"
	resolutionLabelDefault    = resolutionLabelHD
	sourceTypeDVD             = "dvd"
	sourceTypeBluRay          = "blu-ray"
	sourceType4KBluRay        = "4k blu-ray"
)

type presetRequest struct {
	Title         string
	Season        int
	Year          string
	SeasonAirYear string
	Resolution    string
	MediaType     string
}

func (r presetRequest) Description() string {
	var parts []string

	// Title and season
	if title := strings.TrimSpace(r.Title); title != "" {
		if r.Season > 0 && r.MediaType == "tv show" {
			parts = append(parts, fmt.Sprintf("%s Season %d", title, r.Season))
		} else {
			parts = append(parts, title)
		}
	}

	// Type
	mediaType := strings.TrimSpace(r.MediaType)
	if mediaType == "" {
		mediaType = "movie"
	}
	parts = append(parts, fmt.Sprintf("(type: %s)", mediaType))

	// Year or season aired
	isTV := mediaType == "tv show"
	if isTV && strings.TrimSpace(r.SeasonAirYear) != "" && r.SeasonAirYear != r.Year {
		parts = append(parts, fmt.Sprintf("(season aired: %s)", r.SeasonAirYear))
	} else if year := strings.TrimSpace(r.Year); year != "" {
		parts = append(parts, fmt.Sprintf("(year: %s)", year))
	}

	// Resolution
	if resolution := strings.TrimSpace(r.Resolution); resolution != "" {
		parts = append(parts, fmt.Sprintf("(resolution: %s)", resolution))
	}

	// Source (derived from resolution)
	if source := deriveSourceType(r.Resolution); source != "" {
		parts = append(parts, fmt.Sprintf("(source: %s)", source))
	}

	return strings.Join(parts, " ")
}

type presetClassification struct {
	Profile     string
	Confidence  float64
	Reason      string
	Raw         string
	Description string
	Source      string
}

type presetClassifier interface {
	Classify(ctx context.Context, req presetRequest) (presetClassification, error)
}

type llmPresetClassifier struct {
	client *presetllm.Client
}

func newPresetLLMClassifier(cfg *config.Config) presetClassifier {
	if cfg == nil {
		return nil
	}
	clientCfg := presetllm.Config{
		APIKey:         cfg.PresetDecider.APIKey,
		BaseURL:        cfg.PresetDecider.BaseURL,
		Model:          cfg.PresetDecider.Model,
		Referer:        cfg.PresetDecider.Referer,
		Title:          cfg.PresetDecider.Title,
		TimeoutSeconds: cfg.PresetDecider.TimeoutSeconds,
	}
	if strings.TrimSpace(clientCfg.APIKey) == "" {
		return nil
	}
	return &llmPresetClassifier{client: presetllm.NewClient(clientCfg)}
}

func (c *llmPresetClassifier) Classify(ctx context.Context, req presetRequest) (presetClassification, error) {
	if c == nil || c.client == nil {
		return presetClassification{}, errors.New("preset LLM unavailable")
	}
	description := req.Description()
	classification, err := c.client.ClassifyPreset(ctx, description)
	if err != nil {
		return presetClassification{}, err
	}
	return presetClassification{
		Profile:     normalizePresetProfile(classification.Profile),
		Confidence:  classification.Confidence,
		Reason:      strings.TrimSpace(classification.Reason),
		Raw:         classification.Raw,
		Description: description,
		Source:      "preset_llm",
	}, nil
}

type presetDecision struct {
	Profile          string
	SuggestedProfile string
	Confidence       float64
	Reason           string
	Raw              string
	Description      string
	Source           string
	Applied          bool
}

func (e *Encoder) selectPreset(ctx context.Context, item *queue.Item, sampleSource string, logger *slog.Logger) presetDecision {
	var decision presetDecision
	if e == nil || e.cfg == nil || !e.cfg.PresetDecider.Enabled {
		return decision
	}
	if e.presetClassifier == nil {
		logger.Warn("preset decider unavailable; falling back to default",
			logging.Alert("preset_decider_fallback"),
			logging.String(logging.FieldDecisionType, "preset_decider"),
			logging.String(logging.FieldEventType, "preset_decider_unavailable"),
			logging.String(logging.FieldErrorHint, "configure preset_decider settings or disable the feature"),
		)
		return decision
	}
	if item == nil || strings.TrimSpace(item.MetadataJSON) == "" {
		logger.Info("preset decider skipped",
			logging.String("reason", "metadata unavailable"),
			logging.String(logging.FieldDecisionType, "preset_decider"),
		)
		return decision
	}
	meta := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	title := strings.TrimSpace(meta.Title())
	if title == "" {
		logger.Info("preset decider skipped",
			logging.String("reason", "title unavailable"),
			logging.String(logging.FieldDecisionType, "preset_decider"),
		)
		return decision
	}
	request := presetRequest{
		Title:         title,
		Season:        meta.SeasonNumber,
		Year:          parseYearFromMetadata(item.MetadataJSON),
		SeasonAirYear: parseSeasonAirYear(item.MetadataJSON),
		MediaType:     presetMediaType(meta),
		Resolution:    resolutionLabelDefault,
	}
	if !meta.IsMovie() && request.Season <= 0 {
		request.Season = 1
	}
	if res, err := e.detectResolutionLabel(ctx, sampleSource); err != nil {
		if sampleSource != "" {
			logger.Warn("preset decider resolution detection failed; using default resolution",
				logging.String("sample_source", sampleSource),
				logging.Error(err),
				logging.String(logging.FieldEventType, "preset_resolution_failed"),
				logging.String(logging.FieldErrorHint, "check ffprobe availability and sample file path"),
			)
		}
	} else if strings.TrimSpace(res) != "" {
		request.Resolution = res
	}
	classification, err := e.presetClassifier.Classify(ctx, request)
	if err != nil {
		attrs := []logging.Attr{logging.Error(err)}
		if e != nil && e.cfg != nil {
			timeoutSeconds := presetDeciderTimeoutSeconds(&e.cfg.PresetDecider)
			if timeoutSeconds > 0 {
				attrs = append(attrs, logging.Int("timeout_seconds", timeoutSeconds))
			}
			if model := strings.TrimSpace(e.cfg.PresetDecider.Model); model != "" {
				attrs = append(attrs, logging.String("model", model))
			}
			if baseURL := strings.TrimSpace(e.cfg.PresetDecider.BaseURL); baseURL != "" {
				attrs = append(attrs, logging.String("base_url", baseURL))
			}
		}
		attrs = append(attrs,
			logging.Alert("preset_decider_fallback"),
			logging.String(logging.FieldDecisionType, "preset_decider"),
			logging.String(logging.FieldEventType, "preset_decider_failed"),
			logging.String(logging.FieldErrorHint, "check preset_decider credentials or disable the feature"),
		)
		logger.Warn("preset decider classification failed", logging.Args(attrs...)...)
		return decision
	}
	decision.SuggestedProfile = classification.Profile
	decision.Confidence = classification.Confidence
	decision.Reason = classification.Reason
	decision.Raw = classification.Raw
	decision.Description = classification.Description
	decision.Source = classification.Source

	attrs := []logging.Attr{
		logging.String("preset_description", decision.Description),
		logging.String("preset_source", decision.Source),
		logging.String("preset_suggested", decision.SuggestedProfile),
		logging.Float64("preset_confidence", decision.Confidence),
	}
	if decision.Reason != "" {
		attrs = append(attrs, logging.String("preset_reason", decision.Reason))
	}
	if decision.Raw != "" {
		logger.Debug("preset decider raw response", logging.String("preset_raw", decision.Raw))
	}

	fallbackReason := ""
	if decision.SuggestedProfile == "" {
		fallbackReason = "no_profile"
	} else if decision.SuggestedProfile != "clean" && decision.SuggestedProfile != "grain" && decision.SuggestedProfile != "default" {
		fallbackReason = "unsupported_profile"
		attrs = append(attrs, logging.String("note", "unsupported profile"))
	} else if decision.Confidence < presetConfidenceThreshold {
		fallbackReason = "confidence_below_threshold"
		attrs = append(attrs, logging.Float64("required_confidence", presetConfidenceThreshold))
	} else {
		decision.Profile = decision.SuggestedProfile
		decision.Applied = true
	}

	decisionAttrs := append([]logging.Attr{
		logging.String(logging.FieldDecisionType, "preset_decider"),
		logging.String("decision_result", ternary(decision.Applied, "applied", "fallback")),
		logging.String("decision_selected", decision.Profile),
		logging.String("decision_reason", fallbackReason),
	}, attrs...)
	logger.Info("preset decider decision", logging.Args(decisionAttrs...)...)

	if !decision.Applied {
		logger.Warn("preset decider fallback applied",
			logging.Alert("preset_decider_fallback"),
			logging.String("fallback_reason", fallbackReason),
			logging.String("preset_suggested", decision.SuggestedProfile),
			logging.Float64("preset_confidence", decision.Confidence),
			logging.String(logging.FieldDecisionType, "preset_decider"),
			logging.String(logging.FieldEventType, "preset_decider_fallback"),
			logging.String(logging.FieldErrorHint, "review preset_decider confidence threshold or disable the feature"),
		)
	}
	return decision
}

func presetDeciderTimeoutSeconds(cfg *config.PresetDecider) int {
	if cfg != nil && cfg.TimeoutSeconds > 0 {
		return cfg.TimeoutSeconds
	}
	return int(presetllm.DefaultHTTPTimeout().Seconds())
}

func (e *Encoder) detectResolutionLabel(ctx context.Context, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("empty source path")
	}
	binary := "ffprobe"
	if e != nil && e.cfg != nil {
		binary = deps.ResolveFFprobePath(e.cfg.FFprobeBinary())
	}
	result, err := encodeProbe(ctx, binary, path)
	if err != nil {
		return "", err
	}
	width := maxVideoWidth(result)
	return classifyResolution(width), nil
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

func maxVideoWidth(result ffprobe.Result) int {
	maxWidth := 0
	for _, stream := range result.Streams {
		if !strings.EqualFold(stream.CodecType, "video") {
			continue
		}
		if stream.Width > maxWidth {
			maxWidth = stream.Width
		}
	}
	return maxWidth
}

func classifyResolution(width int) string {
	switch {
	case width >= 3200:
		return resolutionLabel4K
	case width >= 1200:
		return resolutionLabelHD
	case width > 0:
		return resolutionLabelSD
	default:
		return ""
	}
}

func deriveSourceType(resolution string) string {
	resolution = strings.TrimSpace(resolution)
	switch resolution {
	case resolutionLabelSD:
		return sourceTypeDVD
	case resolutionLabelHD:
		return sourceTypeBluRay
	case resolutionLabel4K:
		return sourceType4KBluRay
	default:
		return ""
	}
}

func presetMediaType(meta queue.Metadata) string {
	if meta.IsMovie() {
		return "movie"
	}
	return "tv show"
}

func parseYearFromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	for _, key := range []string{"year", "release_year"} {
		if year := coerceYear(payload[key]); year != "" {
			return year
		}
	}
	for _, key := range []string{"release_date", "first_air_date"} {
		if value, ok := payload[key].(string); ok {
			if year := extractDigits(value); year != "" {
				return year
			}
		}
	}
	return ""
}

func coerceYear(value any) string {
	switch v := value.(type) {
	case string:
		return extractDigits(v)
	case float64:
		if v <= 0 {
			return ""
		}
		return fmt.Sprintf("%04d", int(v))
	default:
		return ""
	}
}

func extractDigits(value string) string {
	digits := make([]rune, 0, 4)
	for _, r := range value {
		if unicode.IsDigit(r) {
			digits = append(digits, r)
			if len(digits) == 4 {
				break
			}
		}
	}
	if len(digits) == 4 {
		return string(digits)
	}
	return ""
}

func parseSeasonAirYear(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	airDatesRaw, ok := payload["episode_air_dates"]
	if !ok {
		return ""
	}
	airDates, ok := airDatesRaw.([]any)
	if !ok || len(airDates) == 0 {
		return ""
	}
	// Find the earliest air date to get the season's production year
	var earliestYear string
	for _, dateRaw := range airDates {
		dateStr, ok := dateRaw.(string)
		if !ok {
			continue
		}
		year := extractDigits(dateStr)
		if year == "" {
			continue
		}
		if earliestYear == "" || year < earliestYear {
			earliestYear = year
		}
	}
	return earliestYear
}

func normalizePresetProfile(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "clean", "grain", "default":
		return value
	default:
		return ""
	}
}

func presetSummary(decision presetDecision) string {
	if !decision.Applied || strings.TrimSpace(decision.Profile) == "" {
		return ""
	}
	label := capitalizeASCII(decision.Profile)
	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		return fmt.Sprintf("preset %s (%s)", label, reason)
	}
	return fmt.Sprintf("preset %s", label)
}
