package queue

// Stage represents a pipeline stage for a queue item.
type Stage string

const (
	StageIdentification        Stage = "identification"
	StageRipping               Stage = "ripping"
	StageEpisodeIdentification Stage = "episode_identification"
	StageEncoding              Stage = "encoding"
	StageAnalysis              Stage = "analysis"
	StageSubtitling            Stage = "subtitling"
	StageApply                 Stage = "apply"
	StageOrganizing            Stage = "organizing"
	StageCompleted             Stage = "completed"
	StageFailed                Stage = "failed"
)

// StageOrder is the single source of the pipeline stage enumeration, listing
// every execution stage in registration/display order. Terminal markers
// (StageCompleted, StageFailed) are not pipeline positions and are absent.
// workflow.ConfigureStages validates its template against this list, audit
// phase gates derive ordinals from it, and Item.ResumeStage validates retry
// targets against it.
var StageOrder = []Stage{
	StageIdentification,
	StageRipping,
	StageEpisodeIdentification,
	StageEncoding,
	StageAnalysis,
	StageSubtitling,
	StageApply,
	StageOrganizing,
}

// ReviewReasonUserStopped is the review reason appended when a user manually
// stops an item via "spindle queue cancel".
const ReviewReasonUserStopped = "Stop requested by user"

// humanStageLabels holds the user-facing label for every declared stage.
// TestHumanStageCoversAllStages enforces coverage of StageOrder plus the
// terminal markers.
var humanStageLabels = map[Stage]string{
	StageIdentification:        "identification",
	StageRipping:               "ripping",
	StageEpisodeIdentification: "episode ID",
	StageEncoding:              "encoding",
	StageAnalysis:              "audio analysis",
	StageSubtitling:            "subtitles",
	StageApply:                 "post-processing",
	StageOrganizing:            "library import",
	StageCompleted:             "completed",
	StageFailed:                "failed",
}

// HumanStage returns a user-facing label for a queue stage.
func HumanStage(stage Stage) string {
	if label, ok := humanStageLabels[stage]; ok {
		return label
	}
	return string(stage)
}
