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
