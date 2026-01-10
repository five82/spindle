package ripspec

import (
	"encoding/json"
	"fmt"
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
	Key            string `json:"key"`
	TitleID        int    `json:"title_id"`
	Season         int    `json:"season"`
	Episode        int    `json:"episode"`
	EpisodeTitle   string `json:"episode_title,omitempty"`
	EpisodeAirDate string `json:"episode_air_date,omitempty"`
	RuntimeSeconds int    `json:"runtime_seconds,omitempty"`
	TitleHash      string `json:"title_hash,omitempty"`
	OutputBasename string `json:"output_basename,omitempty"`
}

// Assets captures realised artefacts for each stage.
type Assets struct {
	Ripped    []Asset `json:"ripped,omitempty"`
	Encoded   []Asset `json:"encoded,omitempty"`
	Subtitled []Asset `json:"subtitled,omitempty"`
	Final     []Asset `json:"final,omitempty"`
}

// Asset associates an episode with a file path.
type Asset struct {
	EpisodeKey string `json:"episode_key"`
	TitleID    int    `json:"title_id,omitempty"`
	Path       string `json:"path"`
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

func (a Assets) fromKind(kind string) []Asset {
	switch strings.ToLower(kind) {
	case "encoded":
		return a.Encoded
	case "subtitled":
		return a.Subtitled
	case "final":
		return a.Final
	default:
		return a.Ripped
	}
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
	for k, v := range input {
		out[k] = v
	}
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
