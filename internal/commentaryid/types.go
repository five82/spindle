package commentaryid

type TrackKind string

const (
	TrackKindSameAsPrimary    TrackKind = "same_as_primary"
	TrackKindCommentary       TrackKind = "commentary"
	TrackKindAudioDescription TrackKind = "audio_description"
	TrackKindMusicOnly        TrackKind = "music_only"
	TrackKindUnknown          TrackKind = "unknown"
)

type TrackDecision struct {
	Index      int       `json:"index"`
	Kind       TrackKind `json:"kind"`
	Confidence float64   `json:"confidence"`
	Reason     string    `json:"reason"`
}

type Refinement struct {
	PrimaryIndex int
	KeepIndices  []int
	Dropped      []TrackDecision
	Kept         []TrackDecision
}
