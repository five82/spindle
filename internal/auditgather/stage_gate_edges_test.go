package auditgather

import (
	"testing"

	"github.com/five82/spindle/internal/httpapi"
)

func TestComputeStageGateDoesNotAssumeParallelBranchStarted(t *testing.T) {
	item := &httpapi.ItemResponse{Stage: "ripping", Tasks: []httpapi.TaskResponse{
		{Type: "identification", State: "done"},
		{Type: "ripping", State: "done", DependsOn: []string{"identification"}},
		{Type: "episode_identification", State: "done", DependsOn: []string{"ripping"}},
		{Type: "analysis", State: "running", DependsOn: []string{"episode_identification"}},
		{Type: "encoding", State: "pending", DependsOn: []string{"identification"}},
		{Type: "subtitling", State: "pending", DependsOn: []string{"analysis"}},
	}}
	gate := computeStageGate(item, "tv", "tv", "dvd")
	if !gate.PhaseEpisodeID || !gate.PhaseCommentary || gate.PhaseEncoded || gate.PhaseCrop || gate.PhaseSubtitles || gate.PhaseExtVal {
		t.Fatalf("branch gate = %+v", gate)
	}
	item.Stage = "failed"
	item.FailedAtStage = "encoding"
	item.Tasks = nil
	gate = computeStageGate(item, "movie", "movie", "dvd")
	if gate.FurthestStage != "encoding" || !gate.PhaseEncoded || gate.PhaseEpisodeID || gate.PhaseExtVal {
		t.Fatalf("failed DVD gate = %+v", gate)
	}
}
