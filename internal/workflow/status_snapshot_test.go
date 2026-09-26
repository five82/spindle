package workflow

import (
	"testing"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
)

func TestPipelineStatusResolvesDependenciesAndCopiesHolders(t *testing.T) {
	m := newTestManager([]PipelineStage{
		{Stage: queue.StageIdentification, Claims: map[string]int{"drive": 1, "gpu": 1}},
		{Stage: queue.StageRipping, Claims: map[string]int{"drive": 1}},
		{Stage: queue.StageEncoding, DependsOn: []queue.Stage{queue.StageIdentification}, Claims: map[string]int{"gpu": 1}},
	})
	info := m.PipelineInfo()
	if len(info) != 3 || len(info[0].DependsOn) != 0 || len(info[0].Claims) != 2 || info[0].Claims[0] != "drive" || info[0].Claims[1] != "gpu" || len(info[1].DependsOn) != 1 || info[1].DependsOn[0] != string(queue.StageIdentification) || info[2].DependsOn[0] != string(queue.StageIdentification) {
		t.Fatalf("pipeline status: %+v", info)
	}
	holder := httpapi.ResourceHolder{ItemID: 12}
	if !m.reserve(map[string]int{"gpu": 1}, holder) {
		t.Fatal("could not claim GPU")
	}
	snap := m.SchedulerSnapshot()
	if snap["gpu"].Capacity != 1 || snap["gpu"].Used != 1 || len(snap["gpu"].Holders) != 1 || snap["drive"].Used != 0 {
		t.Fatalf("resource status: %+v", snap)
	}
	m.release(map[string]int{"gpu": 1}, holder)
	if snap["gpu"].Used != 1 || len(snap["gpu"].Holders) != 1 || m.SchedulerSnapshot()["gpu"].Used != 0 {
		t.Fatalf("snapshot changed after release: %+v", snap)
	}
}
