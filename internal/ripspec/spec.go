package ripspec

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Envelope captures the structured payload shared between identification,
// ripping, encoding, and organizing stages.
type Envelope struct {
	Fingerprint string         `json:"fingerprint"`
	ContentKey  string         `json:"content_key"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Titles      []Title        `json:"titles,omitempty"`
	Episodes    []Episode      `json:"episodes,omitempty"`
	Assets      Assets         `json:"assets,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

// Title records playlist metadata captured during disc scanning.
type Title struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Duration       int    `json:"duration"`
	Chapters       int    `json:"chapters,omitempty"`
	Playlist       string `json:"playlist,omitempty"`
	SegmentCount   int    `json:"segment_count,omitempty"`
	SegmentMap     string `json:"segment_map,omitempty"`
	TitleHash      string `json:"title_hash"`
	Season         int    `json:"season,omitempty"`
	Episode        int    `json:"episode,omitempty"`
	EpisodeTitle   string `json:"episode_title,omitempty"`
	EpisodeAirDate string `json:"episode_air_date,omitempty"`
}

// Episode describes a target episode to be produced from the disc.
type Episode struct {
	Key             string  `json:"key"`
	TitleID         int     `json:"title_id"`
	Season          int     `json:"season"`
	Episode         int     `json:"episode"`
	EpisodeTitle    string  `json:"episode_title,omitempty"`
	EpisodeAirDate  string  `json:"episode_air_date,omitempty"`
	RuntimeSeconds  int     `json:"runtime_seconds,omitempty"`
	TitleHash       string  `json:"title_hash,omitempty"`
	OutputBasename  string  `json:"output_basename,omitempty"`
	MatchConfidence float64 `json:"match_confidence,omitempty"` // Confidence score from episode matching (0.0-1.0)
}

// Assets captures realised artefacts for each stage.
type Assets struct {
	Ripped    []Asset `json:"ripped,omitempty"`
	Encoded   []Asset `json:"encoded,omitempty"`
	Subtitled []Asset `json:"subtitled,omitempty"`
	Final     []Asset `json:"final,omitempty"`
}

// Asset status constants for per-episode tracking.
const (
	AssetStatusPending   = "pending"
	AssetStatusCompleted = "completed"
	AssetStatusFailed    = "failed"
)

// Asset associates an episode with a file path and its processing status.
type Asset struct {
	EpisodeKey string `json:"episode_key"`
	TitleID    int    `json:"title_id,omitempty"`
	Path       string `json:"path"`
	Status     string `json:"status,omitempty"`    // pending, completed, failed
	ErrorMsg   string `json:"error_msg,omitempty"` // per-episode error message
}

// Parse loads a rip spec from JSON, returning an empty envelope on blank input.
func Parse(raw string) (Envelope, error) {
	var env Envelope
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return env, nil
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return Envelope{}, err
	}
	env.Metadata = cloneMetadata(env.Metadata)
	env.Attributes = cloneMetadata(env.Attributes)
	env.Titles = slices.Clone(env.Titles)
	env.Episodes = slices.Clone(env.Episodes)
	env.Assets = env.Assets.Clone()
	return env, nil
}

// Encode serialises the envelope to JSON.
func (e Envelope) Encode() (string, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// EpisodeKey formats a deterministic key for an episode.
func EpisodeKey(season, episode int) string {
	if season <= 0 && episode <= 0 {
		return ""
	}
	if season <= 0 {
		season = 1
	}
	return fmt.Sprintf("s%02de%02d", season, episode)
}

// EpisodeByKey returns a pointer to the episode with the supplied key.
func (e *Envelope) EpisodeByKey(key string) *Episode {
	if e == nil {
		return nil
	}
	for idx := range e.Episodes {
		if strings.EqualFold(e.Episodes[idx].Key, key) {
			return &e.Episodes[idx]
		}
	}
	return nil
}

// AddAsset records a realised artefact for an episode.
func (a *Assets) AddAsset(kind string, asset Asset) {
	if a == nil {
		return
	}
	switch strings.ToLower(kind) {
	case "ripped":
		a.Ripped = appendOrReplace(a.Ripped, asset)
	case "encoded":
		a.Encoded = appendOrReplace(a.Encoded, asset)
	case "subtitled":
		a.Subtitled = appendOrReplace(a.Subtitled, asset)
	case "final":
		a.Final = appendOrReplace(a.Final, asset)
	}
}

// FindAsset locates a previously recorded artefact.
func (a Assets) FindAsset(kind, key string) (Asset, bool) {
	list := a.fromKind(kind)
	for _, asset := range list {
		if strings.EqualFold(asset.EpisodeKey, key) {
			return asset, true
		}
	}
	return Asset{}, false
}

// IsCompleted returns true if the asset has a path and is marked completed (or has no status).
func (a Asset) IsCompleted() bool {
	return strings.TrimSpace(a.Path) != "" && a.Status != AssetStatusFailed
}

// IsFailed returns true if the asset is marked as failed.
func (a Asset) IsFailed() bool {
	return a.Status == AssetStatusFailed
}

// ClearFailedAsset resets a failed asset so it can be retried.
func (assets *Assets) ClearFailedAsset(kind, key string) {
	if assets == nil {
		return
	}
	list := assets.listPtr(kind)
	if list == nil {
		return
	}
	for idx := range *list {
		if strings.EqualFold((*list)[idx].EpisodeKey, key) {
			(*list)[idx].Status = ""
			(*list)[idx].ErrorMsg = ""
			(*list)[idx].Path = ""
			return
		}
	}
}

func (a *Assets) listPtr(kind string) *[]Asset {
	switch strings.ToLower(kind) {
	case "ripped":
		return &a.Ripped
	case "encoded":
		return &a.Encoded
	case "subtitled":
		return &a.Subtitled
	case "final":
		return &a.Final
	default:
		return nil
	}
}

func (a Assets) fromKind(kind string) []Asset {
	if ptr := a.listPtr(kind); ptr != nil {
		return *ptr
	}
	return a.Ripped
}

func appendOrReplace(list []Asset, asset Asset) []Asset {
	replaced := false
	for idx := range list {
		if strings.EqualFold(list[idx].EpisodeKey, asset.EpisodeKey) {
			list[idx] = asset
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, asset)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].EpisodeKey == list[j].EpisodeKey {
			return list[i].TitleID < list[j].TitleID
		}
		return list[i].EpisodeKey < list[j].EpisodeKey
	})
	return list
}

func cloneMetadata(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	maps.Copy(out, input)
	return out
}

// Clone creates a deep copy of the assets set.
func (a Assets) Clone() Assets {
	return Assets{
		Ripped:    cloneAssets(a.Ripped),
		Encoded:   cloneAssets(a.Encoded),
		Subtitled: cloneAssets(a.Subtitled),
		Final:     cloneAssets(a.Final),
	}
}

func cloneAssets(list []Asset) []Asset {
	if len(list) == 0 {
		return nil
	}
	out := make([]Asset, len(list))
	copy(out, list)
	return out
}

// ExpectedCount returns the expected episode/file count.
// For TV content with episodes, returns len(Episodes).
// For movies (no episodes), returns 1.
func (e Envelope) ExpectedCount() int {
	if len(e.Episodes) > 0 {
		return len(e.Episodes)
	}
	return 1
}

// AssetCounts returns counts for each stage of the pipeline.
func (e Envelope) AssetCounts() (expected, ripped, encoded, final int) {
	expected = e.ExpectedCount()
	ripped = len(e.Assets.Ripped)
	encoded = len(e.Assets.Encoded)
	final = len(e.Assets.Final)
	return
}

// MissingEpisodes returns episode keys that don't have assets at the given stage.
// For movies, returns nil since there are no episode keys to track.
func (e Envelope) MissingEpisodes(stage string) []string {
	if len(e.Episodes) == 0 {
		return nil
	}
	var missing []string
	for _, ep := range e.Episodes {
		if _, ok := e.Assets.FindAsset(stage, ep.Key); !ok {
			missing = append(missing, ep.Key)
		}
	}
	return missing
}

// CompletedAssetCount returns the count of completed (non-failed) assets at the given stage.
func (a Assets) CompletedAssetCount(stage string) int {
	list := a.fromKind(stage)
	count := 0
	for _, asset := range list {
		if asset.IsCompleted() {
			count++
		}
	}
	return count
}
