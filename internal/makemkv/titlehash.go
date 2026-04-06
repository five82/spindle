package makemkv

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"sort"
	"strconv"
	"strings"
)

// TitleHash computes a deterministic SHA-256 hash for a title using stable
// attributes (name, duration, segment map, track metadata). The goal is to
// identify the logical piece of content regardless of which disc it appears
// on, so the disc fingerprint is intentionally excluded.
func TitleHash(title TitleInfo) string {
	h := sha256.New()

	writeHashComponent(h, strings.ToLower(strings.TrimSpace(title.Name)))
	writeHashComponent(h, strconv.Itoa(title.Duration))
	writeHashComponent(h, strings.TrimSpace(title.SegmentMap))

	tracks := make([]Track, len(title.Tracks))
	copy(tracks, title.Tracks)
	sort.Slice(tracks, func(i, j int) bool {
		if tracks[i].StreamID == tracks[j].StreamID {
			return tracks[i].Type < tracks[j].Type
		}
		return tracks[i].StreamID < tracks[j].StreamID
	})

	for _, track := range tracks {
		writeHashComponent(h, strconv.Itoa(track.StreamID))
		writeHashComponent(h, strconv.Itoa(track.Order))
		writeHashComponent(h, strings.ToLower(string(track.Type)))
		writeHashComponent(h, strings.ToLower(strings.TrimSpace(track.CodecID)))
		writeHashComponent(h, strings.ToLower(strings.TrimSpace(track.CodecShort)))
		writeHashComponent(h, strings.ToLower(strings.TrimSpace(track.CodecLong)))
		writeHashComponent(h, strings.ToLower(strings.TrimSpace(track.Language)))
		writeHashComponent(h, strings.ToLower(strings.TrimSpace(track.LanguageName)))
		writeHashComponent(h, strings.ToLower(strings.TrimSpace(track.Name)))
		writeHashComponent(h, strconv.Itoa(track.ChannelCount))
		writeHashComponent(h, strings.ToLower(strings.TrimSpace(track.ChannelLayout)))
		writeHashComponent(h, strings.ToLower(strings.TrimSpace(track.BitRate)))

		if len(track.Attributes) > 0 {
			keys := make([]int, 0, len(track.Attributes))
			for key := range track.Attributes {
				keys = append(keys, key)
			}
			sort.Ints(keys)
			for _, key := range keys {
				writeHashComponent(h, strconv.Itoa(key))
				writeHashComponent(h, strings.ToLower(strings.TrimSpace(track.Attributes[key])))
			}
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}

func writeHashComponent(h hash.Hash, value string) {
	_, _ = h.Write([]byte(value))
	_, _ = h.Write([]byte{0})
}
