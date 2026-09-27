package auditgather

import "testing"

func TestAuditPathBoundaries(t *testing.T) {
	for _, tc := range []struct {
		path, root string
		want       bool
	}{
		{"/library/season/../movie.mkv", "/library", true},
		{"/library", "/library", true},
		{"/library-other/movie.mkv", "/library", false},
		{"/library/movie.mkv", "", false},
	} {
		if got := pathWithinRoot(tc.path, tc.root); got != tc.want {
			t.Errorf("pathWithinRoot(%q, %q) = %v", tc.path, tc.root, got)
		}
	}
}
