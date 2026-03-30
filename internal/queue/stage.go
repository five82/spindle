package queue

// Stage represents a pipeline stage for a queue item.
type Stage string

const (
	StageIdentification       Stage = "identification"
	StageRipping              Stage = "ripping"
	StageEpisodeIdentification Stage = "episode_identification"
	StageEncoding             Stage = "encoding"
	StageAudioAnalysis        Stage = "audio_analysis"
	StageSubtitling           Stage = "subtitling"
	StageOrganizing           Stage = "organizing"
	StageCompleted            Stage = "completed"
	StageFailed               Stage = "failed"
)

// ReviewReasonUserStopped is the review reason appended when a user manually
// stops an item via "spindle queue stop".
const ReviewReasonUserStopped = "Stop requested by user"
