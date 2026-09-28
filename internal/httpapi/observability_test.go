package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func TestMovieProjectionUsesSelectedSourceAndDeliveredEvidence(t *testing.T) {
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "movie", Title: "Film"}, Titles: []ripspec.Title{{ID: 0, Name: "Extra", Duration: 60}, {ID: 3, Name: "Selected feature", Duration: 6000}}, Assets: ripspec.Assets{Ripped: []ripspec.Asset{{EpisodeKey: "main", TitleID: 3, Path: "rip.mkv"}}, Encoded: []ripspec.Asset{{EpisodeKey: "main", Path: "encoded.mkv"}}, Final: []ripspec.Asset{{EpisodeKey: "main", Path: "/library/film.mkv", SizeBytes: 500, Route: "library"}}}}
	env.Attributes.FinalValidation = &ripspec.FinalValidation{Passed: true, Entries: []ripspec.FinalValidationEntry{{EpisodeKey: "main", Passed: true, Error: "ffprobe unavailable"}}}
	env.Attributes.SubtitleGenerationResults = []ripspec.SubtitleGenRecord{{EpisodeKey: "main", Source: "none", SkipReason: "No verified candidate"}}
	env.Attributes.EncodeStats = []ripspec.EncodeStats{{EpisodeKey: "main", OriginalSizeBytes: 1000, EncodedSizeBytes: 400, Speed: 2}}
	data, err := env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	item := &queue.Item{ID: 4, RipSpecData: data, Stage: queue.StageCompleted}
	task := &queue.Task{ID: 99, Type: queue.StageEncoding, State: queue.TaskDone, Attempts: 2, EncodingDetailsJSON: `{"asset_key":"main","encoded_size":400}`}
	resp := ToItemResponse(item, []*queue.Task{task}, false)
	if resp.Source == nil || resp.Source.TitleID != 3 || resp.Source.Name != "Selected feature" {
		t.Fatalf("guessed source: %+v", resp.Source)
	}
	if len(resp.Episodes) != 1 {
		t.Fatal(resp.Episodes)
	}
	ep := resp.Episodes[0]
	if ep.Key != "main" || ep.FinalSizeBytes != 500 || ep.FinalRoute != "library" || ep.EncodeStats.EncodedSizeBytes != 400 {
		t.Fatalf("delivered/intermediate conflated: %+v", ep)
	}
	if ep.FinalValidation == nil || ep.FinalValidation.Error != "ffprobe unavailable" || ep.SubtitleSkipReason != "No verified candidate" {
		t.Fatalf("lost unknown/outcome: %+v", ep)
	}
	if resp.Tasks[0].ID != 99 || resp.Tasks[0].Attempts != 2 || string(resp.Tasks[0].Encoding) != task.EncodingDetailsJSON {
		t.Fatalf("lost scope: %+v", resp.Tasks)
	}
	if _, err = json.Marshal(resp); err != nil {
		t.Fatal(err)
	}
	env.Assets = ripspec.Assets{}
	data, err = env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	item.RipSpecData = data
	resp = ToItemResponse(item, nil, false)
	if resp.Source != nil || len(resp.Episodes) != 0 {
		t.Fatal("scanned title guessed as selection")
	}
}

func TestActivityAssetOwnershipIncludesConcurrentLanes(t *testing.T) {
	tasks := []*queue.Task{{State: queue.TaskRunning, ActiveAssetKey: "a", Activities: []queue.Activity{{ID: "transcription", AssetKey: "b", State: "running"}, {ID: "references", AssetKey: "c", State: "waiting"}, {ID: "old", AssetKey: "d", State: "done"}}}}
	keys := activeAssetKeys(tasks)
	if len(keys) != 2 || !keys["a"] || !keys["b"] {
		t.Fatal(keys)
	}
	tasks[0].State = queue.TaskPending
	if keys = activeAssetKeys(tasks); len(keys) != 0 {
		t.Fatal(keys)
	}
}
