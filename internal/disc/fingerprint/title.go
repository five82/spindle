package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"spindle/internal/disc"
)

// TitleFingerprint computes a deterministic fingerprint for a MakeMKV title using
// stable attributes (duration, track ordering, codec metadata, languages, etc.).
// The goal is to identify the logical piece of content regardless of which disc
// it appears on, so the disc fingerprint is intentionally excluded.
func TitleFingerprint(title disc.Title) string {
	hasher := sha256.New()

	writeComponent(hasher, strings.ToLower(strings.TrimSpace(title.Name)))
	writeComponent(hasher, strconv.Itoa(title.Duration))

	tracks := append([]disc.Track(nil), title.Tracks...)
	sort.Slice(tracks, func(i, j int) bool {
		if tracks[i].StreamID == tracks[j].StreamID {
			return tracks[i].Type < tracks[j].Type
		}
		return tracks[i].StreamID < tracks[j].StreamID
	})

	for _, track := range tracks {
		writeComponent(hasher, strconv.Itoa(track.StreamID))
		writeComponent(hasher, strconv.Itoa(track.Order))
		writeComponent(hasher, strings.ToLower(string(track.Type)))
		writeComponent(hasher, strings.ToLower(strings.TrimSpace(track.CodecID)))
		writeComponent(hasher, strings.ToLower(strings.TrimSpace(track.CodecShort)))
		writeComponent(hasher, strings.ToLower(strings.TrimSpace(track.CodecLong)))
		writeComponent(hasher, strings.ToLower(strings.TrimSpace(track.Language)))
		writeComponent(hasher, strings.ToLower(strings.TrimSpace(track.LanguageName)))
		writeComponent(hasher, strings.ToLower(strings.TrimSpace(track.Name)))
		writeComponent(hasher, strconv.Itoa(track.ChannelCount))
		writeComponent(hasher, strings.ToLower(strings.TrimSpace(track.ChannelLayout)))
		writeComponent(hasher, strings.ToLower(strings.TrimSpace(track.BitRate)))

		if len(track.Attributes) > 0 {
			keys := make([]int, 0, len(track.Attributes))
			for key := range track.Attributes {
				keys = append(keys, key)
			}
			sort.Ints(keys)
			for _, key := range keys {
				writeComponent(hasher, strconv.Itoa(key))
				writeComponent(hasher, strings.ToLower(strings.TrimSpace(track.Attributes[key])))
			}
		}
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

func writeComponent(hasher hashWriter, value string) {
	if hasher == nil {
		return
	}
	_, _ = hasher.Write([]byte(value))
	_, _ = hasher.Write([]byte{0})
}

type hashWriter interface {
	Write(p []byte) (int, error)
}
