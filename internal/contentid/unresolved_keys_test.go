package contentid

import (
	"reflect"
	"testing"
)

func TestUnresolvedKeysPreserveRipOrder(t *testing.T) {
	rips := []ripFingerprint{{EpisodeKey: "third"}, {EpisodeKey: "first"}}
	if got := unresolvedKeysFromRips(rips); !reflect.DeepEqual(got, []string{"third", "first"}) {
		t.Fatalf("keys: %v", got)
	}
	if got := unresolvedKeysFromRips(nil); len(got) != 0 {
		t.Fatalf("empty keys: %v", got)
	}
}
