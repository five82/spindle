package queue

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsMissingParentDirectory(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "absent", "queue.db"))
	if store != nil || err == nil || !strings.Contains(err.Error(), "set pragma") {
		t.Fatalf("Open: %+v %v", store, err)
	}
}
