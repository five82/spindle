package auditgather

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestActiveDiskWaitIsWarningButResolvedWaitIsNot(t *testing.T) {
	r := &Report{Item: ItemSummary{Tasks: []TaskSummary{{Type: "ripping", State: "pending", Activities: []queue.Activity{{Operation: "disk_space", State: "waiting", Message: "Waiting for disk space: need 188 GiB, available 90 GiB"}}}}}}
	anomalies := detectAnomalies(r, &Analysis{})
	if len(anomalies) != 1 || anomalies[0].Category != "disk_space" || anomalies[0].Severity != "warning" || !strings.Contains(anomalies[0].Message, "188 GiB") {
		t.Fatalf("active wait = %+v", anomalies)
	}
	r.Item.Tasks[0].State = "done"
	if got := detectAnomalies(r, &Analysis{}); len(got) != 0 {
		t.Fatalf("resolved wait still flagged: %+v", got)
	}
}
