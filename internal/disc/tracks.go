package disc

// TrackType represents the general classification for a MakeMKV stream.
type TrackType string

const (
	TrackTypeUnknown  TrackType = "unknown"
	TrackTypeVideo    TrackType = "video"
	TrackTypeAudio    TrackType = "audio"
	TrackTypeSubtitle TrackType = "subtitle"
	TrackTypeData     TrackType = "data"
)

// Track captures the parsed metadata for a MakeMKV stream associated with a title.
type Track struct {
	StreamID      int
	Order         int
	Type          TrackType
	CodecID       string
	CodecShort    string
	CodecLong     string
	Language      string
	LanguageName  string
	Name          string
	ChannelCount  int
	ChannelLayout string
	BitRate       string
	Attributes    map[int]string
}

// IsAudio returns true when the track represents an audio stream.
func (t Track) IsAudio() bool {
	return t.Type == TrackTypeAudio
}

// IsCommentaryFlagged returns true when MakeMKV marked the stream as a special/commentary track.
func (t Track) IsCommentaryFlagged() bool {
	if len(t.Attributes) == 0 {
		return false
	}
	for id, value := range t.Attributes {
		if id == 3 || id == 28 { // language codes, not commentary flags
			continue
		}
		if id == 38 || id == 39 {
			if value == "c" || value == "C" {
				return true
			}
		}
	}
	return false
}
